package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"auth-service/internal/audit"
	"auth-service/internal/auth"
	"auth-service/internal/idempotency"
	"auth-service/internal/model"
	"auth-service/internal/repository"
	"auth-service/internal/router"
	"auth-service/internal/service"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func signupReplayApp(t *testing.T, db *pgxpool.Pool) (*fiber.App, service.AuthService) {
	t.Helper()
	tokens := auth.NewTokenManager("signup_replay_test_secret_more_than_32_bytes", "gelatoflow-auth", "gelatoflow-api", 15*time.Minute)
	svc, err := service.NewAuthService(repository.NewPostgresUserRepository(db), tokens, 10)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	router.Setup(app, svc, tokens, router.Options{RequestTimeout: 5 * time.Second, Pool: db, AuditLogger: audit.NewPostgresAuditLogger(db)})
	return app, svc
}

func signupCall(t *testing.T, app *fiber.App, path string, input any, keys ...string) (int, []byte, http.Header) {
	t.Helper()
	body, _ := json.Marshal(input)
	req := httptest.NewRequest("POST", "/api/v1/auth/"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, key := range keys {
		req.Header.Add("Idempotency-Key", key)
	}
	response, err := app.Test(req, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, raw, response.Header
}

func signupInput() service.SignUpInput {
	return service.SignUpInput{Email: uuid.NewString() + "@example.invalid", DisplayName: "Signup Replay", Password: "Replay-password-123!"}
}

func TestSignupHeaderReplaysOriginalWithoutRepeatingUserOutboxOrAudit(t *testing.T) {
	db := hardeningDatabase(t)
	app, _ := signupReplayApp(t, db)
	in, key := signupInput(), uuid.NewString()
	status, original, _ := signupCall(t, app, "signup", in, key)
	if status != 201 {
		t.Fatal("initial signup failed", status)
	}
	var user model.User
	if err := json.Unmarshal(original, &user); err != nil {
		t.Fatal(err)
	}
	var expiry time.Time
	if err := db.QueryRow(context.Background(), "SELECT expires_at FROM auth_signup_idempotency WHERE key=$1", key).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	// The response is the original operation, not a new current-account read.
	if _, err := db.Exec(context.Background(), "UPDATE users SET role='MANAGER',status='DISABLED',display_name='Changed Later' WHERE id=$1", user.ID); err != nil {
		t.Fatal(err)
	}
	restarted, _ := signupReplayApp(t, db)
	in.Email = "  " + strings.ToUpper(in.Email) + "  "
	in.DisplayName = "  " + in.DisplayName + "  "
	status, replay, headers := signupCall(t, restarted, "signup", in, strings.ToUpper(key))
	if status != 201 || !bytes.Equal(original, replay) || headers.Get("Idempotency-Replayed") != "true" {
		t.Fatal("restart/normalization did not replay original response", status)
	}
	for _, table := range []string{"users", "outbox_events", "auth_audit_logs", "auth_signup_idempotency"} {
		var count int
		if err := db.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatal("replay duplicated facts", table, count, err)
		}
	}
	var after time.Time
	var stored string
	if err := db.QueryRow(context.Background(), "SELECT expires_at,row_to_json(auth_signup_idempotency)::text FROM auth_signup_idempotency WHERE key=$1", key).Scan(&after, &stored); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(expiry) || strings.Contains(stored, in.Password) || bytes.Contains(replay, []byte("password_hash")) || bytes.Contains(replay, []byte("IdempotentReplay")) {
		t.Fatal("receipt extended TTL or exposed credentials/internal metadata")
	}
}

func TestSignupKeyConflictsOnChangedPasswordOrIdentity(t *testing.T) {
	db := hardeningDatabase(t)
	app, _ := signupReplayApp(t, db)
	in, key := signupInput(), uuid.NewString()
	if status, _, _ := signupCall(t, app, "signup", in, key); status != 201 {
		t.Fatal(status)
	}
	for _, field := range []string{"password", "email", "display_name"} {
		t.Run(field, func(t *testing.T) {
			changed := in
			switch field {
			case "password":
				changed.Password = "Different-password-123!"
			case "email":
				changed.Email = uuid.NewString() + "@example.invalid"
			case "display_name":
				changed.DisplayName = "Changed"
			}
			status, body, _ := signupCall(t, app, "signup", changed, key)
			if status != 409 || !bytes.Contains(body, []byte("IDEMPOTENCY_CONFLICT")) {
				t.Fatal("changed input accepted", status)
			}
		})
	}
	var count int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatal("conflict created account", count, err)
	}
}

func TestSignupConcurrentClaimsCreateExactlyOneAccountAndOutbox(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprint(different), func(t *testing.T) {
			db := hardeningDatabase(t)
			_, svc := signupReplayApp(t, db)
			in, key := signupInput(), uuid.New()
			start := make(chan struct{})
			results := make(chan error, 20)
			ids := make(chan uuid.UUID, 20)
			for i := 0; i < 20; i++ {
				go func(i int) {
					<-start
					input := in
					if different {
						input.Email = fmt.Sprintf("r%d-%s", i, in.Email)
					}
					user, err := svc.SignUp(idempotency.WithKey(context.Background(), key), input)
					if err == nil {
						ids <- user.ID
					}
					results <- err
				}(i)
			}
			close(start)
			success, conflicts := 0, 0
			for i := 0; i < 20; i++ {
				err := <-results
				if err == nil {
					success++
				} else if errors.Is(err, idempotency.ErrConflict) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if !different && success != 20 || different && (success != 1 || conflicts != 19) {
				t.Fatal("concurrent outcome mismatch", success, conflicts)
			}
			var first uuid.UUID
			for i := 0; i < success; i++ {
				id := <-ids
				if i == 0 {
					first = id
				} else if id != first {
					t.Fatal("same key created different users")
				}
			}
			for _, table := range []string{"users", "outbox_events", "auth_signup_idempotency"} {
				var count int
				if err := db.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
					t.Fatal(table, count, err)
				}
			}
		})
	}
}

func TestSignupResponseWriteFailureRollsBackEveryFact(t *testing.T) {
	db := hardeningDatabase(t)
	app, _ := signupReplayApp(t, db)
	in, key := signupInput(), uuid.NewString()
	if _, err := db.Exec(context.Background(), `CREATE FUNCTION reject_signup_response() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture response failure'; END $$; CREATE TRIGGER reject_signup_response BEFORE UPDATE ON auth_signup_idempotency FOR EACH ROW EXECUTE FUNCTION reject_signup_response()`); err != nil {
		t.Fatal(err)
	}
	status, _, _ := signupCall(t, app, "signup", in, key)
	if status != 503 {
		t.Fatal("response persistence failure was accepted", status)
	}
	for _, table := range []string{"users", "outbox_events", "auth_signup_idempotency", "auth_audit_logs"} {
		var count int
		if err := db.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial signup after receipt failure", table, count, err)
		}
	}
	if _, err := db.Exec(context.Background(), "DROP TRIGGER reject_signup_response ON auth_signup_idempotency"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := signupCall(t, app, "signup", in, key); status != 201 {
		t.Fatal("retry after rollback failed", status)
	}
}

type signupLostAckRepository struct {
	*repository.PostgresUserRepository
	lost atomic.Bool
}

func (r *signupLostAckRepository) CreateSignup(ctx context.Context, u *model.User, e *model.OutboxEvent, key uuid.UUID, hash []byte) (*repository.SignupReplay, bool, error) {
	result, replay, err := r.PostgresUserRepository.CreateSignup(ctx, u, e, key, hash)
	if err == nil && !replay && !r.lost.Swap(true) {
		return nil, false, idempotency.ErrUnavailable
	}
	return result, replay, err
}

func TestSignupAmbiguousCommitAcknowledgementReplaysOriginal(t *testing.T) {
	db := hardeningDatabase(t)
	repo := &signupLostAckRepository{PostgresUserRepository: repository.NewPostgresUserRepository(db)}
	tokens := auth.NewTokenManager("signup_test_secret_that_has_more_than_32_bytes", "gelatoflow-auth", "gelatoflow-api", 15*time.Minute)
	svc, err := service.NewAuthService(repo, tokens, 10)
	if err != nil {
		t.Fatal(err)
	}
	in, key := signupInput(), uuid.New()
	ctx := idempotency.WithKey(context.Background(), key)
	if _, err := svc.SignUp(ctx, in); !errors.Is(err, idempotency.ErrUnavailable) {
		t.Fatal("lost acknowledgement not simulated", err)
	}
	user, err := svc.SignUp(ctx, in)
	if err != nil || !user.IdempotentReplay {
		t.Fatal("retry lost committed original", err)
	}
	var count int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM outbox_events").Scan(&count); err != nil || count != 1 {
		t.Fatal("ambiguous acknowledgement repeated event", count, err)
	}
}

func TestSignupKeyValidationAndLegacyNoKeyBehavior(t *testing.T) {
	db := hardeningDatabase(t)
	app, _ := signupReplayApp(t, db)
	for _, keys := range [][]string{{""}, {"bad"}, {uuid.Nil.String()}, {uuid.NewString(), uuid.NewString()}} {
		if status, _, _ := signupCall(t, app, "signup", signupInput(), keys...); status != 400 {
			t.Fatal("invalid key accepted", status)
		}
	}
	in := signupInput()
	if status, _, _ := signupCall(t, app, "signup", in); status != 201 {
		t.Fatal(status)
	}
	if status, body, _ := signupCall(t, app, "signup", in); status != 409 || !bytes.Contains(body, []byte("EMAIL_ALREADY_EXISTS")) {
		t.Fatal("no-key contract changed", status)
	}
}

func TestSignupMissingOrCorruptReplayStorageFailsClosedWithoutBreakingLegacy(t *testing.T) {
	db := hardeningDatabase(t)
	app, _ := signupReplayApp(t, db)
	in, key := signupInput(), uuid.NewString()
	if status, _, _ := signupCall(t, app, "signup", in, key); status != 201 {
		t.Fatal(status)
	}
	if _, err := db.Exec(context.Background(), "UPDATE auth_signup_idempotency SET response='{}'::jsonb WHERE key=$1", key); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := signupCall(t, app, "signup", in, key); status != 503 {
		t.Fatal("corrupt receipt returned success", status)
	}
	if _, err := db.Exec(context.Background(), "ALTER TABLE auth_signup_idempotency RENAME TO unavailable_signup_receipts"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := signupCall(t, app, "signup", signupInput(), uuid.NewString()); status != 503 {
		t.Fatal("missing replay storage accepted keyed mutation", status)
	}
	if status, _, _ := signupCall(t, app, "signup", signupInput()); status != 201 {
		t.Fatal("optional feature broke no-key callers", status)
	}
}

func TestSignupExpiryAndBoundedCleanupReleaseOnlyExpiredReceipts(t *testing.T) {
	db := hardeningDatabase(t)
	app, _ := signupReplayApp(t, db)
	repo := repository.NewPostgresUserRepository(db)
	key := uuid.NewString()
	if status, _, _ := signupCall(t, app, "signup", signupInput(), key); status != 201 {
		t.Fatal(status)
	}
	if _, err := db.Exec(context.Background(), "UPDATE auth_signup_idempotency SET created_at=clock_timestamp()-interval '25 hours',expires_at=clock_timestamp()-interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	if status, _, h := signupCall(t, app, "signup", signupInput(), key); status != 201 || h.Get("Idempotency-Replayed") != "" {
		t.Fatal("expired key was not reusable", status)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO auth_signup_idempotency(key,request_hash,password_hash,created_at,expires_at) SELECT md5(i::text)::uuid,decode(repeat('00',32),'hex'),'fixture-proof',clock_timestamp()-interval '25 hours',clock_timestamp()-interval '1 hour' FROM generate_series(1,1002) AS i`); err != nil {
		t.Fatal(err)
	}
	if count, err := repo.CleanupSignupReceipts(context.Background()); err != nil || count != 1000 {
		t.Fatal("cleanup batch unbounded or incomplete", count, err)
	}
	if count, err := repo.CleanupSignupReceipts(context.Background()); err != nil || count != 2 {
		t.Fatal("cleanup missed remaining expired proofs", count, err)
	}
	var count int
	if err := db.QueryRow(context.Background(), "SELECT count(*) FROM auth_signup_idempotency").Scan(&count); err != nil || count != 1 {
		t.Fatal("cleanup removed active proof", count, err)
	}
}

func TestLoginNeverReplaysCachedCredentialsAndAuditRemainsBestEffort(t *testing.T) {
	db := hardeningDatabase(t)
	app, _ := signupReplayApp(t, db)
	in, key := signupInput(), uuid.NewString()
	if _, err := db.Exec(context.Background(), `CREATE FUNCTION reject_best_effort_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END $$; CREATE TRIGGER reject_best_effort_audit BEFORE INSERT ON auth_audit_logs FOR EACH ROW EXECUTE FUNCTION reject_best_effort_audit()`); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := signupCall(t, app, "signup", in, key); status != 201 {
		t.Fatal("best effort signup policy changed", status)
	}
	login := service.LoginInput{Email: in.Email, Password: in.Password}
	if status, _, _ := signupCall(t, app, "login", login, key); status != 200 {
		t.Fatal("best effort login policy changed", status)
	}
	login.Password = "Incorrect-password-123!"
	if status, body, _ := signupCall(t, app, "login", login, key); status != 401 || bytes.Contains(body, []byte("access_token")) {
		t.Fatal("login replay bypassed live credentials", status)
	}
}
