package tests

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"analytics-service/internal/messaging"
	"analytics-service/internal/models"
	"analytics-service/internal/repository"
	"analytics-service/internal/service"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestConsumerIntegration verifies the Full Pipeline E2E Integration:
// RabbitMQ Broker -> Consumer -> AnalyticsService -> Real MongoDB (analytics & processed_events collections).
func TestConsumerIntegration(t *testing.T) {
	// Load .env.test from parent directory
	_ = godotenv.Load("../.env.test")

	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://guest:guest@localhost:5672/"
	}

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Connect and verify MongoDB
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Skipf("Failed to connect to MongoDB (skipping integration test): %v", err)
	}
	defer mongoClient.Disconnect(ctx)

	if err := mongoClient.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB ping failed (skipping integration test): %v", err)
	}

	db := mongoClient.Database("analytics_test_db")
	_ = db.Collection("analytics").Drop(ctx)
	_ = db.Collection("processed_events").Drop(ctx)
	defer db.Collection("analytics").Drop(ctx)
	defer db.Collection("processed_events").Drop(ctx)

	// 2. Setup Real Repositories and Service
	analyticsRepo := repository.NewAnalyticsRepository(db)
	eventRepo := repository.NewEventRepository(db)
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(analyticsRepo, eventRepo, mockOrderClient)

	// 3. Connect and Start RabbitMQ Consumer
	consumer, err := messaging.NewConsumer(rabbitURL, svc)
	if err != nil {
		t.Skipf("Failed to connect to RabbitMQ (skipping integration test): %v", err)
	}
	defer consumer.Close()

	if err := consumer.Start(); err != nil {
		t.Fatalf("Failed to start consumer: %v", err)
	}

	// 4. Setup Publisher
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

	// 5. Publish OrderPlaced event (CloudEvents 1.0 format)
	orderEvent := models.OrderPlacedEvent{
		ID:     "evt_e2e_001",
		Type:   "OrderPlaced",
		Time:   "2026-08-20T10:30:00.000Z",
		Source: "order-service",
		Data: models.OrderPlacedData{
			OrderID:          "ord_e2e_001",
			TotalAmountMinor: 2500050,
			TotalAmount:      25000.50,
			Currency:         "THB",
			Items: []models.OrderPlacedItem{
				{FlavorID: "F01", FlavorName: "Vanilla", Portions: 200, UnitPriceMinor: 8334, SubtotalMinor: 1666700},
				{FlavorID: "F03", FlavorName: "Strawberry", Portions: 100, UnitPriceMinor: 8334, SubtotalMinor: 833350},
			},
		},
	}
	orderBody, _ := json.Marshal(orderEvent)

	err = pubCh.PublishWithContext(
		ctx,
		"order",
		"OrderPlaced",
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        orderBody,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish order message: %v", err)
	}

	// 6. Publish Duplicate OrderPlaced event with identical event ID to verify real MongoDB deduplication
	err = pubCh.PublishWithContext(
		ctx,
		"order",
		"OrderPlaced",
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        orderBody,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish duplicate order message: %v", err)
	}

	// 7. Publish WasteRecorded event (CloudEvents 1.0 format)
	wasteEvent := models.WasteRecordedEvent{
		ID:     "evt_e2e_002",
		Type:   "WasteRecorded",
		Time:   "2026-08-20T14:00:00.000Z",
		Source: "batch-inventory-service",
		Data: models.WasteRecordedData{
			WasteID:       "wst_e2e_001",
			BatchID:       "B999",
			FlavorID:      "F01",
			FlavorName:    "Vanilla",
			Portions:      15,
			Reason:        "melted",
			CostLostMinor: 125000,
			Currency:      "THB",
		},
	}
	wasteBody, _ := json.Marshal(wasteEvent)

	err = pubCh.PublishWithContext(
		ctx,
		"inventory",
		"WasteRecorded",
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        wasteBody,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish waste message: %v", err)
	}

	// 8. Poll Real MongoDB with timeout until documents are committed (Eventual Consistency)
	var record *models.Analytics
	pollDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(pollDeadline) {
		rec, err := analyticsRepo.FindByDate(ctx, "2026-08-20")
		if err == nil && rec != nil && rec.Financials.GrossSalesMinor > 0 && rec.WasteStats.TotalWastePortions > 0 {
			record = rec
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if record == nil {
		t.Fatalf("Timed out waiting for events to be persisted into real MongoDB analytics collection")
	}

	// 9. Verify Aggregation Results in Real MongoDB analytics Collection
	if record.Financials.GrossSalesMinor != 2500050 {
		t.Errorf("Expected GrossSalesMinor 2500050, got %d", record.Financials.GrossSalesMinor)
	}
	if record.Financials.GrossSales != 25000.50 {
		t.Errorf("Expected GrossSales 25000.50, got %f", record.Financials.GrossSales)
	}
	// Verify that duplicate order event was NOT counted twice
	if record.Financials.TotalOrders != 1 {
		t.Errorf("Expected TotalOrders 1 (deduplicated), got %d", record.Financials.TotalOrders)
	}
	if record.Operations.ScoopsSold != 300 {
		t.Errorf("Expected ScoopsSold 300, got %d", record.Operations.ScoopsSold)
	}
	if record.WasteStats.TotalWastePortions != 15 {
		t.Errorf("Expected TotalWastePortions 15, got %d", record.WasteStats.TotalWastePortions)
	}
	if record.WasteStats.CostLostMinor != 125000 {
		t.Errorf("Expected CostLostMinor 125000, got %d", record.WasteStats.CostLostMinor)
	}
	if len(record.WasteStats.WasteByReason) != 1 || record.WasteStats.WasteByReason[0].Portions != 15 {
		t.Errorf("Expected WasteByReason Portions 15, got %+v", record.WasteStats.WasteByReason)
	}

	// 10. Verify Event Deduplication in Real MongoDB processed_events Collection
	isOrderProcessed, err := eventRepo.IsProcessed(ctx, "evt_e2e_001")
	if err != nil {
		t.Fatalf("Failed to query processed_events for order event: %v", err)
	}
	if !isOrderProcessed {
		t.Errorf("Expected order event evt_e2e_001 to be stored in real MongoDB processed_events")
	}

	isWasteProcessed, err := eventRepo.IsProcessed(ctx, "evt_e2e_002")
	if err != nil {
		t.Fatalf("Failed to query processed_events for waste event: %v", err)
	}
	if !isWasteProcessed {
		t.Errorf("Expected waste event evt_e2e_002 to be stored in real MongoDB processed_events")
	}

	t.Log("Full Pipeline E2E Integration test passed successfully against real RabbitMQ and MongoDB!")
}
