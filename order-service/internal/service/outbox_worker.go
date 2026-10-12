package service

import (
	"context"
	"log"
	"time"

	"order-service/internal/publisher"
	"order-service/internal/repository"
)

func RunOutboxWorker(ctx context.Context, repo repository.OrderRepository, pub publisher.Publisher) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := publishPending(ctx, repo, pub); err != nil && ctx.Err() == nil {
			log.Printf("order outbox: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func publishPending(ctx context.Context, repo repository.OrderRepository, pub publisher.Publisher) error {
	events, err := repo.PendingOutbox(ctx, 100)
	if err != nil {
		return err
	}
	for _, event := range events {
		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := pub.Publish(publishCtx, event.RoutingKey, event.ID, event.Payload)
		cancel()
		if err != nil {
			return err
		}
		if err := repo.MarkOutboxPublished(ctx, event.ID); err != nil {
			return err
		}
	}
	return nil
}
