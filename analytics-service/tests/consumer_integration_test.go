package tests

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"analytics-service/internal/messaging"
	"analytics-service/internal/models"
	"analytics-service/internal/service"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestConsumerIntegration(t *testing.T) {
	// Skip this test in CI or if explicitly asked, as it requires a running RabbitMQ instance.
	// You can run RabbitMQ locally via docker: docker run -p 5672:5672 rabbitmq
	// Load .env.test from the parent directory for local dev/testing
	_ = godotenv.Load("../.env.test")

	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://guest:guest@localhost:5672/"
	}

	// 1. Setup Mock Repository and Service
	mockRepo := NewMockRepository()
	svc := service.NewAnalyticsService(mockRepo)

	// 2. Setup and Start Consumer
	consumer, err := messaging.NewConsumer(rabbitURL, svc)
	if err != nil {
		t.Skipf("Failed to connect to RabbitMQ (skipping integration test): %v", err)
	}
	defer consumer.Close()

	if err := consumer.Start(); err != nil {
		t.Fatalf("Failed to start consumer: %v", err)
	}

	// 3. Setup Publisher
	pubConn, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatalf("Failed to create publisher connection: %v", err)
	}
	defer pubConn.Close()

	pubCh, err := pubConn.Channel()
	if err != nil {
		t.Fatalf("Failed to create publisher channel: %v", err)
	}
	defer pubCh.Close()

	// 4. Publish OrderPlaced event (CloudEvents-style envelope)
	orderEvent := models.OrderPlacedEvent{
		EventID:   "evt_integ_001",
		EventType: "OrderPlaced",
		Timestamp: "2026-08-20T10:30:00.000Z",
		Source:    "order-service",
		Data: models.OrderPlacedData{
			OrderID:     "ord_integ_001",
			TotalAmount: 25000.50,
			Items: []models.OrderPlacedItem{
				{FlavorID: "F01", FlavorName: "Vanilla", Portions: 200, UnitPrice: 83.335, Subtotal: 16667},
				{FlavorID: "F03", FlavorName: "Strawberry", Portions: 100, UnitPrice: 83.335, Subtotal: 8333.50},
			},
		},
	}
	orderBody, _ := json.Marshal(orderEvent)

	err = pubCh.PublishWithContext(
		context.Background(),
		"order",         // exchange
		"order.success", // routing key
		false,           // mandatory
		false,           // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        orderBody,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish order message: %v", err)
	}

	// 5. Publish WasteRecorded event (CloudEvents-style envelope)
	wasteEvent := models.WasteRecordedEvent{
		EventID:   "evt_integ_002",
		EventType: "WasteRecorded",
		Timestamp: "2026-08-20T14:00:00.000Z",
		Source:    "batch-inventory-service",
		Data: models.WasteRecordedData{
			WasteID:    "wst_integ_001",
			BatchID:    "B999",
			FlavorID:   "F01",
			FlavorName: "Vanilla",
			Portions:   15,
			Reason:     "melted",
		},
	}
	wasteBody, _ := json.Marshal(wasteEvent)

	err = pubCh.PublishWithContext(
		context.Background(),
		"inventory",       // exchange
		"inventory.waste", // routing key
		false,             // mandatory
		false,             // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        wasteBody,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish waste message: %v", err)
	}

	// 6. Wait for consumer to process both messages using channel-based signaling
	// instead of a fixed time.Sleep. This is zero-CPU-waste, race-free, and
	// Go-idiomatic.
	for i := 0; i < 2; i++ {
		select {
		case <-mockRepo.Saved:
			// message processed
		case <-time.After(5 * time.Second):
			t.Fatal("Timed out waiting for consumer to process messages")
		}
	}

	// 7. Verify Results in Mock Repository (using thread-safe Get)
	record := mockRepo.Get("2026-08-20")
	if record == nil {
		t.Fatalf("Expected analytics record to be created for 2026-08-20")
	}

	// Check order processing results
	if record.Financials.GrossSales != 25000.50 {
		t.Errorf("Expected GrossSales 25000.50, got %f", record.Financials.GrossSales)
	}
	if record.Operations.ScoopsSold != 300 {
		t.Errorf("Expected ScoopsSold 300, got %d", record.Operations.ScoopsSold)
	}

	// Check inventory waste processing results
	if record.WasteStats.TotalWastePortions != 15 {
		t.Errorf("Expected TotalWastePortions 15, got %d", record.WasteStats.TotalWastePortions)
	}
	if len(record.WasteStats.WasteByReason) != 1 {
		t.Fatalf("Expected 1 waste reason, got %d", len(record.WasteStats.WasteByReason))
	}
	if record.WasteStats.WasteByReason[0].Portions != 15 {
		t.Errorf("Expected WasteByReason Portions 15, got %d", record.WasteStats.WasteByReason[0].Portions)
	}

	t.Log("Integration test passed successfully!")
}
