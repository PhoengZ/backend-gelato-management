package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"auth-service/internal/idempotency"
	"auth-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SignupReplay is an operation receipt, never a second authority for current users.
// The original salted bcrypt hash verifies retries without storing plaintext or
// a fast password digest. It expires with the receipt after 24 hours.
type SignupReplay struct {
	User         model.User
	PasswordHash string
}

type SignupReplayRepository interface {
	LookupSignup(context.Context, uuid.UUID, []byte) (*SignupReplay, error)
	CreateSignup(context.Context, *model.User, *model.OutboxEvent, uuid.UUID, []byte) (*SignupReplay, bool, error)
}

func scanSignup(row rowScanner, fingerprint []byte) (*SignupReplay, error) {
	var savedHash, response []byte
	var result SignupReplay
	if err := row.Scan(&savedHash, &result.PasswordHash, &response); err != nil {
		return nil, err
	}
	if !bytes.Equal(fingerprint, savedHash) {
		return nil, idempotency.ErrConflict
	}
	if json.Unmarshal(response, &result.User) != nil || result.User.ID == uuid.Nil || result.User.Role != model.RoleCustomer || result.User.CreatedAt.IsZero() || result.PasswordHash == "" {
		return nil, idempotency.ErrUnavailable
	}
	return &result, nil
}

func (r *PostgresUserRepository) LookupSignup(ctx context.Context, key uuid.UUID, fingerprint []byte) (*SignupReplay, error) {
	result, err := scanSignup(r.pool.QueryRow(ctx, `SELECT request_hash,password_hash,response
		FROM auth_signup_idempotency WHERE key=$1 AND expires_at>clock_timestamp()`, key), fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil && !errors.Is(err, idempotency.ErrConflict) {
		return nil, idempotency.ErrUnavailable
	}
	return result, err
}

func (r *PostgresUserRepository) CreateSignup(ctx context.Context, user *model.User, event *model.OutboxEvent, key uuid.UUID, fingerprint []byte) (*SignupReplay, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, idempotency.ErrUnavailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err := tx.Exec(ctx, "DELETE FROM auth_signup_idempotency WHERE key=$1 AND expires_at<=clock_timestamp()", key); err != nil {
		return nil, false, idempotency.ErrUnavailable
	}
	tag, err := tx.Exec(ctx, `INSERT INTO auth_signup_idempotency(key,request_hash,password_hash)
		VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, key, fingerprint, user.PasswordHash)
	if err != nil {
		return nil, false, idempotency.ErrUnavailable
	}
	if tag.RowsAffected() == 0 {
		// The unique key waits for the first writer's transaction; this statement
		// then sees its completed receipt. No user/outbox/audit is created again.
		result, err := scanSignup(tx.QueryRow(ctx, `SELECT request_hash,password_hash,response
			FROM auth_signup_idempotency WHERE key=$1`, key), fingerprint)
		if err != nil && !errors.Is(err, idempotency.ErrConflict) {
			err = idempotency.ErrUnavailable
		}
		return result, true, err
	}
	if err := insertUserAndOutbox(ctx, tx, user, event); err != nil {
		return nil, false, err
	}
	response, err := json.Marshal(user)
	if err != nil {
		return nil, false, idempotency.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, "UPDATE auth_signup_idempotency SET response=$2 WHERE key=$1", key, response); err != nil {
		return nil, false, idempotency.ErrUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, idempotency.ErrUnavailable
	}
	return &SignupReplay{User: *user, PasswordHash: user.PasswordHash}, false, nil
}

// CleanupSignupReceipts removes expired credential proofs in bounded batches.
// SKIP LOCKED applies only to receipts, never to users or Inventory stock.
func (r *PostgresUserRepository) CleanupSignupReceipts(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM auth_signup_idempotency WHERE key IN (
		SELECT key FROM auth_signup_idempotency WHERE expires_at<=clock_timestamp()
		ORDER BY expires_at,key LIMIT 1000 FOR UPDATE SKIP LOCKED)`)
	return tag.RowsAffected(), err
}

func (r *PostgresUserRepository) RunSignupReceiptCleanup(ctx context.Context, report func(error)) {
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 2*time.Second)
		count, err := r.CleanupSignupReceipts(work)
		cancel()
		if err == nil && count == 1000 {
			continue
		}
		if err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
