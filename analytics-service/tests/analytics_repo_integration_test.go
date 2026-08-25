package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"analytics-service/internal/models"
	"analytics-service/internal/repository"

	"github.com/joho/godotenv"
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
	_ = db.Collection("analytics").Drop(ctx)
	defer db.Collection("analytics").Drop(ctx)

	t.Run("Create and Read Record", func(t *testing.T) {
		record := &models.Analytics{
			Date: "2026-08-25",
			Financials: models.Financials{
				GrossSales: 150.0,
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
		if found.Financials.GrossSales != 150.0 {
			t.Errorf("Expected GrossSales 150.0, got %f", found.Financials.GrossSales)
		}
	})

	t.Run("Update Existing Record", func(t *testing.T) {
		// Fetch existing
		record, _ := repo.FindByDate(ctx, "2026-08-25")
		record.Financials.GrossSales = 300.0 // Update field

		// Test Save (Update)
		err := repo.Save(ctx, record)
		if err != nil {
			t.Fatalf("Failed to update record: %v", err)
		}

		// Verify Update
		updated, _ := repo.FindByDate(ctx, "2026-08-25")
		if updated.Financials.GrossSales != 300.0 {
			t.Errorf("Expected GrossSales to be updated to 300.0, got %f", updated.Financials.GrossSales)
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
}
