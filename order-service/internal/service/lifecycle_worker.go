package service

import (
	"context"
	"log"
	"time"
)

// The Order rows are the durable work queue; no extra columns or tables are needed.
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
