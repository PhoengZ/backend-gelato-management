package audit

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const auditWriteTimeout = 500 * time.Millisecond

// Preserve a live request's existing storage budget rather than making audit
// durability worse for a slow-but-valid database. Cancelled/expired/unbounded
// callers get a short independent grace period; configuration caps requests at
// 30 seconds, and the audit writer keeps that same upper bound.
func auditWriteBudget(ctx context.Context) time.Duration {
	if ctx.Err() == nil {
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 30*time.Second {
				return 30 * time.Second
			}
			if remaining > auditWriteTimeout {
				return remaining
			}
		}
	}
	return auditWriteTimeout
}

func (l *PostgresAuditLogger) insert(ctx context.Context, record AuditRecord, metadata []byte) error {
	const query = `INSERT INTO auth_audit_logs (id,event_type,user_id,ip_address,user_agent,metadata,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`
	args := []any{record.ID, record.EventType, record.UserID, record.IPAddress, record.UserAgent, metadata, record.CreatedAt}
	tag, err := l.pool.Exec(ctx, query, args...)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	// A lost acknowledgement can be retried without duplicate evidence. An ID
	// collision with different facts is an error, never an audit-row overwrite.
	var same bool
	err = l.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth_audit_logs WHERE id=$1 AND event_type=$2
AND user_id IS NOT DISTINCT FROM $3::uuid AND ip_address=$4 AND user_agent=$5
AND metadata=$6::jsonb AND created_at=$7)`, args...).Scan(&same)
	if err != nil {
		return err
	}
	if !same {
		return errors.New("audit event identity conflicts with existing evidence")
	}
	return nil
}

func retryAuditWrite(ctx context.Context, write func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = write(); err == nil || !retryableAuditError(err) || ctx.Err() != nil || attempt == 2 {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func retryableAuditError(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "40001" || pg.Code == "40P01" || strings.HasPrefix(pg.Code, "08") || pg.Code == "57P01" || pg.Code == "57P02" || pg.Code == "57P03"
	}
	var network net.Error
	return errors.As(err, &network) || pgconn.SafeToRetry(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}
