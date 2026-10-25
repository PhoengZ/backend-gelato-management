package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"auth-service/internal/audit"
	"github.com/google/uuid"
)

func TestAuthAuditSurvivesRequestCancellationWithoutDuplicateOrOverwrite(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_%v", expired), func(t *testing.T) {
			db := hardeningDatabase(t)
			logger := audit.NewPostgresAuditLogger(db)
			ctx, cancel := context.WithCancel(context.Background())
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			record := audit.AuditRecord{ID: uuid.New(), EventType: audit.EventLoginFailed,
				IPAddress: "127.0.0.1", UserAgent: "Audit resilience test", CreatedAt: time.Now().UTC(),
				Metadata: map[string]any{"reason": "invalid_credentials"}}
			if err := logger.Log(ctx, record); err != nil {
				t.Fatal("request cancellation lost committed-action evidence", err)
			}
			if err := logger.Log(context.Background(), record); err != nil {
				t.Fatal("same event retry was not safe", err)
			}
			var count int
			if err := db.QueryRow(context.Background(), "SELECT count(*) FROM auth_audit_logs WHERE id=$1", record.ID).Scan(&count); err != nil || count != 1 {
				t.Fatal("same event duplicated audit history", count, err)
			}
			changed := record
			changed.Metadata = map[string]any{"reason": "different_facts"}
			if err := logger.Log(context.Background(), changed); err == nil {
				t.Fatal("event identity silently rebound to different facts")
			}
			var reason string
			if err := db.QueryRow(context.Background(), "SELECT metadata->>'reason' FROM auth_audit_logs WHERE id=$1", record.ID).Scan(&reason); err != nil || reason != "invalid_credentials" {
				t.Fatal("audit history was overwritten", reason, err)
			}
		})
	}
}

func TestAuthAuditRetriesOnlyTransientPostgresFailures(t *testing.T) {
	for _, transient := range []bool{true, false} {
		t.Run(fmt.Sprintf("transient_%v", transient), func(t *testing.T) {
			db := hardeningDatabase(t)
			code, attempts := "40001", int64(3)
			if !transient {
				code, attempts = "23514", 1
			}
			query := fmt.Sprintf(`CREATE SEQUENCE audit_retry_attempts;
CREATE FUNCTION fail_first_audits() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF nextval('audit_retry_attempts') < 3 THEN
  RAISE EXCEPTION 'test audit write failure' USING ERRCODE = '%s';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER fail_first_audits BEFORE INSERT ON auth_audit_logs FOR EACH ROW EXECUTE FUNCTION fail_first_audits()`, code)
			if _, err := db.Exec(context.Background(), query); err != nil {
				t.Fatal(err)
			}
			err := audit.NewPostgresAuditLogger(db).Log(context.Background(), audit.AuditRecord{EventType: "RETRY_TEST"})
			if (transient && err != nil) || (!transient && err == nil) {
				t.Fatal("incorrect transient/permanent result", err)
			}
			var actualAttempts, rows int64
			if err := db.QueryRow(context.Background(), "SELECT (SELECT last_value FROM audit_retry_attempts),(SELECT count(*) FROM auth_audit_logs)").Scan(&actualAttempts, &rows); err != nil || actualAttempts != attempts {
				t.Fatal("unbounded or missing retries", actualAttempts, attempts, err)
			}
			if (transient && rows != 1) || (!transient && rows != 0) {
				t.Fatal("incorrect persisted audit count", rows)
			}
		})
	}
}

func TestAuthAuditWriteHasItsOwnBoundedBudget(t *testing.T) {
	db := hardeningDatabase(t)
	if _, err := db.Exec(context.Background(), `CREATE FUNCTION slow_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(3); RETURN NEW; END $$;
CREATE TRIGGER slow_audit BEFORE INSERT ON auth_audit_logs FOR EACH ROW EXECUTE FUNCTION slow_audit()`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := audit.NewPostgresAuditLogger(db).Log(context.Background(), audit.AuditRecord{EventType: "BUDGET_TEST"})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatal("audit write was unbounded", time.Since(start), err)
	}
}

func TestAuthAuditPreservesLiveRequestBudgetForSlowDatabase(t *testing.T) {
	db := hardeningDatabase(t)
	if _, err := db.Exec(context.Background(), `CREATE FUNCTION delayed_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.7); RETURN NEW; END $$;
CREATE TRIGGER delayed_audit BEFORE INSERT ON auth_audit_logs FOR EACH ROW EXECUTE FUNCTION delayed_audit()`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := audit.NewPostgresAuditLogger(db).Log(ctx, audit.AuditRecord{EventType: "SLOW_VALID_TEST"}); err != nil {
		t.Fatal("audit grace period shortened a legitimate live request", err)
	}
	var rows int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM auth_audit_logs WHERE event_type='SLOW_VALID_TEST'").Scan(&rows); err != nil || rows != 1 {
		t.Fatal("slow valid audit was lost", rows, err)
	}
}
