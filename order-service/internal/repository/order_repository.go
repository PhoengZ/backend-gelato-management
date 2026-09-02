package repository

import (
	"order-service/internal/domain"

	"gorm.io/gorm"
)

type OrderRepository interface {
	CreateOrder(order *domain.Order) error
	GetOrder(id string) (*domain.Order, error)
	UpdateOrderStatus(id string, status domain.OrderStatus) error
}

type orderRepositoryImpl struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) OrderRepository {
	return &orderRepositoryImpl{db: db}
}

func (r *orderRepositoryImpl) CreateOrder(order *domain.Order) error {
	return r.db.Create(order).Error
}

func (r *orderRepositoryImpl) GetOrder(id string) (*domain.Order, error) {
	var order domain.Order
	err := r.db.Preload("Items").Where("id = ?", id).First(&order).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *orderRepositoryImpl) UpdateOrderStatus(id string, status domain.OrderStatus) error {
	return r.db.Model(&domain.Order{}).Where("id = ?", id).Update("status", status).Error
}
