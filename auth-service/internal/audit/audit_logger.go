package audit

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	EventUserRegistered = "USER_REGISTERED"
	EventLoginSuccess   = "LOGIN_SUCCESS"
	EventLoginFailed    = "LOGIN_FAILED"
	EventLoginError     = "LOGIN_ERROR"
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
	record.CreatedAt = record.CreatedAt.UTC().Truncate(time.Microsecond)
	record = normalizeRecord(record)
	metadataJSON, err := json.Marshal(record.Metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	// A client disconnect must not cancel evidence for an already completed
	// authentication operation. The detached write has its own small budget.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteBudget(ctx))
	defer cancel()
	err = retryAuditWrite(writeCtx, func() error { return l.insert(writeCtx, record, metadataJSON) })
	if err != nil {
		log.Printf("failed to write auth audit log: error_type=%T", err)
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
	l.records = append(l.records, normalizeRecord(record))
	return nil
}

// PostgreSQL varchar(512) counts characters, not UTF-8 bytes. Any truncation or
// replacement is explicit in audit metadata rather than silently losing a row.
func normalizeRecord(record AuditRecord) AuditRecord {
	metadata := make(map[string]any, len(record.Metadata)+3)
	for key, value := range record.Metadata {
		metadata[key] = value
	}
	agent := record.UserAgent
	if !utf8.ValidString(agent) {
		agent = strings.ToValidUTF8(agent, "\uFFFD")
		metadata["user_agent_invalid_utf8"] = true
	}
	if strings.ContainsRune(agent, '\x00') {
		agent = strings.ReplaceAll(agent, "\x00", "\uFFFD")
		metadata["user_agent_sanitized"] = true
	}
	runes := []rune(agent)
	if len(runes) > 512 {
		agent = string(runes[:512])
		metadata["user_agent_truncated"] = true
		metadata["user_agent_original_characters"] = len(runes)
	}
	record.UserAgent, record.Metadata = agent, metadata
	return record
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
