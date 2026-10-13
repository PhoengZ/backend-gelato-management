package audit

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	EventUserRegistered = "USER_REGISTERED"
	EventLoginSuccess   = "LOGIN_SUCCESS"
	EventLoginFailed    = "LOGIN_FAILED"
	EventRoleChanged    = "ROLE_CHANGED"
)

type AuditRecord struct {
	ID        uuid.UUID      `json:"id"`
	EventType string         `json:"event_type"`
	UserID    *uuid.UUID     `json:"user_id,omitempty"`
	IPAddress string         `json:"ip_address"`
	UserAgent string         `json:"user_agent"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type AuditLogger interface {
	Log(ctx context.Context, record AuditRecord) error
	LogUserRegistered(ctx context.Context, userID uuid.UUID, ip, userAgent string, metadata map[string]any) error
	LogLoginSuccess(ctx context.Context, userID uuid.UUID, ip, userAgent string, metadata map[string]any) error
	LogLoginFailed(ctx context.Context, email string, userID *uuid.UUID, ip, userAgent, reason string) error
	LogRoleChanged(ctx context.Context, userID uuid.UUID, oldRole, newRole string, changedBy *uuid.UUID, ip, userAgent string) error
}

type PostgresAuditLogger struct {
	pool *pgxpool.Pool
}

func NewPostgresAuditLogger(pool *pgxpool.Pool) *PostgresAuditLogger {
	return &PostgresAuditLogger{pool: pool}
}

func (l *PostgresAuditLogger) Log(ctx context.Context, record AuditRecord) error {
	if l.pool == nil {
		return nil
	}
	if record.ID == uuid.Nil {
		record.ID = uuid.New()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	metadataJSON, err := json.Marshal(record.Metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	const query = `
		INSERT INTO auth_audit_logs (id, event_type, user_id, ip_address, user_agent, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err = l.pool.Exec(ctx, query,
		record.ID,
		record.EventType,
		record.UserID,
		record.IPAddress,
		record.UserAgent,
		metadataJSON,
		record.CreatedAt,
	)
	if err != nil {
		log.Printf("failed to write audit log: %v", err)
		return err
	}
	return nil
}

func (l *PostgresAuditLogger) LogUserRegistered(ctx context.Context, userID uuid.UUID, ip, userAgent string, metadata map[string]any) error {
	return l.Log(ctx, AuditRecord{
		EventType: EventUserRegistered,
		UserID:    &userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  metadata,
	})
}

func (l *PostgresAuditLogger) LogLoginSuccess(ctx context.Context, userID uuid.UUID, ip, userAgent string, metadata map[string]any) error {
	return l.Log(ctx, AuditRecord{
		EventType: EventLoginSuccess,
		UserID:    &userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  metadata,
	})
}

func (l *PostgresAuditLogger) LogLoginFailed(ctx context.Context, email string, userID *uuid.UUID, ip, userAgent, reason string) error {
	meta := map[string]any{
		"email":  email,
		"reason": reason,
	}
	return l.Log(ctx, AuditRecord{
		EventType: EventLoginFailed,
		UserID:    userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  meta,
	})
}

func (l *PostgresAuditLogger) LogRoleChanged(ctx context.Context, userID uuid.UUID, oldRole, newRole string, changedBy *uuid.UUID, ip, userAgent string) error {
	meta := map[string]any{
		"old_role": oldRole,
		"new_role": newRole,
	}
	if changedBy != nil {
		meta["changed_by"] = changedBy.String()
	}
	return l.Log(ctx, AuditRecord{
		EventType: EventRoleChanged,
		UserID:    &userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  meta,
	})
}

// InMemoryAuditLogger provides thread-safe in-memory audit log recording for testing.
type InMemoryAuditLogger struct {
	mu      sync.RWMutex
	records []AuditRecord
}

func NewInMemoryAuditLogger() *InMemoryAuditLogger {
	return &InMemoryAuditLogger{
		records: make([]AuditRecord, 0),
	}
}

func (l *InMemoryAuditLogger) Log(ctx context.Context, record AuditRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if record.ID == uuid.Nil {
		record.ID = uuid.New()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	l.records = append(l.records, record)
	return nil
}

func (l *InMemoryAuditLogger) LogUserRegistered(ctx context.Context, userID uuid.UUID, ip, userAgent string, metadata map[string]any) error {
	return l.Log(ctx, AuditRecord{
		EventType: EventUserRegistered,
		UserID:    &userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  metadata,
	})
}

func (l *InMemoryAuditLogger) LogLoginSuccess(ctx context.Context, userID uuid.UUID, ip, userAgent string, metadata map[string]any) error {
	return l.Log(ctx, AuditRecord{
		EventType: EventLoginSuccess,
		UserID:    &userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  metadata,
	})
}

func (l *InMemoryAuditLogger) LogLoginFailed(ctx context.Context, email string, userID *uuid.UUID, ip, userAgent, reason string) error {
	meta := map[string]any{
		"email":  email,
		"reason": reason,
	}
	return l.Log(ctx, AuditRecord{
		EventType: EventLoginFailed,
		UserID:    userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  meta,
	})
}

func (l *InMemoryAuditLogger) LogRoleChanged(ctx context.Context, userID uuid.UUID, oldRole, newRole string, changedBy *uuid.UUID, ip, userAgent string) error {
	meta := map[string]any{
		"old_role": oldRole,
		"new_role": newRole,
	}
	if changedBy != nil {
		meta["changed_by"] = changedBy.String()
	}
	return l.Log(ctx, AuditRecord{
		EventType: EventRoleChanged,
		UserID:    &userID,
		IPAddress: ip,
		UserAgent: userAgent,
		Metadata:  meta,
	})
}

func (l *InMemoryAuditLogger) Records() []AuditRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()

	copied := make([]AuditRecord, len(l.records))
	copy(copied, l.records)
	return copied
}

func (l *InMemoryAuditLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = make([]AuditRecord, 0)
}

// NoopAuditLogger discards all audit events.
type NoopAuditLogger struct{}

func NewNoopAuditLogger() *NoopAuditLogger {
	return &NoopAuditLogger{}
}

func (l *NoopAuditLogger) Log(_ context.Context, _ AuditRecord) error { return nil }
func (l *NoopAuditLogger) LogUserRegistered(_ context.Context, _ uuid.UUID, _, _ string, _ map[string]any) error {
	return nil
}
func (l *NoopAuditLogger) LogLoginSuccess(_ context.Context, _ uuid.UUID, _, _ string, _ map[string]any) error {
	return nil
}
func (l *NoopAuditLogger) LogLoginFailed(_ context.Context, _ string, _ *uuid.UUID, _, _, _ string) error {
	return nil
}
func (l *NoopAuditLogger) LogRoleChanged(_ context.Context, _ uuid.UUID, _, _ string, _ *uuid.UUID, _, _ string) error {
	return nil
}
