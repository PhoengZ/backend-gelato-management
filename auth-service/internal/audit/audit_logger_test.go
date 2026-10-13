package audit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInMemoryAuditLoggerRecordsEvents(t *testing.T) {
	logger := NewInMemoryAuditLogger()
	ctx := context.Background()

	userID := uuid.New()
	managerID := uuid.New()
	ip := "203.0.113.1"
	userAgent := "Mozilla/5.0 TestBrowser"

	// 1. Log USER_REGISTERED
	err := logger.LogUserRegistered(ctx, userID, ip, userAgent, map[string]any{
		"email": "user@example.com",
		"role":  "CUSTOMER",
	})
	if err != nil {
		t.Fatalf("LogUserRegistered failed: %v", err)
	}

	// 2. Log LOGIN_SUCCESS
	err = logger.LogLoginSuccess(ctx, userID, ip, userAgent, map[string]any{
		"email": "user@example.com",
	})
	if err != nil {
		t.Fatalf("LogLoginSuccess failed: %v", err)
	}

	// 3. Log LOGIN_FAILED
	err = logger.LogLoginFailed(ctx, "attacker@example.com", nil, ip, userAgent, "bad_password")
	if err != nil {
		t.Fatalf("LogLoginFailed failed: %v", err)
	}

	// 4. Log ROLE_CHANGED
	err = logger.LogRoleChanged(ctx, userID, "CUSTOMER", "STAFF", &managerID, ip, userAgent)
	if err != nil {
		t.Fatalf("LogRoleChanged failed: %v", err)
	}

	records := logger.Records()
	if len(records) != 4 {
		t.Fatalf("expected 4 audit records, got %d", len(records))
	}

	// Verify Record 1: USER_REGISTERED
	if records[0].EventType != EventUserRegistered {
		t.Errorf("record 0 event_type expected %s, got %s", EventUserRegistered, records[0].EventType)
	}
	if records[0].UserID == nil || *records[0].UserID != userID {
		t.Errorf("record 0 user_id mismatch")
	}
	if records[0].IPAddress != ip || records[0].UserAgent != userAgent {
		t.Errorf("record 0 client context mismatch")
	}

	// Verify Record 2: LOGIN_SUCCESS
	if records[1].EventType != EventLoginSuccess {
		t.Errorf("record 1 event_type expected %s, got %s", EventLoginSuccess, records[1].EventType)
	}
	if records[1].UserID == nil || *records[1].UserID != userID {
		t.Errorf("record 1 user_id mismatch")
	}

	// Verify Record 3: LOGIN_FAILED
	if records[2].EventType != EventLoginFailed {
		t.Errorf("record 2 event_type expected %s, got %s", EventLoginFailed, records[2].EventType)
	}
	if records[2].UserID != nil {
		t.Errorf("record 3 user_id should be nil")
	}
	if records[2].Metadata["email"] != "attacker@example.com" || records[2].Metadata["reason"] != "bad_password" {
		t.Errorf("record 3 metadata mismatch: %+v", records[2].Metadata)
	}

	// Verify Record 4: ROLE_CHANGED
	if records[3].EventType != EventRoleChanged {
		t.Errorf("record 3 event_type expected %s, got %s", EventRoleChanged, records[3].EventType)
	}
	if records[3].Metadata["old_role"] != "CUSTOMER" || records[3].Metadata["new_role"] != "STAFF" {
		t.Errorf("record 3 roles mismatch: %+v", records[3].Metadata)
	}
	if records[3].Metadata["changed_by"] != managerID.String() {
		t.Errorf("record 3 changed_by mismatch: %+v", records[3].Metadata)
	}
}

func TestPostgresAuditLoggerNilPoolSafe(t *testing.T) {
	logger := NewPostgresAuditLogger(nil)
	ctx := context.Background()
	userID := uuid.New()

	// Should safely return nil without panicking when pool is nil
	err := logger.LogUserRegistered(ctx, userID, "127.0.0.1", "curl/8.0", nil)
	if err != nil {
		t.Fatalf("nil pool logger should return nil error, got %v", err)
	}

	err = logger.Log(ctx, AuditRecord{
		ID:        uuid.New(),
		EventType: EventLoginSuccess,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("nil pool logger should return nil error, got %v", err)
	}
}

func TestNoopAuditLogger(t *testing.T) {
	logger := NewNoopAuditLogger()
	ctx := context.Background()
	userID := uuid.New()

	if err := logger.LogUserRegistered(ctx, userID, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := logger.LogLoginSuccess(ctx, userID, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := logger.LogLoginFailed(ctx, "a@b.com", nil, "", "", "reason"); err != nil {
		t.Fatal(err)
	}
	if err := logger.LogRoleChanged(ctx, userID, "CUSTOMER", "STAFF", nil, "", ""); err != nil {
		t.Fatal(err)
	}
}
