package tests

import (
	"batch-inventory-service/internal/inventory"
	"batch-inventory-service/internal/outbox"
	"batch-inventory-service/internal/service"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

type outboxEventRecord struct {
	ID      string
	Payload map[string]any
}

func queryOutboxEvents(t *testing.T, s *inventory.Service, eventType string) []outboxEventRecord {
	t.Helper()
	rows, err := s.DB.Query(context.Background(), "SELECT id::text, payload FROM outbox_events ORDER BY created_at, id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var results []outboxEventRecord
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if eventType == "" || payload["type"] == eventType {
			results = append(results, outboxEventRecord{ID: id, Payload: payload})
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return results
}

func TestNFRHardening_LowStockTriggerAndStormingPrevention(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()

	flavorID := uuid.NewString()
	// Create a batch with 15 portions (above 10 threshold)
	b := batch(t, s, flavorID, 15, time.Now().UTC().Add(72*time.Hour))

	// Initial check: outbox should not have any low-stock events
	events := queryOutboxEvents(t, s, service.TypeLowStock)
	if len(events) != 0 {
		t.Fatalf("expected 0 low stock events initially, got %d", len(events))
	}

	// 1. Reserve 10 portions -> remaining available is 5 (<= 10)
	reserve(t, s, inventory.Item{FlavorID: flavorID, Portions: 10})

	events = queryOutboxEvents(t, s, service.TypeLowStock)
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 low stock event after drop below threshold, got %d", len(events))
	}

	evt := events[0].Payload
	data, ok := evt["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid data field in event: %+v", evt)
	}
	if data["flavor_id"] != flavorID {
		t.Errorf("expected flavor_id %s, got %v", flavorID, data["flavor_id"])
	}
	if data["batch_id"] != b.ID {
		t.Errorf("expected batch_id %s, got %v", b.ID, data["batch_id"])
	}
	if int64(data["remaining_portions"].(float64)) != 5 {
		t.Errorf("expected remaining_portions 5, got %v", data["remaining_portions"])
	}
	if int64(data["threshold_portions"].(float64)) != 10 {
		t.Errorf("expected threshold_portions 10, got %v", data["threshold_portions"])
	}

	// 2. Alert Storming Prevention: Reserve another 2 portions -> remaining available is 3 (still <= 10)
	reserve(t, s, inventory.Item{FlavorID: flavorID, Portions: 2})

	events = queryOutboxEvents(t, s, service.TypeLowStock)
	if len(events) != 1 {
		t.Fatalf("expected still exactly 1 low stock event due to storming prevention, got %d", len(events))
	}

	// 3. Replenishment: Create a new batch with 20 portions -> available becomes 3 + 20 = 23 (> 10)
	batch(t, s, flavorID, 20, time.Now().UTC().Add(72*time.Hour))

	// Verify low_stock_notified_at was reset
	var notifiedCount int64
	err := s.DB.QueryRow(ctx, "SELECT count(*) FROM batches WHERE flavor_id = $1 AND low_stock_notified_at IS NOT NULL", flavorID).Scan(&notifiedCount)
	if err != nil {
		t.Fatal(err)
	}
	if notifiedCount != 0 {
		t.Fatalf("expected low_stock_notified_at to be reset to NULL after replenishment, but %d batches remained set", notifiedCount)
	}

	// 4. Re-alerting after replenishment: Reserve 15 portions -> available drops to 8 (<= 10)
	reserve(t, s, inventory.Item{FlavorID: flavorID, Portions: 15})

	events = queryOutboxEvents(t, s, service.TypeLowStock)
	if len(events) != 2 {
		t.Fatalf("expected 2 low stock events after replenishment and subsequent drop, got %d", len(events))
	}
}

func TestNFRHardening_WasteTriggersLowStockEvent(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()

	flavorID := uuid.NewString()
	// Create a batch with 12 portions
	b := batch(t, s, flavorID, 12, time.Now().UTC().Add(72*time.Hour))

	// Waste 4 portions -> remaining available is 8 (<= 10)
	actor := uuid.NewString()
	_, err := s.Waste(ctx, b.ID, actor, inventory.WasteInput{
		Portions: 4,
		Reason:   "DAMAGED",
		Note:     "test waste",
		Key:      uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	events := queryOutboxEvents(t, s, service.TypeLowStock)
	if len(events) != 1 {
		t.Fatalf("expected 1 low stock event after waste caused stock to drop <= 10, got %d", len(events))
	}

	data := events[0].Payload["data"].(map[string]any)
	if int64(data["remaining_portions"].(float64)) != 8 {
		t.Errorf("expected remaining_portions 8, got %v", data["remaining_portions"])
	}
}

func TestNFRHardening_ExpiringBatchWorkerSweep(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()

	flavorID := uuid.NewString()
	// Batch 1: Expiring in 24 hours (within 48h window)
	b1 := batch(t, s, flavorID, 10, time.Now().UTC().Add(24*time.Hour))

	// Batch 2: Expiring in 72 hours (outside 48h window)
	_ = batch(t, s, flavorID, 10, time.Now().UTC().Add(72*time.Hour))

	// Run background sweep
	if err := s.Sweep(ctx, 100); err != nil {
		t.Fatal(err)
	}

	events := queryOutboxEvents(t, s, service.TypeExpiring)
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 expiring event, got %d", len(events))
	}

	evt := events[0].Payload
	data := evt["data"].(map[string]any)
	if data["batch_id"] != b1.ID {
		t.Errorf("expected expiring batch_id %s, got %v", b1.ID, data["batch_id"])
	}
	if data["flavor_id"] != flavorID {
		t.Errorf("expected flavor_id %s, got %v", flavorID, data["flavor_id"])
	}

	// Run sweep again -> should not produce a duplicate expiring event for b1
	if err := s.Sweep(ctx, 100); err != nil {
		t.Fatal(err)
	}

	events = queryOutboxEvents(t, s, service.TypeExpiring)
	if len(events) != 1 {
		t.Fatalf("expected still exactly 1 expiring event after subsequent sweep, got %d", len(events))
	}
}

func TestNFRHardening_DynamicPublisherRouting(t *testing.T) {
	tests := []struct {
		eventType string
		expected  string
	}{
		{service.TypeWasteRecorded, outbox.RoutingKeyWaste},
		{service.TypeLowStock, outbox.RoutingKeyLowStock},
		{service.TypeExpiring, outbox.RoutingKeyExpiring},
	}

	for _, tt := range tests {
		payload, err := json.Marshal(map[string]string{"type": tt.eventType})
		if err != nil {
			t.Fatal(err)
		}
		routingKey := outbox.ResolveRoutingKey(payload)
		if routingKey != tt.expected {
			t.Errorf("for event type %s, expected routing key %s, got %s", tt.eventType, tt.expected, routingKey)
		}
	}
}
