package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"analytics-service/internal/models"
	"analytics-service/internal/repository"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestSaveReversalAppliesOncePerOrderInMongo(t *testing.T) {
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI).SetServerSelectionTimeout(3*time.Second))
	if err != nil {
		t.Skipf("MongoDB unavailable: %v", err)
	}
	defer client.Disconnect(ctx)
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB unavailable: %v", err)
	}
	db := client.Database("analytics_reversal_test_db")
	_ = db.Drop(ctx)
	defer db.Drop(context.Background())
	repo := repository.NewAnalyticsRepository(db)
	if err := repo.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	day := &models.Analytics{Date: "2026-09-02", Financials: models.Financials{GrossSalesMinor: 3000, TotalOrders: 2}, FlavorStats: []models.FlavorStat{}}
	if err := repo.Save(ctx, day); err != nil {
		t.Fatal(err)
	}

	reversed := *day
	reversed.Financials = models.Financials{GrossSalesMinor: 1750, TotalOrders: 1, AverageOrderValueMinor: 1750}
	applied, err := repo.SaveReversal(ctx, &reversed, "order-1")
	if err != nil || !applied {
		t.Fatalf("first reversal: applied=%v err=%v", applied, err)
	}
	// A redelivery computed from a stale read must not write again.
	stale := *day
	stale.Financials = models.Financials{GrossSalesMinor: 1750, TotalOrders: 1}
	if applied, err = repo.SaveReversal(ctx, &stale, "order-1"); err != nil || applied {
		t.Fatalf("second reversal: applied=%v err=%v", applied, err)
	}
	got, err := repo.FindByDate(ctx, "2026-09-02")
	if err != nil || got.Financials.GrossSalesMinor != 1750 || got.Financials.AverageOrderValueMinor != 1750 || len(got.ReversedOrderIDs) != 1 || got.ReversedOrderIDs[0] != "order-1" {
		t.Fatalf("stored %+v %v", got, err)
	}
	// A later order.placed Save keeps the reversal marker.
	got.Financials.TotalOrders++
	if err := repo.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.FindByDate(ctx, "2026-09-02"); len(again.ReversedOrderIDs) != 1 {
		t.Fatal("Save dropped the reversal marker")
	}

	// A reversal for a day without any document creates it once.
	empty := &models.Analytics{Date: "2026-09-05", Financials: models.Financials{GrossSalesMinor: -100, TotalOrders: -1}, FlavorStats: []models.FlavorStat{}}
	if applied, err = repo.SaveReversal(ctx, empty, "order-2"); err != nil || !applied {
		t.Fatalf("new-day reversal: applied=%v err=%v", applied, err)
	}
	if applied, err = repo.SaveReversal(ctx, empty, "order-2"); err != nil || applied {
		t.Fatalf("new-day replay: applied=%v err=%v", applied, err)
	}
}
