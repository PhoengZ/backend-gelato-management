package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"fulfillment-service/internal/events"
	"fulfillment-service/internal/models"
)

var (
	ErrNotFound          = errors.New("fulfillment not found")
	ErrInvalidTransition = errors.New("invalid status transition")
)

// SlotLength is the width of a pickup slot.
const SlotLength = 15 * time.Minute

// slotNamespace makes slot IDs deterministic: the same slot start always maps
// to the same UUID, so no coordination is needed to create slots on demand.
var slotNamespace = uuid.MustParse("6f1d3a52-7c0e-4b3e-9a55-2d1c8e4f7b10")

const uniqueViolation = "23505"

const selectCols = `id, order_id, queue_number, pickup_slot_id, pickup_at, status, items, picked_up_at, created_at, updated_at`

type contextKey string

const actorContextKey contextKey = "fulfillment_actor_id"

// WithActorID stores an actor UUID in the context for status change auditing.
func WithActorID(ctx context.Context, actorID uuid.UUID) context.Context {
	return context.WithValue(ctx, actorContextKey, actorID)
}

// ActorIDFromContext retrieves the actor UUID from context if present.
func ActorIDFromContext(ctx context.Context) *uuid.UUID {
	if v, ok := ctx.Value(actorContextKey).(uuid.UUID); ok && v != uuid.Nil {
		return &v
	}
	return nil
}

type FulfillmentRepository struct {
	db *pgxpool.Pool
}

func NewFulfillmentRepository(db *pgxpool.Pool) *FulfillmentRepository {
	return &FulfillmentRepository{db: db}
}

func scanFulfillment(row pgx.Row) (models.Fulfillment, error) {
	var f models.Fulfillment
	var items []byte
	err := row.Scan(&f.ID, &f.OrderID, &f.QueueNumber, &f.PickupSlotID, &f.PickupAt,
		&f.Status, &items, &f.PickedUpAt, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Fulfillment{}, ErrNotFound
	}
	if err != nil {
		return models.Fulfillment{}, err
	}
	if err := json.Unmarshal(items, &f.Items); err != nil {
		return models.Fulfillment{}, err
	}
	if f.Items == nil {
		f.Items = []models.Item{}
	}
	return f, nil
}

// GetByOrderID returns the ticket for an order.
func (r *FulfillmentRepository) GetByOrderID(ctx context.Context, orderID uuid.UUID) (models.Fulfillment, error) {
	return scanFulfillment(r.db.QueryRow(ctx,
		`SELECT `+selectCols+` FROM fulfillments WHERE order_id = $1`, orderID))
}

// List returns tickets ordered by pickup slot then queue number. A nil or empty
// statuses slice returns every status.
func (r *FulfillmentRepository) List(ctx context.Context, statuses []models.Status) ([]models.Fulfillment, error) {
	var filter []string
	for _, s := range statuses {
		filter = append(filter, string(s))
	}

	rows, err := r.db.Query(ctx, `
		SELECT `+selectCols+` FROM fulfillments
		WHERE ($1::text[] IS NULL OR status = ANY($1))
		ORDER BY (SELECT starts_at FROM pickup_slots WHERE id = pickup_slot_id), queue_seq`, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.Fulfillment{}
	for rows.Next() {
		f, err := scanFulfillment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// markProcessed records the event id and reports whether it was already seen.
func markProcessed(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) (duplicate bool, err error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`, eventID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 0, nil
}

// CreateFromOrderPlaced creates the ticket for a paid order. It is idempotent:
// a repeated event id, or a second event for the same order, is a no-op.
func (r *FulfillmentRepository) CreateFromOrderPlaced(ctx context.Context, eventID uuid.UUID, d events.OrderPlacedData) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if dup, err := markProcessed(ctx, tx, eventID); err != nil || dup {
		return err
	}

	startsAt := d.PickupAt.UTC().Truncate(SlotLength)
	slotID := uuid.NewSHA1(slotNamespace, []byte(startsAt.Format(time.RFC3339)))

	// Atomically hand out the next queue number of the slot. The first insert
	// stores 2 and returns 1; later calls increment under the row lock.
	var seq int
	err = tx.QueryRow(ctx, `
		INSERT INTO pickup_slots (id, starts_at, next_queue) VALUES ($1, $2, 2)
		ON CONFLICT (starts_at) DO UPDATE SET next_queue = pickup_slots.next_queue + 1
		RETURNING next_queue - 1`, slotID, startsAt).Scan(&seq)
	if err != nil {
		return err
	}

	items := make([]models.Item, 0, len(d.Items))
	for _, it := range d.Items {
		items = append(items, models.Item{FlavorID: it.FlavorID, FlavorName: it.FlavorName, Portions: it.Portions})
	}
	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return err
	}

	var fulfillmentID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO fulfillments (order_id, pickup_slot_id, queue_seq, queue_number, pickup_at, status, items)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		d.OrderID, slotID, seq, fmt.Sprintf("%02d", seq), d.PickupAt.UTC(), models.StatusPreparing, itemsJSON).Scan(&fulfillmentID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			// Ticket for this order already exists; roll back so the queue
			// number taken above is not wasted.
			return nil
		}
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO fulfillment_status_history (id, fulfillment_id, old_status, new_status, actor_id, created_at)
		VALUES (gen_random_uuid(), $1, NULL, $2, NULL, now())`,
		fulfillmentID, models.StatusPreparing)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// CancelByOrder cancels a ticket that is still PREPARING. Tickets that are
// already ready or picked up, or unknown orders, are left unchanged.
func (r *FulfillmentRepository) CancelByOrder(ctx context.Context, eventID, orderID uuid.UUID) (cancelled bool, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	if dup, err := markProcessed(ctx, tx, eventID); err != nil || dup {
		return false, err
	}

	var fID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE fulfillments SET status = $2, updated_at = now()
		WHERE order_id = $1 AND status = $3
		RETURNING id`,
		orderID, models.StatusCancelled, models.StatusPreparing).Scan(&fID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, tx.Commit(ctx)
		}
		return false, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO fulfillment_status_history (id, fulfillment_id, old_status, new_status, actor_id, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, NULL, now())`,
		fID, models.StatusPreparing, models.StatusCancelled)
	if err != nil {
		return false, err
	}

	return true, tx.Commit(ctx)
}

// UpdateStatus moves a ticket to READY_FOR_PICKUP or PICKED_UP and writes the
// matching event to the outbox in the same transaction. Repeating the current
// status returns the ticket without publishing again.
func (r *FulfillmentRepository) UpdateStatus(ctx context.Context, orderID uuid.UUID, to models.Status) (models.Fulfillment, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return models.Fulfillment{}, err
	}
	defer tx.Rollback(ctx)

	cur, err := scanFulfillment(tx.QueryRow(ctx,
		`SELECT `+selectCols+` FROM fulfillments WHERE order_id = $1 FOR UPDATE`, orderID))
	if err != nil {
		return models.Fulfillment{}, err
	}
	if cur.Status == to {
		return cur, nil
	}
	if !models.CanTransition(cur.Status, to) {
		return models.Fulfillment{}, ErrInvalidTransition
	}

	now := time.Now().UTC()
	var pickedUpAt *time.Time
	if to == models.StatusPickedUp {
		pickedUpAt = &now
	}
	updated, err := scanFulfillment(tx.QueryRow(ctx, `
		UPDATE fulfillments
		SET status = $2, picked_up_at = COALESCE($3, picked_up_at), updated_at = $4
		WHERE order_id = $1
		RETURNING `+selectCols, orderID, to, pickedUpAt, now))
	if err != nil {
		return models.Fulfillment{}, err
	}

	actorID := ActorIDFromContext(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO fulfillment_status_history (id, fulfillment_id, old_status, new_status, actor_id, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)`,
		updated.ID, cur.Status, to, actorID, now); err != nil {
		return models.Fulfillment{}, err
	}

	var env events.Envelope
	var routingKey string
	switch to {
	case models.StatusReady:
		routingKey = events.RoutingKeyOrderReady
		env, err = events.NewOrderReady(now, events.OrderReadyData{
			OrderID: updated.OrderID, QueueNumber: updated.QueueNumber, PickupSlotID: updated.PickupSlotID,
		})
	case models.StatusPickedUp:
		routingKey = events.RoutingKeyOrderPickedUp
		env, err = events.NewOrderPickedUp(now, events.OrderPickedUpData{
			OrderID: updated.OrderID, PickedUpAt: now,
		})
	}
	if err != nil {
		return models.Fulfillment{}, err
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return models.Fulfillment{}, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (id, routing_key, payload) VALUES ($1, $2, $3)`,
		env.ID, routingKey, payload); err != nil {
		return models.Fulfillment{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return models.Fulfillment{}, err
	}
	return updated, nil
}

// GetSlotCapacity returns the active ticket count (PREPARING or READY_FOR_PICKUP)
// for the 15-minute slot window corresponding to slotTime.
func (r *FulfillmentRepository) GetSlotCapacity(ctx context.Context, slotTime time.Time) (int, error) {
	startsAt := slotTime.UTC().Truncate(SlotLength)
	slotID := uuid.NewSHA1(slotNamespace, []byte(startsAt.Format(time.RFC3339)))

	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM fulfillments
		WHERE pickup_slot_id = $1 AND status IN ($2, $3)`,
		slotID, models.StatusPreparing, models.StatusReady).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetHistory returns the immutable audit history entries for a fulfillment ticket.
func (r *FulfillmentRepository) GetHistory(ctx context.Context, fulfillmentID uuid.UUID) ([]models.FulfillmentStatusHistory, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, fulfillment_id, old_status, new_status, actor_id, created_at
		FROM fulfillment_status_history
		WHERE fulfillment_id = $1
		ORDER BY created_at ASC, id ASC`, fulfillmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []models.FulfillmentStatusHistory
	for rows.Next() {
		var h models.FulfillmentStatusHistory
		var oldStatus *string
		if err := rows.Scan(&h.ID, &h.FulfillmentID, &oldStatus, &h.NewStatus, &h.ActorID, &h.CreatedAt); err != nil {
			return nil, err
		}
		if oldStatus != nil {
			s := models.Status(*oldStatus)
			h.OldStatus = &s
		}
		history = append(history, h)
	}
	return history, rows.Err()
}
