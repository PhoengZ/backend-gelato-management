package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"analytics-service/internal/models"
	"analytics-service/internal/repository"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestAnalyticsRepositoryIntegration(t *testing.T) {
	// 1. Setup Environment
	_ = godotenv.Load("../.env.test")
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017" // fallback
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 2. Connect to MongoDB Container
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Skipf("Failed to connect to MongoDB (skipping integration test): %v", err)
	}
	defer client.Disconnect(ctx)

	// Ping to ensure connection is alive
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB ping failed (skipping integration test): %v", err)
	}

	// 3. Setup Test Database and Repository
	db := client.Database("analytics_test_db")
	repo := repository.NewAnalyticsRepository(db)

	// Clean up collection before and after test
	_ = db.Collection("daily_aggregates").Drop(ctx)
	_ = db.Collection("analytics").Drop(ctx)
	defer db.Collection("daily_aggregates").Drop(ctx)
	defer db.Collection("analytics").Drop(ctx)

	// Ensure unique index is established synchronously for test
	if err := repo.EnsureIndexes(ctx); err != nil {
		t.Fatalf("Failed to ensure indexes: %v", err)
	}

	t.Run("Create and Read Record", func(t *testing.T) {
		record := &models.Analytics{
			Date: "2026-08-25",
			Financials: models.Financials{
				GrossSalesMinor: 15000,
			},
			WasteStats: models.WasteStats{
				CostLostMinor: 5000,
			},
		}

		// Test Save (Create)
		err := repo.Save(ctx, record)
		if err != nil {
			t.Fatalf("Failed to save record: %v", err)
		}

		// Test FindByDate (Read)
		found, err := repo.FindByDate(ctx, "2026-08-25")
		if err != nil {
			t.Fatalf("Failed to find record: %v", err)
		}
		if found == nil {
			t.Fatalf("Expected to find record, got nil")
		}
		if found.Financials.GrossSalesMinor != 15000 {
			t.Errorf("Expected GrossSalesMinor 15000, got %d", found.Financials.GrossSalesMinor)
		}
		if found.WasteStats.CostLostMinor != 5000 {
			t.Errorf("Expected CostLostMinor 5000, got %d", found.WasteStats.CostLostMinor)
		}
	})

	t.Run("Update Existing Record", func(t *testing.T) {
		// Fetch existing
		record, _ := repo.FindByDate(ctx, "2026-08-25")
		record.Financials.GrossSalesMinor = 30000

		// Test Save (Update)
		err := repo.Save(ctx, record)
		if err != nil {
			t.Fatalf("Failed to update record: %v", err)
		}

		// Verify Update
		updated, _ := repo.FindByDate(ctx, "2026-08-25")
		if updated.Financials.GrossSalesMinor != 30000 {
			t.Errorf("Expected GrossSalesMinor to be updated to 30000, got %d", updated.Financials.GrossSalesMinor)
		}
	})

	t.Run("Find By Date Range", func(t *testing.T) {
		// Insert another record
		repo.Save(ctx, &models.Analytics{Date: "2026-08-26"})
		repo.Save(ctx, &models.Analytics{Date: "2026-08-28"}) // Outside range

		// Test Range Query
		results, err := repo.FindByDateRange(ctx, "2026-08-25", "2026-08-26")
		if err != nil {
			t.Fatalf("Failed to find by date range: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 records in range, got %d", len(results))
		}
	})

	t.Run("Unique Index on Date in daily_aggregates", func(t *testing.T) {
		coll := db.Collection("daily_aggregates")

		// Direct insertion of duplicate date documents should violate unique index
		dupDate := "2026-12-31"
		_, err := coll.InsertOne(ctx, bson.M{"date": dupDate, "note": "first"})
		if err != nil {
			t.Fatalf("First insert failed: %v", err)
		}

		_, err = coll.InsertOne(ctx, bson.M{"date": dupDate, "note": "second"})
		if err == nil {
			t.Errorf("Expected duplicate key error inserting duplicate date %s, but got nil", dupDate)
		} else if !mongo.IsDuplicateKeyError(err) {
			t.Errorf("Expected mongo.IsDuplicateKeyError, got: %v", err)
		}
	})
}
