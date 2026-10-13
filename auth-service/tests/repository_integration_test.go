package tests

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"auth-service/internal/model"
	"auth-service/internal/repository"
	"auth-service/migrations"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresUserRepository(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect for test schema: %v", err)
	}
	schema := "auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		defer adminPool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = adminPool.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
	})

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET search_path TO "+identifier)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("connect to isolated schema: %v", err)
	}
	defer pool.Close()
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatalf("reapply migrations idempotently: %v", err)
	}

	repo := repository.NewPostgresUserRepository(pool)
	user := &model.User{
		ID:           uuid.New(),
		Email:        "repository@example.com",
		DisplayName:  "Repository Test",
		PasswordHash: "$2a$10$test-hash",
		Role:         model.RoleCustomer,
		Status:       model.UserStatusActive,
	}
	if err := repo.Create(ctx, user); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if user.CreatedAt.IsZero() {
		t.Fatal("Create did not return timestamps")
	}

	byEmail, err := repo.FindByEmail(ctx, user.Email)
	if err != nil || byEmail.ID != user.ID {
		t.Fatalf("FindByEmail returned user=%+v err=%v", byEmail, err)
	}
	byID, err := repo.FindByID(ctx, user.ID)
	if err != nil || byID.Email != user.Email {
		t.Fatalf("FindByID returned user=%+v err=%v", byID, err)
	}

	duplicate := *user
	duplicate.ID = uuid.New()
	if err := repo.Create(ctx, &duplicate); !errors.Is(err, repository.ErrEmailAlreadyExists) {
		t.Fatalf("expected duplicate email error, got %v", err)
	}
	if _, err := repo.FindByID(ctx, uuid.New()); !errors.Is(err, repository.ErrUserNotFound) {
		t.Fatalf("expected not found error, got %v", err)
	}

	outboxUser := &model.User{
		ID:           uuid.New(),
		Email:        "outbox-user@example.com",
		DisplayName:  "Outbox Test",
		PasswordHash: "$2a$10$test-hash",
		Role:         model.RoleCustomer,
		Status:       model.UserStatusActive,
	}
	outboxEvent := &model.OutboxEvent{
		ID:        uuid.New(),
		EventType: "user.registered",
		Payload:   []byte(`{"test":true}`),
	}
	if err := repo.CreateWithOutbox(ctx, outboxUser, outboxEvent); err != nil {
		t.Fatalf("CreateWithOutbox returned error: %v", err)
	}
	if outboxEvent.CreatedAt.IsZero() {
		t.Fatal("outboxEvent timestamp not populated")
	}
}
