package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"analytics-service/internal/models"
	"analytics-service/internal/service"
)

// MockRepository implements repository.AnalyticsRepository with
// thread-safe access via sync.RWMutex and a Saved channel for
// signaling test waiters when Save completes.
type MockRepository struct {
	mu      sync.RWMutex
	Records map[string]*models.Analytics
	Saved   chan string // signals the date key on every Save
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		Records: make(map[string]*models.Analytics),
		Saved:   make(chan string, 100), // buffered to avoid blocking in unit tests
	}
}

func (m *MockRepository) FindByDate(ctx context.Context, date string) (*models.Analytics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if record, exists := m.Records[date]; exists {
		// return a copy so service modifications don't instantly apply without Save
		cp := *record
		return &cp, nil
	}
	return nil, nil
}

func (m *MockRepository) Save(ctx context.Context, analytics *models.Analytics) error {
	m.mu.Lock()
	m.Records[analytics.Date] = analytics
	m.mu.Unlock()
	// Non-blocking send to signal save completion
	select {
	case m.Saved <- analytics.Date:
	default:
	}
	return nil
}

func (m *MockRepository) FindByDateRange(ctx context.Context, startDate, endDate string) ([]models.Analytics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []models.Analytics
	for date, record := range m.Records {
		if date >= startDate && date <= endDate {
			result = append(result, *record)
		}
	}
	return result, nil
}

// Get returns a thread-safe copy of the record for the given date.
func (m *MockRepository) Get(date string) *models.Analytics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if record, exists := m.Records[date]; exists {
		cp := *record
		return &cp
	}
	return nil
}

func TestProcessOrderPlaced(t *testing.T) {
	mockRepo := NewMockRepository()
	svc := service.NewAnalyticsService(mockRepo)

	event := models.OrderPlacedEvent{
		EventID:   "evt_test_001",
		EventType: "OrderPlaced",
		Timestamp: "2026-08-20T10:30:00.000Z",
		Source:    "order-service",
		Data: models.OrderPlacedData{
			OrderID:     "ord_12345",
			TotalAmount: 15500.00,
			Items: []models.OrderPlacedItem{
				{FlavorID: "F01", FlavorName: "Vanilla", Portions: 150, UnitPrice: 50, Subtotal: 7500},
				{FlavorID: "F02", FlavorName: "Chocolate", Portions: 200, UnitPrice: 40, Subtotal: 8000},
			},
		},
	}

	err := svc.ProcessOrderPlaced(context.Background(), event)
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

	// Verify per-flavor revenue tracking
	for _, fs := range record.FlavorStats {
		switch fs.FlavorID {
		case "F01":
			if fs.Revenue != 7500 {
				t.Errorf("Expected F01 Revenue 7500, got %f", fs.Revenue)
			}
			if fs.Name != "Vanilla" {
				t.Errorf("Expected F01 Name 'Vanilla', got '%s'", fs.Name)
			}
		case "F02":
			if fs.Revenue != 8000 {
				t.Errorf("Expected F02 Revenue 8000, got %f", fs.Revenue)
			}
			if fs.Name != "Chocolate" {
				t.Errorf("Expected F02 Name 'Chocolate', got '%s'", fs.Name)
			}
		}
	}
}

func TestProcessWasteRecorded(t *testing.T) {
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

	event := models.WasteRecordedEvent{
		EventID:   "evt_test_002",
		EventType: "WasteRecorded",
		Timestamp: "2026-08-20T14:00:00.000Z",
		Source:    "batch-inventory-service",
		Data: models.WasteRecordedData{
			WasteID:    "wst_001",
			BatchID:    "B123",
			FlavorID:   "F01",
			FlavorName: "Vanilla",
			Portions:   10,
			Reason:     "expired",
		},
	}

	err := svc.ProcessWasteRecorded(context.Background(), event)
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

	if record.WasteStats.WasteByReason[0].Portions != 10 {
		t.Errorf("Expected WasteByReason Portions 10, got %d", record.WasteStats.WasteByReason[0].Portions)
	}

	// Verify flavor name is stored
	if len(record.FlavorStats) != 1 {
		t.Fatalf("Expected 1 flavor stat, got %d", len(record.FlavorStats))
	}
	if record.FlavorStats[0].Name != "Vanilla" {
		t.Errorf("Expected FlavorStat Name 'Vanilla', got '%s'", record.FlavorStats[0].Name)
	}
}

func TestGetAnalyticsSummary(t *testing.T) {
	mockRepo := NewMockRepository()
	svc := service.NewAnalyticsService(mockRepo)

	today := time.Now().Format("2006-01-02")
	mockRepo.Records[today] = &models.Analytics{
		Date: today,
		Financials: models.Financials{
			GrossSales:  1000,
			TotalOrders: 10,
		},
		Operations: models.Operations{
			ScoopsSold: 50,
		},
		WasteStats: models.WasteStats{
			TotalWastePortions: 5,
			WasteByReason:      []models.WasteByReason{},
		},
		FlavorStats: []models.FlavorStat{
			{FlavorID: "F01", Name: "Vanilla", ScoopsSold: 30, Revenue: 600, WastePortions: 3},
			{FlavorID: "F02", Name: "Chocolate", ScoopsSold: 20, Revenue: 400, WastePortions: 2},
		},
	}

	summary, err := svc.GetAnalyticsSummary(context.Background(), "1d")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if summary.TotalRevenue != 1000 {
		t.Errorf("Expected TotalRevenue 1000, got %f", summary.TotalRevenue)
	}

	if summary.TotalOrders != 10 {
		t.Errorf("Expected TotalOrders 10, got %d", summary.TotalOrders)
	}

	if summary.TotalScoops != 50 {
		t.Errorf("Expected TotalScoops 50, got %d", summary.TotalScoops)
	}

	if summary.TotalWaste != 5 {
		t.Errorf("Expected TotalWaste 5, got %d", summary.TotalWaste)
	}

	if len(summary.SalesByFlavor) != 2 {
		t.Errorf("Expected 2 salesByFlavor entries, got %d", len(summary.SalesByFlavor))
	}

	if len(summary.WasteByFlavor) != 2 {
		t.Errorf("Expected 2 wasteByFlavor entries, got %d", len(summary.WasteByFlavor))
	}

	if len(summary.SalesTrend) != 1 {
		t.Fatalf("Expected 1 salesTrend entry, got %d", len(summary.SalesTrend))
	}

	if summary.SalesTrend[0].Date != today {
		t.Errorf("Expected salesTrend date %s, got %s", today, summary.SalesTrend[0].Date)
	}

	if summary.SalesTrend[0].Revenue != 1000 {
		t.Errorf("Expected salesTrend revenue 1000, got %f", summary.SalesTrend[0].Revenue)
	}
}

func TestGetAnalyticsSummary_InvalidPeriod(t *testing.T) {
	mockRepo := NewMockRepository()
	svc := service.NewAnalyticsService(mockRepo)

	_, err := svc.GetAnalyticsSummary(context.Background(), "invalid")
	if err == nil {
		t.Fatal("Expected error for invalid period, got nil")
	}
}
