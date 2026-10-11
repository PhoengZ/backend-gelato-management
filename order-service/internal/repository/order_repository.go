package repository

import (
	"context"
	"errors"
	"time"

	"order-service/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound          = gorm.ErrRecordNotFound
	ErrInvalidTransition = errors.New("order is not in a transitionable state")
)

type OrderRepository interface {
	Create(context.Context, *domain.Order) error
	Get(context.Context, string) (*domain.Order, error)
	GetByIdempotencyKey(context.Context, string, string) (*domain.Order, error)
	BindCheckout(context.Context, string, string, string) (*domain.Order, error)
	BeginTransition(context.Context, string, domain.OrderStatus) (*domain.Order, error)
	PendingTransitions(context.Context, string, int) ([]domain.Order, error)
	RejectPaymentConfirmation(context.Context, string) error
	MarkPaidWithOutbox(context.Context, string, *domain.OutboxEvent) error
	MarkCancelled(context.Context, string, *domain.OutboxEvent) error
	PendingOutbox(context.Context, int) ([]domain.OutboxEvent, error)
	MarkOutboxPublished(context.Context, string) error
}

// BindCheckout serializes competing responses and never exposes a payment URL
// for an order that was cancelled, paid, or whose stock hold has expired.
func (r *gormOrderRepository) BindCheckout(ctx context.Context, id, sessionID, checkoutURL string) (*domain.Order, error) {
	var order domain.Order
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, "id = ?", id).Error; err != nil {
			return err
		}
		if order.Status != domain.StatusPendingPayment || !order.ReservationExpiresAt.After(time.Now().UTC()) {
			return ErrInvalidTransition
		}
		if order.CheckoutSessionID != "" && (order.CheckoutSessionID != sessionID || order.CheckoutURL != checkoutURL) {
			return ErrInvalidTransition
		}
		if order.CheckoutSessionID == "" {
			if err := tx.Model(&order).Updates(map[string]any{"checkout_session_id": sessionID, "checkout_url": checkoutURL}).Error; err != nil {
				return err
			}
		}
		return tx.Preload("Items").First(&order, "id = ?", id).Error
	})
	return &order, err
}

type gormOrderRepository struct{ db *gorm.DB }

func NewOrderRepository(db *gorm.DB) (OrderRepository, error) {
	if db == nil {
		return nil, errors.New("order database is required")
	}
	return &gormOrderRepository{db: db}, nil
}

func (r *gormOrderRepository) Create(ctx context.Context, order *domain.Order) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(order).Error
	})
}

func (r *gormOrderRepository) Get(ctx context.Context, id string) (*domain.Order, error) {
	var order domain.Order
	err := r.db.WithContext(ctx).Preload("Items").First(&order, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *gormOrderRepository) GetByIdempotencyKey(ctx context.Context, customerID, key string) (*domain.Order, error) {
	var order domain.Order
	err := r.db.WithContext(ctx).Preload("Items").First(&order, "customer_id = ? AND idempotency_key = ?", customerID, key).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

// BeginTransition commits ownership of the workflow before any Inventory RPC.
// The row lock serializes cancellation, payment confirmation and checkout binding.
func (r *gormOrderRepository) BeginTransition(ctx context.Context, id string, target domain.OrderStatus) (*domain.Order, error) {
	terminal := domain.StatusCancelled
	switch target {
	case domain.StatusCancelPending:
	case domain.StatusPaymentConfirming:
		terminal = domain.StatusPaid
	default:
		return nil, ErrInvalidTransition
	}
	var order domain.Order
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Items").First(&order, "id = ?", id).Error; err != nil {
			return err
		}
		if order.Status == target || order.Status == terminal {
			return nil
		}
		if order.Status != domain.StatusPendingPayment {
			return ErrInvalidTransition
		}
		if err := tx.Model(&order).Updates(map[string]any{"status": target, "checkout_url": ""}).Error; err != nil {
			return err
		}
		order.Status, order.CheckoutURL = target, ""
		return nil
	})
	return &order, err
}

func (r *gormOrderRepository) PendingTransitions(ctx context.Context, afterID string, limit int) ([]domain.Order, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var orders []domain.Order
	query := r.db.WithContext(ctx).Preload("Items").Where("status IN ?", []domain.OrderStatus{domain.StatusCancelPending, domain.StatusPaymentConfirming})
	if afterID != "" {
		query = query.Where("id > ?", afterID)
	}
	err := query.Order("id").Limit(limit).Find(&orders).Error
	return orders, err
}

// Only an authoritative Inventory rejection permits changing a confirmation
// intent to cancellation. Ambiguous network errors must retain confirmation.
func (r *gormOrderRepository) RejectPaymentConfirmation(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Order{}).Where("id = ? AND status = ?", id, domain.StatusPaymentConfirming).Update("status", domain.StatusCancelPending)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}
		var current domain.Order
		if err := tx.First(&current, "id = ?", id).Error; err != nil {
			return err
		}
		if current.Status == domain.StatusCancelPending || current.Status == domain.StatusCancelled {
			return nil
		}
		return ErrInvalidTransition
	})
}

func (r *gormOrderRepository) MarkPaidWithOutbox(ctx context.Context, id string, event *domain.OutboxEvent) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Order{}).Where("id = ? AND status = ?", id, domain.StatusPaymentConfirming).Update("status", domain.StatusPaid)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var current domain.Order
			if err := tx.First(&current, "id = ?", id).Error; err != nil {
				return err
			}
			if current.Status == domain.StatusPaid {
				return nil
			}
			return ErrInvalidTransition
		}
		return tx.Create(event).Error
	})
}

func (r *gormOrderRepository) MarkCancelled(ctx context.Context, id string, event *domain.OutboxEvent) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Order{}).Where("id = ? AND status = ?", id, domain.StatusCancelPending).Update("status", domain.StatusCancelled)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var current domain.Order
			if err := tx.First(&current, "id = ?", id).Error; err != nil {
				return err
			}
			if current.Status == domain.StatusCancelled {
				return nil
			}
			return ErrInvalidTransition
		}
		return tx.Create(event).Error
	})
}

func (r *gormOrderRepository) PendingOutbox(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var events []domain.OutboxEvent
	err := r.db.WithContext(ctx).Where("published_at IS NULL").Order("created_at").Limit(limit).Find(&events).Error
	return events, err
}

func (r *gormOrderRepository) MarkOutboxPublished(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&domain.OutboxEvent{}).Where("id = ? AND published_at IS NULL", id).Update("published_at", gorm.Expr("CURRENT_TIMESTAMP")).Error
}
