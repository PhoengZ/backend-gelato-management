package service

import (
	"context"
	"log"
	"time"
)

// RunExpiryWorker periodically cancels unpaid orders whose stock hold passed.
// Order rows are its only state, so it resumes naturally after a restart.
func RunExpiryWorker(ctx context.Context, svc Service, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := svc.ExpireOverdueOrders(ctx); err != nil && ctx.Err() == nil {
			log.Printf("order expiry: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// The Order rows are the durable work queue; intents and their persisted
// cancellation reason are enough to resume after a restart.
func RunLifecycleWorker(ctx context.Context, svc Service) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := svc.ResumePendingTransitions(ctx); err != nil && ctx.Err() == nil {
			log.Printf("order lifecycle: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
