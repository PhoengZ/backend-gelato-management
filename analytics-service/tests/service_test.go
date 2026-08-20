package tests

import (
	"context"
	"testing"
	"time"

	"analytics-service/internal/models"
	"analytics-service/internal/service"
)

// MockRepository implements repository.AnalyticsRepository
type MockRepository struct {
	Records map[string]*models.Analytics
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		Records: make(map[string]*models.Analytics),
	}
}

func (m *MockRepository) FindByDate(ctx context.Context, date string) (*models.Analytics, error) {
	if record, exists := m.Records[date]; exists {
		// return a copy so service modifications don't instantly apply without Save
		cp := *record
		return &cp, nil
	}
	return nil, nil
}

func (m *MockRepository) Save(ctx context.Context, analytics *models.Analytics) error {
	m.Records[analytics.Date] = analytics
	return nil
}

func (m *MockRepository) FindByDateRange(ctx context.Context, startDate, endDate string) ([]models.Analytics, error) {
	var result []models.Analytics
	for date, record := range m.Records {
		if date >= startDate && date <= endDate {
			result = append(result, *record)
		}
	}
	return result, nil
}

func TestProcessOrderSuccess(t *testing.T) {
	mockRepo := NewMockRepository()
	svc := service.NewAnalyticsService(mockRepo)

	msg := models.OrderSuccessMessage{
		Date:        "2026-08-20",
		TotalAmount: 15500.00,
		OrderItems: []models.OrderItem{
			{FlavorID: "F01", Qty: 150},
			{FlavorID: "F02", Qty: 200},
		},
	}

	err := svc.ProcessOrderSuccess(context.Background(), msg)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	record := mockRepo.Records["2026-08-20"]
	if record == nil {
		t.Fatalf("Expected record to be created")
	}

	if record.Financials.GrossSales != 15500.00 {
		t.Errorf("Expected GrossSales 15500.00, got %f", record.Financials.GrossSales)
	}

	if record.Financials.TotalOrders != 1 {
		t.Errorf("Expected TotalOrders 1, got %d", record.Financials.TotalOrders)
	}

	if record.Financials.AverageOrderValue != 15500.00 {
		t.Errorf("Expected AverageOrderValue 15500.00, got %f", record.Financials.AverageOrderValue)
	}

	if record.Operations.ScoopsSold != 350 {
		t.Errorf("Expected ScoopsSold 350, got %d", record.Operations.ScoopsSold)
	}

	if len(record.FlavorStats) != 2 {
		t.Fatalf("Expected 2 flavor stats, got %d", len(record.FlavorStats))
	}
}

func TestProcessInventoryWaste(t *testing.T) {
	mockRepo := NewMockRepository()
	svc := service.NewAnalyticsService(mockRepo)

	// Pre-populate an order so waste_rate can be calculated
	mockRepo.Records["2026-08-20"] = &models.Analytics{
		Date: "2026-08-20",
		Operations: models.Operations{
			ScoopsSold: 100, // Pre-sold
		},
		WasteStats: models.WasteStats{
			WasteByReason: []models.WasteByReason{},
		},
	}

	msg := models.InventoryWasteMessage{
		Date:     "2026-08-20",
		FlavorID: "F01",
		Portions: 10,
		BatchID:  "B123",
		Reason:   "expired",
		CostLost: 250.00,
	}

	err := svc.ProcessInventoryWaste(context.Background(), msg)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	record := mockRepo.Records["2026-08-20"]
	
	if record.WasteStats.TotalWastePortions != 10 {
		t.Errorf("Expected TotalWastePortions 10, got %d", record.WasteStats.TotalWastePortions)
	}

	// 10 portions waste / 100 scoops sold = 0.1
	if record.Operations.WasteRate != 0.1 {
		t.Errorf("Expected WasteRate 0.1, got %f", record.Operations.WasteRate)
	}

	if len(record.WasteStats.WasteByReason) != 1 {
		t.Fatalf("Expected 1 waste reason, got %d", len(record.WasteStats.WasteByReason))
	}

	if record.WasteStats.WasteByReason[0].CostLost != 250.00 {
		t.Errorf("Expected CostLost 250.00, got %f", record.WasteStats.WasteByReason[0].CostLost)
	}
}

func TestGetAnalytics(t *testing.T) {
	mockRepo := NewMockRepository()
	svc := service.NewAnalyticsService(mockRepo)

	today := time.Now().Format("2006-01-02")
	mockRepo.Records[today] = &models.Analytics{
		Date: today,
		Financials: models.Financials{
			GrossSales: 1000,
		},
	}

	res, err := svc.GetAnalytics(context.Background(), "1d")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(res) == 0 {
		t.Fatalf("Expected at least 1 record")
	}

	if res[0].Financials.GrossSales != 1000 {
		t.Errorf("Expected GrossSales 1000, got %f", res[0].Financials.GrossSales)
	}
}
