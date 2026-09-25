package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kitchen-ticket-service/internal/models"
)

var ErrNotFound = errors.New("kitchen ticket not found")

type TicketRepository struct {
	db *pgxpool.Pool
}

func NewTicketRepository(db *pgxpool.Pool) *TicketRepository {
	return &TicketRepository{db: db}
}

// Create inserts a new kitchen ticket and returns the stored row.
func (r *TicketRepository) Create(ctx context.Context, req models.CreateTicketRequest) (models.KitchenTicket, error) {
	const q = `
		INSERT INTO kitchen_tickets (order_id, pickup_slot, queue_number, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id, order_id, pickup_slot, queue_number, status, created_at, updated_at`

	var t models.KitchenTicket
	err := r.db.QueryRow(ctx, q, req.OrderID, req.PickupSlot, req.QueueNumber, models.StatusPreparing).
		Scan(&t.ID, &t.OrderID, &t.PickupSlot, &t.QueueNumber, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// GetByID returns a single ticket (GET ONE).
func (r *TicketRepository) GetByID(ctx context.Context, id uuid.UUID) (models.KitchenTicket, error) {
	const q = `
		SELECT id, order_id, pickup_slot, queue_number, status, created_at, updated_at
		FROM kitchen_tickets WHERE id = $1`

	var t models.KitchenTicket
	err := r.db.QueryRow(ctx, q, id).
		Scan(&t.ID, &t.OrderID, &t.PickupSlot, &t.QueueNumber, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.KitchenTicket{}, ErrNotFound
	}
	return t, err
}

// GetAll returns every ticket, newest first (GET ALL).
// Supports an optional status filter, e.g. GET /tickets?status=PREPARING
func (r *TicketRepository) GetAll(ctx context.Context, statusFilter string) ([]models.KitchenTicket, error) {
	q := `
		SELECT id, order_id, pickup_slot, queue_number, status, created_at, updated_at
		FROM kitchen_tickets`
	args := []any{}

	if statusFilter != "" {
		q += ` WHERE status = $1`
		args = append(args, statusFilter)
	}
	q += ` ORDER BY pickup_slot ASC, queue_number ASC`

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tickets := []models.KitchenTicket{}
	for rows.Next() {
		var t models.KitchenTicket
		if err := rows.Scan(&t.ID, &t.OrderID, &t.PickupSlot, &t.QueueNumber, &t.Status, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tickets = append(tickets, t)
	}
	return tickets, rows.Err()
}

// UpdateStatus changes a ticket's status (PUT /tickets/:id).
func (r *TicketRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status models.TicketStatus) (models.KitchenTicket, error) {
	const q = `
		UPDATE kitchen_tickets
		SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, order_id, pickup_slot, queue_number, status, created_at, updated_at`

	var t models.KitchenTicket
	err := r.db.QueryRow(ctx, q, id, status).
		Scan(&t.ID, &t.OrderID, &t.PickupSlot, &t.QueueNumber, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.KitchenTicket{}, ErrNotFound
	}
	return t, err
}

// Delete removes a ticket (DELETE /tickets/:id).
func (r *TicketRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM kitchen_tickets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
