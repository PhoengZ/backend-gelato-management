package repository

import (
	"context"
	"errors"

	"order-service/internal/domain"

	"gorm.io/gorm"
)

var (
	ErrNotFound          = gorm.ErrRecordNotFound
	ErrInvalidTransition = errors.New("order is not in a transitionable state")
)

type OrderRepository interface {
	Create(context.Context, *domain.Order) error
	Get(context.Context, string) (*domain.Order, error)
	GetByIdempotencyKey(context.Context, string, string) (*domain.Order, error)
	MarkPaidWithOutbox(context.Context, string, *domain.OutboxEvent) error
	MarkCancelled(context.Context, string, *domain.OutboxEvent) error
	PendingOutbox(context.Context, int) ([]domain.OutboxEvent, error)
	MarkOutboxPublished(context.Context, string) error
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

func (r *gormOrderRepository) MarkPaidWithOutbox(ctx context.Context, id string, event *domain.OutboxEvent) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Order{}).Where("id = ? AND status = ?", id, domain.StatusPendingPayment).Update("status", domain.StatusPaid)
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
		result := tx.Model(&domain.Order{}).Where("id = ? AND status = ?", id, domain.StatusPendingPayment).Update("status", domain.StatusCancelled)
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
