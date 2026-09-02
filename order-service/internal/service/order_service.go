package service

import (
	"context"
	"fmt"
	"log"
	
	"order-service/internal/client"
	"order-service/internal/domain"
	"order-service/internal/publisher"
	"order-service/internal/repository"
	"order-service/pkg/pb"
)

type OrderService interface {
	PlaceOrder(ctx context.Context, order *domain.Order) error
	UpdateOrderStatus(ctx context.Context, id string, status domain.OrderStatus) error
}

type orderServiceImpl struct {
	repo       repository.OrderRepository
	invClient  client.InventoryClient
	publisher  publisher.RabbitMQPublisher
}

func NewOrderService(repo repository.OrderRepository, invClient client.InventoryClient, pub publisher.RabbitMQPublisher) OrderService {
	return &orderServiceImpl{
		repo:       repo,
		invClient:  invClient,
		publisher:  pub,
	}
}

func (s *orderServiceImpl) PlaceOrder(ctx context.Context, order *domain.Order) error {
	// 1. Prepare gRPC request to reserve portions
	var pbItems []*pb.OrderItem
	for _, item := range order.Items {
		pbItems = append(pbItems, &pb.OrderItem{
			FlavorId: item.FlavorID,
			Portions: int32(item.Portions),
		})
	}
	
	// Create ID first so we can send it in request
	// (usually UUID would be generated on DB insert, but we can generate before or let Inventory just know it's a temp ID)
	
	resReq := &pb.ReserveRequest{
		OrderId: order.ID,
		Items:   pbItems,
	}
	
	// 2. Call Batch Inventory Service via gRPC
	resResp, err := s.invClient.ReservePortions(ctx, resReq)
	if err != nil {
		log.Printf("Failed to reserve portions: %v", err)
		return fmt.Errorf("failed to reserve portions: %v", err)
	}
	
	if !resResp.Success {
		return fmt.Errorf("reservation failed: %s", resResp.Message)
	}

	// 3. Save order to DB as PENDING_PAYMENT
	order.Status = domain.StatusPendingPayment
	err = s.repo.CreateOrder(order)
	if err != nil {
		log.Printf("Failed to save order to DB: %v", err)
		// IDEALLY: we should call ReleaseReservation on gRPC to rollback!
		return fmt.Errorf("failed to save order: %w", err)
	}

	log.Printf("Order %s created successfully with status %s", order.ID, order.Status)
	return nil
}

func (s *orderServiceImpl) UpdateOrderStatus(ctx context.Context, id string, status domain.OrderStatus) error {
	// Get existing order
	order, err := s.repo.GetOrder(id)
	if err != nil {
		return err
	}
	
	// Update status in DB
	err = s.repo.UpdateOrderStatus(id, status)
	if err != nil {
		return err
	}
	
	// Publish Event if PAID
	if status == domain.StatusPaid {
		event := domain.OrderPlacedEvent{
			OrderID:  order.ID,
			TimeSlot: order.TimeSlot,
			Status:   status,
			Items:    order.Items,
		}
		err = s.publisher.PublishOrderPlaced(ctx, event)
		if err != nil {
			log.Printf("Failed to publish OrderPlacedEvent: %v", err)
			// Depending on requirements, we might implement Transactional Outbox here
		}
	} else if status == domain.StatusCancelled {
		event := domain.OrderCancelledEvent{
			OrderID: order.ID,
		}
		err = s.publisher.PublishOrderCancelled(ctx, event)
		if err != nil {
			log.Printf("Failed to publish OrderCancelledEvent: %v", err)
		}
	}

	log.Printf("Order %s updated to status %s", id, status)
	return nil
}
