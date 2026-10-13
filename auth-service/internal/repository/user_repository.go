package repository

import (
	"context"
	"errors"
	"fmt"

	"auth-service/internal/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrEmailAlreadyExists = errors.New("email already exists")
)

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByEmail(ctx context.Context, email string) (*model.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*model.User, error)
}

type OutboxUserRepository interface {
	UserRepository
	CreateWithOutbox(ctx context.Context, user *model.User, event *model.OutboxEvent) error
}

type PostgresUserRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresUserRepository(pool *pgxpool.Pool) *PostgresUserRepository {
	return &PostgresUserRepository{pool: pool}
}

func (r *PostgresUserRepository) Create(ctx context.Context, user *model.User) error {
	return r.CreateWithOutbox(ctx, user, nil)
}

func (r *PostgresUserRepository) CreateWithOutbox(ctx context.Context, user *model.User, event *model.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	const query = `
		INSERT INTO users (id, email, display_name, password_hash, role, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`

	err = tx.QueryRow(
		ctx,
		query,
		user.ID,
		user.Email,
		user.DisplayName,
		user.PasswordHash,
		user.Role,
		user.Status,
	).Scan(&user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrEmailAlreadyExists
		}
		return fmt.Errorf("create user: %w", err)
	}

	if event != nil {
		if event.ID == uuid.Nil {
			event.ID = uuid.New()
		}
		const outboxQuery = `
			INSERT INTO outbox_events (id, event_type, payload, created_at)
			VALUES ($1, $2, $3, NOW())
			RETURNING created_at`
		err = tx.QueryRow(ctx, outboxQuery, event.ID, event.EventType, event.Payload).Scan(&event.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert outbox event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *PostgresUserRepository) FindByEmail(ctx context.Context, email string) (*model.User, error) {
	const query = `
		SELECT id, email, display_name, password_hash, role, status, created_at, updated_at
		FROM users
		WHERE email = $1`

	return scanUser(r.pool.QueryRow(ctx, query, email))
}

func (r *PostgresUserRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	const query = `
		SELECT id, email, display_name, password_hash, role, status, created_at, updated_at
		FROM users
		WHERE id = $1`

	return scanUser(r.pool.QueryRow(ctx, query, id))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*model.User, error) {
	var user model.User
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.DisplayName,
		&user.PasswordHash,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &user, nil
}
