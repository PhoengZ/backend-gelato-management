package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"analytics-service/internal/handler"
	"analytics-service/internal/models"
	"analytics-service/internal/router"
	"analytics-service/internal/service"

	"github.com/gofiber/fiber/v2"
)

func setupTestApp() (*fiber.App, *MockRepository) {
	mockRepo := NewMockRepository()
	mockEventRepo := NewMockEventRepository()
	mockOrderClient := NewMockOrderClient()
	svc := service.NewAnalyticsService(mockRepo, mockEventRepo, mockOrderClient)

	app := fiber.New(fiber.Config{
		ErrorHandler: handler.CustomErrorHandler,
	})
	router.SetupRoutes(app, svc)

	return app, mockRepo
}

func TestGetAnalyticsSummary_Handler_Success(t *testing.T) {
	app, mockRepo := setupTestApp()

	today := time.Now().Format("2006-01-02")
	mockRepo.Records[today] = &models.Analytics{
		Date: today,
		Financials: models.Financials{
			GrossSales:  5000,
			TotalOrders: 20,
		},
		Operations: models.Operations{
			ScoopsSold: 60,
		},
		WasteStats: models.WasteStats{
			TotalWastePortions: 4,
			WasteByReason:      []models.WasteByReason{},
		},
		FlavorStats: []models.FlavorStat{
			{FlavorID: "flv_01", Name: "Vanilla", ScoopsSold: 35, Revenue: 3000, WastePortions: 2},
			{FlavorID: "flv_02", Name: "Chocolate", ScoopsSold: 25, Revenue: 2000, WastePortions: 2},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary?period=1w", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200 OK, got %d", resp.StatusCode)
	}

	var body models.AnalyticsSummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode response body: %v", err)
	}

	if body.TotalRevenue != 5000 {
		t.Errorf("Expected TotalRevenue 5000, got %f", body.TotalRevenue)
	}
	if body.TotalOrders != 20 {
		t.Errorf("Expected TotalOrders 20, got %d", body.TotalOrders)
	}
	if len(body.SalesByFlavor) != 2 {
		t.Errorf("Expected 2 SalesByFlavor items, got %d", len(body.SalesByFlavor))
	}
}

func TestGetAnalyticsSummary_Handler_InvalidPeriod(t *testing.T) {
	app, _ := setupTestApp()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary?period=invalid_period", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400 Bad Request, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	var errResp models.ApiErrorResponse
	if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
		t.Fatalf("Failed to decode ApiErrorResponse: %v, raw: %s", err, string(bodyBytes))
	}

	if errResp.Code != "HTTP_400" {
		t.Errorf("Expected error code 'HTTP_400', got '%s'", errResp.Code)
	}
	if errResp.Message == "" {
		t.Errorf("Expected non-empty error message, got empty string")
	}
}

func TestGetAnalyticsSummary_Handler_NotFound(t *testing.T) {
	app, _ := setupTestApp()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/unknown-path", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected status 404 Not Found, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	var errResp models.ApiErrorResponse
	if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
		t.Fatalf("Failed to decode ApiErrorResponse: %v, raw: %s", err, string(bodyBytes))
	}

	if errResp.Code != "HTTP_404" {
		t.Errorf("Expected error code 'HTTP_404', got '%s'", errResp.Code)
	}
}
