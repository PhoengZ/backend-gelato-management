package tests

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"analytics-service/internal/models"
	"analytics-service/internal/service"
)

// MockRepository implements repository.AnalyticsRepository
type MockRepository struct {
	mu      sync.RWMutex
	Records map[string]*models.Analytics
	Saved   chan string
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		Records: make(map[string]*models.Analytics),
		Saved:   make(chan string, 100),
	}
}

func (m *MockRepository) FindByDate(ctx context.Context, date string) (*models.Analytics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if record, exists := m.Records[date]; exists {
		cp := *record
		return &cp, nil
	}
	return nil, nil
}

func (m *MockRepository) Save(ctx context.Context, analytics *models.Analytics) error {
	m.mu.Lock()
	m.Records[analytics.Date] = analytics
	m.mu.Unlock()
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

// MockEventRepository implements repository.EventRepository for event deduplication
type MockEventRepository struct {
	mu        sync.RWMutex
	Processed map[string]string
}

func NewMockEventRepository() *MockEventRepository {
	return &MockEventRepository{
		Processed: make(map[string]string),
	}
}

func (m *MockEventRepository) IsProcessed(ctx context.Context, eventID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.Processed[eventID]
	return exists, nil
}

func (m *MockEventRepository) MarkProcessed(ctx context.Context, eventID, eventType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Processed[eventID] = eventType
	return nil
}

// MockOrderClient implements client.OrderClient for gRPC queries
type MockOrderClient struct {
	mu          sync.RWMutex
	Orders      map[string]*models.OrderDetails
	StreamedIDs []string
	StreamErr   error
	GetOrderErr error
}

func NewMockOrderClient() *MockOrderClient {
	return &MockOrderClient{
		Orders: make(map[string]*models.OrderDetails),
	}
}

func (m *MockOrderClient) StreamOrderItems(ctx context.Context, orderIDs []string) ([]models.OrderItemDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StreamedIDs = append(m.StreamedIDs, orderIDs...)
	if m.StreamErr != nil {
		return nil, m.StreamErr
	}
	var result []models.OrderItemDetail
	for _, id := range orderIDs {
		if ord, ok := m.Orders[id]; ok {
			result = append(result, ord.Items...)
		}
	}
	return result, nil
}

func (m *MockOrderClient) GetOrderDetails(ctx context.Context, orderID string) (*models.OrderDetails, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.GetOrderErr != nil {
		return nil, m.GetOrderErr
	}
	if ord, ok := m.Orders[orderID]; ok {
		cp := *ord
		return &cp, nil
	}
	return nil, nil
}

func (m *MockOrderClient) Close() error {
	return nil
}

func TestProcessOrderPlaced(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	event := models.OrderPlacedEvent{
		ID:     "evt_test_001",
		Type:   "OrderPlaced",
		Time:   "2026-08-20T10:30:00.000Z",
		Source: "order-service",
		Data: models.OrderPlacedData{
			OrderID:          "ord_12345",
			TotalAmountMinor: 1550000,
			TotalAmount:      15500.00,
			Currency:         "THB",
			Items: []models.OrderPlacedItem{
				{FlavorID: "F01", FlavorName: "Vanilla", Portions: 150, UnitPriceMinor: 5000, SubtotalMinor: 750000, Subtotal: 7500},
				{FlavorID: "F02", FlavorName: "Chocolate", Portions: 200, UnitPriceMinor: 4000, SubtotalMinor: 800000, Subtotal: 8000},
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

	if record.Financials.GrossSalesMinor != 1550000 {
		t.Errorf("Expected GrossSalesMinor 1550000, got %d", record.Financials.GrossSalesMinor)
	}
	if record.Financials.GrossSales != 15500.00 {
		t.Errorf("Expected GrossSales 15500.00, got %f", record.Financials.GrossSales)
	}
	if record.Financials.TotalOrders != 1 {
		t.Errorf("Expected TotalOrders 1, got %d", record.Financials.TotalOrders)
	}
	if record.Operations.ScoopsSold != 350 {
		t.Errorf("Expected ScoopsSold 350, got %d", record.Operations.ScoopsSold)
	}
	if len(record.FlavorStats) != 2 {
		t.Fatalf("Expected 2 flavor stats, got %d", len(record.FlavorStats))
	}

	// Verify event was marked as processed in EventRepository (no order table needed)
	processed, _ := mockEventRepo.IsProcessed(context.Background(), "evt_test_001")
	if !processed {
		t.Errorf("Expected event evt_test_001 to be marked as processed")
	}
}

func TestProcessOrderCancelled_ViaGRPC(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	// Pre-populate daily analytics record
	mockRepo.Records["2026-08-20"] = &models.Analytics{
		Date: "2026-08-20",
		Financials: models.Financials{
			GrossSalesMinor: 200000,
			GrossSales:      2000.00,
			TotalOrders:     2,
		},
		Operations: models.Operations{
			ScoopsSold: 20,
		},
		FlavorStats: []models.FlavorStat{
			{FlavorID: "F01", Name: "Vanilla", ScoopsSold: 10, RevenueMinor: 100000, Revenue: 1000.00},
			{FlavorID: "F02", Name: "Chocolate", ScoopsSold: 10, RevenueMinor: 100000, Revenue: 1000.00},
		},
	}

	// Mock order response from Order Service gRPC
	mockOrderClient.Orders["ord_cancel_001"] = &models.OrderDetails{
		OrderID:          "ord_cancel_001",
		CreatedAt:        "2026-08-20T11:00:00Z",
		TotalAmountMinor: 100000, // 1000.00 THB
		Currency:         "THB",
		Items: []models.OrderItemDetail{
			{FlavorID: "F01", FlavorName: "Vanilla", Portions: 5, SubtotalMinor: 50000},
			{FlavorID: "F02", FlavorName: "Chocolate", Portions: 5, SubtotalMinor: 50000},
		},
	}

	// Thin cancellation event containing only order_id
	cancelEvent := models.OrderCancelledEvent{
		ID:     "evt_cancel_001",
		Type:   "OrderCancelled",
		Time:   "2026-08-20T12:00:00Z",
		Source: "order-service",
		Data: models.OrderCancelledData{
			OrderID: "ord_cancel_001",
			Reason:  "CUSTOMER_REQUEST",
		},
	}

	err := svc.ProcessOrderCancelled(context.Background(), cancelEvent)
	if err != nil {
		t.Fatalf("Expected no error processing cancellation, got: %v", err)
	}

	record := mockRepo.Records["2026-08-20"]
	if record.Financials.GrossSalesMinor != 100000 {
		t.Errorf("Expected GrossSalesMinor 100000, got %d", record.Financials.GrossSalesMinor)
	}
	if record.Financials.TotalOrders != 1 {
		t.Errorf("Expected TotalOrders 1, got %d", record.Financials.TotalOrders)
	}
	if record.Operations.ScoopsSold != 10 {
		t.Errorf("Expected ScoopsSold 10, got %d", record.Operations.ScoopsSold)
	}

	for _, fs := range record.FlavorStats {
		if fs.FlavorID == "F01" && fs.ScoopsSold != 5 {
			t.Errorf("Expected F01 scoops sold 5, got %d", fs.ScoopsSold)
		}
	}

	// Verify event was marked as processed
	processed, _ := mockEventRepo.IsProcessed(context.Background(), "evt_cancel_001")
	if !processed {
		t.Errorf("Expected event evt_cancel_001 to be marked as processed")
	}
}

func TestProcessWasteRecorded(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	// Pre-populate an order so waste_rate can be calculated
	mockRepo.Records["2026-08-20"] = &models.Analytics{
		Date: "2026-08-20",
		Operations: models.Operations{
			ScoopsSold: 100,
		},
		WasteStats: models.WasteStats{
			WasteByReason: []models.WasteByReason{},
		},
	}

	event := models.WasteRecordedEvent{
		ID:     "evt_test_002",
		Type:   "WasteRecorded",
		Time:   "2026-08-20T14:00:00.000Z",
		Source: "batch-inventory-service",
		Data: models.WasteRecordedData{
			WasteID:       "wst_001",
			BatchID:       "B123",
			FlavorID:      "F01",
			FlavorName:    "Vanilla",
			Portions:      10,
			Reason:        "expired",
			CostLostMinor: 25000, // 250.00 THB
			Currency:      "THB",
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
	if record.WasteStats.CostLostMinor != 25000 {
		t.Errorf("Expected CostLostMinor 25000, got %d", record.WasteStats.CostLostMinor)
	}
	if record.Operations.WasteRate != 0.1 {
		t.Errorf("Expected WasteRate 0.1, got %f", record.Operations.WasteRate)
	}
	if len(record.WasteStats.WasteByReason) != 1 {
		t.Fatalf("Expected 1 waste reason, got %d", len(record.WasteStats.WasteByReason))
	}
	if record.WasteStats.WasteByReason[0].CostLostMinor != 25000 {
		t.Errorf("Expected WasteByReason CostLostMinor 25000, got %d", record.WasteStats.WasteByReason[0].CostLostMinor)
	}
}

func TestBatchQueryOrderItems_ClientStreaming(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	mockOrderClient.Orders["ord_001"] = &models.OrderDetails{
		OrderID: "ord_001",
		Items: []models.OrderItemDetail{
			{FlavorID: "F01", FlavorName: "Vanilla", Portions: 2},
		},
	}
	mockOrderClient.Orders["ord_002"] = &models.OrderDetails{
		OrderID: "ord_002",
		Items: []models.OrderItemDetail{
			{FlavorID: "F02", FlavorName: "Pistachio", Portions: 3},
		},
	}

	items, err := svc.BatchQueryOrderItems(context.Background(), []string{"ord_001", "ord_002"})
	if err != nil {
		t.Fatalf("Expected no error from BatchQueryOrderItems: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("Expected 2 consolidated items, got %d", len(items))
	}
	if len(mockOrderClient.StreamedIDs) != 2 {
		t.Errorf("Expected 2 streamed order IDs, got %d", len(mockOrderClient.StreamedIDs))
	}
}

func TestGetAnalyticsSummary(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	today := time.Now().Format("2006-01-02")
	mockRepo.Records[today] = &models.Analytics{
		Date: today,
		Financials: models.Financials{
			GrossSalesMinor: 100000, // 1000.00
			GrossSales:      1000,
			TotalOrders:     10,
		},
		Operations: models.Operations{
			ScoopsSold: 50,
		},
		WasteStats: models.WasteStats{
			TotalWastePortions: 5,
			CostLostMinor:      5000,
			WasteByReason:      []models.WasteByReason{},
		},
		FlavorStats: []models.FlavorStat{
			{FlavorID: "F01", Name: "Vanilla", ScoopsSold: 30, RevenueMinor: 60000, Revenue: 600, WastePortions: 3},
			{FlavorID: "F02", Name: "Chocolate", ScoopsSold: 20, RevenueMinor: 40000, Revenue: 400, WastePortions: 2},
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
}

func TestGetAnalyticsSummary_InvalidPeriod(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	_, err := svc.GetAnalyticsSummary(context.Background(), "invalid")
	if err == nil {
		t.Fatal("Expected error for invalid period, got nil")
	}
}

func TestProcessOrderPlaced_SnakeCasePayload(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	canonicalJSON := `{
		"id": "evt_canonical_001",
		"type": "com.gelatoflow.order.placed.v1",
		"time": "2026-08-20T10:30:00Z",
		"source": "/gelatoflow/order-service",
		"data": {
			"order_id": "ord_snake_001",
			"total_amount_minor": 15000,
			"items": [
				{
					"flavor_id": "flv_01",
					"flavor_name": "Pistachio",
					"portions": 3,
					"unit_price_minor": 5000,
					"subtotal_minor": 15000
				}
			]
		}
	}`

	var event models.OrderPlacedEvent
	if err := json.Unmarshal([]byte(canonicalJSON), &event); err != nil {
		t.Fatalf("Failed to unmarshal canonical snake_case JSON: %v", err)
	}

	if event.Data.OrderID != "ord_snake_001" {
		t.Errorf("Expected OrderID 'ord_snake_001', got '%s'", event.Data.OrderID)
	}
	if event.Data.TotalAmountMinor != 15000 {
		t.Errorf("Expected TotalAmountMinor 15000, got %d", event.Data.TotalAmountMinor)
	}

	err := svc.ProcessOrderPlaced(context.Background(), event)
	if err != nil {
		t.Fatalf("Expected no error processing order, got %v", err)
	}

	record := mockRepo.Records["2026-08-20"]
	if record == nil {
		t.Fatalf("Expected record for 2026-08-20 to be created")
	}
	if record.Financials.GrossSalesMinor != 15000 {
		t.Errorf("Expected GrossSalesMinor 15000, got %d", record.Financials.GrossSalesMinor)
	}
	if record.Operations.ScoopsSold != 3 {
		t.Errorf("Expected ScoopsSold 3, got %d", record.Operations.ScoopsSold)
	}
}

func TestProcessOrderPlaced_Idempotency(t *testing.T) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	event := models.OrderPlacedEvent{
		ID:     "evt_idem_001",
		Time:   "2026-08-20T10:30:00Z",
		Source: "order-service",
		Data: models.OrderPlacedData{
			OrderID:          "ord_idem_001",
			TotalAmountMinor: 10000,
			TotalAmount:      100.00,
			Items: []models.OrderPlacedItem{
				{FlavorID: "F01", FlavorName: "Vanilla", Portions: 2, UnitPriceMinor: 5000, SubtotalMinor: 10000, Subtotal: 100},
			},
		},
	}

	// First execution
	if err := svc.ProcessOrderPlaced(context.Background(), event); err != nil {
		t.Fatalf("First ProcessOrderPlaced failed: %v", err)
	}

	record1 := mockRepo.Records["2026-08-20"]
	if record1.Financials.TotalOrders != 1 {
		t.Fatalf("Expected TotalOrders 1, got %d", record1.Financials.TotalOrders)
	}

	// Duplicate delivery with identical event.ID
	if err := svc.ProcessOrderPlaced(context.Background(), event); err != nil {
		t.Fatalf("Second ProcessOrderPlaced should be a no-op, got error: %v", err)
	}

	record2 := mockRepo.Records["2026-08-20"]
	if record2.Financials.TotalOrders != 1 {
		t.Errorf("Expected TotalOrders to remain 1 after duplicate delivery, got %d", record2.Financials.TotalOrders)
	}
}
