package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"analytics-service/internal/repository"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestEventRepositoryIntegration verifies Narrow Integration of EventRepository
// against a real MongoDB container instance.
func TestEventRepositoryIntegration(t *testing.T) {
	_ = godotenv.Load("../.env.test")
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Skipf("Failed to connect to MongoDB (skipping integration test): %v", err)
	}
	defer client.Disconnect(ctx)

	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB ping failed (skipping integration test): %v", err)
	}

	db := client.Database("analytics_test_db")
	coll := db.Collection("processed_events")
	_ = coll.Drop(ctx)
	defer coll.Drop(ctx)

	repo := repository.NewEventRepository(db)

	t.Run("IsProcessed on non-existing event returns false", func(t *testing.T) {
		processed, err := repo.IsProcessed(ctx, "evt_unknown_999")
		if err != nil {
			t.Fatalf("Unexpected error checking IsProcessed: %v", err)
		}
		if processed {
			t.Errorf("Expected event to not be processed, got true")
		}
	})

	t.Run("MarkProcessed persists event and IsProcessed returns true", func(t *testing.T) {
		eventID := "evt_narrow_test_001"
		err := repo.MarkProcessed(ctx, eventID, "OrderPlaced")
		if err != nil {
			t.Fatalf("Failed to mark event processed: %v", err)
		}

		processed, err := repo.IsProcessed(ctx, eventID)
		if err != nil {
			t.Fatalf("Unexpected error checking IsProcessed: %v", err)
		}
		if !processed {
			t.Errorf("Expected event to be marked as processed, got false")
		}
	})

	t.Run("MarkProcessed idempotency via upsert", func(t *testing.T) {
		eventID := "evt_narrow_test_002"
		if err := repo.MarkProcessed(ctx, eventID, "WasteRecorded"); err != nil {
			t.Fatalf("First mark failed: %v", err)
		}
		// Subsequent mark with same event ID should succeed without error
		if err := repo.MarkProcessed(ctx, eventID, "WasteRecorded"); err != nil {
			t.Fatalf("Second mark failed: %v", err)
		}

		count, err := coll.CountDocuments(ctx, bson.M{"_id": eventID})
		if err != nil {
			t.Fatalf("Failed to count documents: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected exactly 1 document for upserted event, got %d", count)
		}
	})
}
