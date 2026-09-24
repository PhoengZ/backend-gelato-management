package service

import (
	"context"
	"fmt"
	"time"

	"analytics-service/internal/client"
	"analytics-service/internal/factory"
	"analytics-service/internal/models"
	"analytics-service/internal/repository"
)

// Sentinel errors for validation
var (
	ErrInvalidPeriod = fmt.Errorf("unsupported analytics period")
	ErrInvalidDate   = fmt.Errorf("invalid or missing date")
)

type AnalyticsService interface {
	ProcessOrderPlaced(ctx context.Context, event models.OrderPlacedEvent) error
	ProcessOrderCancelled(ctx context.Context, event models.OrderCancelledEvent) error
	ProcessWasteRecorded(ctx context.Context, event models.WasteRecordedEvent) error
	GetAnalyticsSummary(ctx context.Context, period string) (*models.AnalyticsSummaryResponse, error)
	BatchQueryOrderItems(ctx context.Context, orderIDs []string) ([]models.OrderItemDetail, error)
}

type analyticsService struct {
	repo        repository.AnalyticsRepository
	eventRepo   repository.EventRepository
	orderClient client.OrderClient
}

func NewAnalyticsService(
	repo repository.AnalyticsRepository,
	eventRepo repository.EventRepository,
	orderClient client.OrderClient,
) AnalyticsService {
	return &analyticsService{
		repo:        repo,
		eventRepo:   eventRepo,
		orderClient: orderClient,
	}
}

// extractDate parses an ISO 8601 timestamp from the event envelope and
// returns the date portion (YYYY-MM-DD) used as the daily record key.
func extractDate(timestamp string) (string, error) {
	if timestamp == "" {
		return "", ErrInvalidDate
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		// Fallback: try parsing as date-only
		t, err = time.Parse("2006-01-02", timestamp)
		if err != nil {
			return "", fmt.Errorf("%w: %s", ErrInvalidDate, timestamp)
		}
	}
	return t.Format("2006-01-02"), nil
}

func (s *analyticsService) getOrCreateAnalytics(ctx context.Context, date string) (*models.Analytics, error) {
	if date == "" {
		return nil, ErrInvalidDate
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidDate, date)
	}

	record, err := s.repo.FindByDate(ctx, date)
	if err != nil {
		return nil, err
	}
	if record == nil {
		record = &models.Analytics{
			Date: date,
			WasteStats: models.WasteStats{
				WasteByReason: []models.WasteByReason{},
			},
			FlavorStats: []models.FlavorStat{},
		}
	}
	return record, nil
}

// ProcessOrderPlaced handles the OrderPlaced event published by the Order
// Service after successful payment. It updates daily revenue, order count,
// scoops sold, and per-flavor sales statistics in integer minor units.
// Note: Analytics Service does NOT store shadow copies of orders in its database.
func (s *analyticsService) ProcessOrderPlaced(ctx context.Context, event models.OrderPlacedEvent) error {
	// Check event deduplication by event.ID (CloudEvents 1.0)
	if event.ID != "" && s.eventRepo != nil {
		processed, err := s.eventRepo.IsProcessed(ctx, event.ID)
		if err != nil {
			return fmt.Errorf("failed to check event deduplication: %w", err)
		}
		if processed {
			// Order already processed; idempotent no-op
			return nil
		}
	}

	date, err := extractDate(event.Time)
	if err != nil {
		return err
	}

	record, err := s.getOrCreateAnalytics(ctx, date)
	if err != nil {
		return err
	}

	// Calculate minor units
	totalAmountMinor := event.Data.TotalAmountMinor
	if totalAmountMinor == 0 && event.Data.TotalAmount > 0 {
		totalAmountMinor = int64(event.Data.TotalAmount * 100.0)
	}

	// Update financials
	record.Financials.GrossSalesMinor += totalAmountMinor
	record.Financials.GrossSales = float64(record.Financials.GrossSalesMinor) / 100.0
	record.Financials.TotalOrders += 1
	if record.Financials.TotalOrders > 0 {
		record.Financials.AverageOrderValueMinor = record.Financials.GrossSalesMinor / int64(record.Financials.TotalOrders)
		record.Financials.AverageOrderValue = float64(record.Financials.AverageOrderValueMinor) / 100.0
	}
	if event.Data.Currency != "" {
		record.Financials.Currency = event.Data.Currency
	}

	// Update operations and flavor stats
	for _, item := range event.Data.Items {
		record.Operations.ScoopsSold += item.Portions

		subtotalMinor := item.SubtotalMinor
		if subtotalMinor == 0 && item.Subtotal > 0 {
			subtotalMinor = int64(item.Subtotal * 100.0)
		}

		// Update FlavorStats
		found := false
		for i, fs := range record.FlavorStats {
			if fs.FlavorID == item.FlavorID {
				record.FlavorStats[i].ScoopsSold += item.Portions
				record.FlavorStats[i].RevenueMinor += subtotalMinor
				record.FlavorStats[i].Revenue = float64(record.FlavorStats[i].RevenueMinor) / 100.0
				if item.FlavorName != "" {
					record.FlavorStats[i].Name = item.FlavorName
				}
				found = true
				break
			}
		}
		if !found {
			record.FlavorStats = append(record.FlavorStats, models.FlavorStat{
				FlavorID:     item.FlavorID,
				Name:         item.FlavorName,
				ScoopsSold:   item.Portions,
				RevenueMinor: subtotalMinor,
				Revenue:      float64(subtotalMinor) / 100.0,
			})
		}
	}

	// Recalculate WasteRate after ScoopsSold changes
	if record.Operations.ScoopsSold > 0 {
		record.Operations.WasteRate = float64(record.WasteStats.TotalWastePortions) / float64(record.Operations.ScoopsSold)
	}

	if err := s.repo.Save(ctx, record); err != nil {
		return err
	}

	// Mark event as processed for idempotency
	if event.ID != "" && s.eventRepo != nil {
		_ = s.eventRepo.MarkProcessed(ctx, event.ID, "OrderPlaced")
	}

	return nil
}

// ProcessOrderCancelled handles the OrderCancelled thin event. It queries the
// Order Service via gRPC to obtain the order snapshot, then applies the negative
// offset to the daily analytics stats without using any local shadow database table.
func (s *analyticsService) ProcessOrderCancelled(ctx context.Context, event models.OrderCancelledEvent) error {
	// Deduplicate event by event.ID
	if event.ID != "" && s.eventRepo != nil {
		processed, err := s.eventRepo.IsProcessed(ctx, event.ID)
		if err != nil {
			return fmt.Errorf("failed to check event deduplication: %w", err)
		}
		if processed {
			return nil
		}
	}

	if s.orderClient == nil {
		return fmt.Errorf("order client is not configured for gRPC query")
	}

	// Fetch authoritative order details from Order Service via gRPC
	order, err := s.orderClient.GetOrderDetails(ctx, event.Data.OrderID)
	if err != nil {
		return fmt.Errorf("failed to fetch order details from order service via gRPC: %w", err)
	}
	if order == nil {
		return fmt.Errorf("order not found from order service for cancellation: %s", event.Data.OrderID)
	}

	dateStr := order.CreatedAt
	if dateStr == "" {
		dateStr = event.Time
	}
	date, err := extractDate(dateStr)
	if err != nil {
		return err
	}

	record, err := s.getOrCreateAnalytics(ctx, date)
	if err != nil {
		return err
	}

	// Update financials
	record.Financials.GrossSalesMinor -= order.TotalAmountMinor
	if record.Financials.GrossSalesMinor < 0 {
		record.Financials.GrossSalesMinor = 0
	}
	record.Financials.GrossSales = float64(record.Financials.GrossSalesMinor) / 100.0

	record.Financials.TotalOrders -= 1
	if record.Financials.TotalOrders > 0 {
		record.Financials.AverageOrderValueMinor = record.Financials.GrossSalesMinor / int64(record.Financials.TotalOrders)
		record.Financials.AverageOrderValue = float64(record.Financials.AverageOrderValueMinor) / 100.0
	} else {
		record.Financials.TotalOrders = 0
		record.Financials.AverageOrderValueMinor = 0
		record.Financials.AverageOrderValue = 0
	}

	// Update operations and flavor stats
	for _, item := range order.Items {
		record.Operations.ScoopsSold -= item.Portions
		if record.Operations.ScoopsSold < 0 {
			record.Operations.ScoopsSold = 0
		}

		// Update FlavorStats
		for i, fs := range record.FlavorStats {
			if fs.FlavorID == item.FlavorID {
				record.FlavorStats[i].ScoopsSold -= item.Portions
				if record.FlavorStats[i].ScoopsSold < 0 {
					record.FlavorStats[i].ScoopsSold = 0
				}
				record.FlavorStats[i].RevenueMinor -= item.SubtotalMinor
				if record.FlavorStats[i].RevenueMinor < 0 {
					record.FlavorStats[i].RevenueMinor = 0
				}
				record.FlavorStats[i].Revenue = float64(record.FlavorStats[i].RevenueMinor) / 100.0
				break
			}
		}
	}

	// Recalculate WasteRate after ScoopsSold changes
	if record.Operations.ScoopsSold > 0 {
		record.Operations.WasteRate = float64(record.WasteStats.TotalWastePortions) / float64(record.Operations.ScoopsSold)
	} else {
		record.Operations.WasteRate = 0
	}

	if err := s.repo.Save(ctx, record); err != nil {
		return err
	}

	// Mark event as processed
	if event.ID != "" && s.eventRepo != nil {
		_ = s.eventRepo.MarkProcessed(ctx, event.ID, "OrderCancelled")
	}

	return nil
}

// ProcessWasteRecorded handles the WasteRecorded event published by the
// Batch Inventory Service when waste is logged (e.g., expired batch).
func (s *analyticsService) ProcessWasteRecorded(ctx context.Context, event models.WasteRecordedEvent) error {
	// Deduplicate event by event.ID
	if event.ID != "" && s.eventRepo != nil {
		processed, err := s.eventRepo.IsProcessed(ctx, event.ID)
		if err != nil {
			return fmt.Errorf("failed to check event deduplication: %w", err)
		}
		if processed {
			return nil
		}
	}

	date, err := extractDate(event.Time)
	if err != nil {
		return err
	}

	record, err := s.getOrCreateAnalytics(ctx, date)
	if err != nil {
		return err
	}

	// Update waste stats
	record.WasteStats.TotalWastePortions += event.Data.Portions
	record.WasteStats.CostLostMinor += event.Data.CostLostMinor
	if record.Operations.ScoopsSold > 0 {
		record.Operations.WasteRate = float64(record.WasteStats.TotalWastePortions) / float64(record.Operations.ScoopsSold)
	}

	// Update WasteByReason
	foundReason := false
	for i, wr := range record.WasteStats.WasteByReason {
		if wr.Reason == event.Data.Reason {
			record.WasteStats.WasteByReason[i].Portions += event.Data.Portions
			record.WasteStats.WasteByReason[i].CostLostMinor += event.Data.CostLostMinor
			foundReason = true
			break
		}
	}
	if !foundReason {
		record.WasteStats.WasteByReason = append(record.WasteStats.WasteByReason, models.WasteByReason{
			Reason:        event.Data.Reason,
			Portions:      event.Data.Portions,
			CostLostMinor: event.Data.CostLostMinor,
		})
	}

	// Update FlavorStats
	foundFlavor := false
	for i, fs := range record.FlavorStats {
		if fs.FlavorID == event.Data.FlavorID {
			record.FlavorStats[i].WastePortions += event.Data.Portions
			record.FlavorStats[i].CostLostMinor += event.Data.CostLostMinor
			if event.Data.FlavorName != "" {
				record.FlavorStats[i].Name = event.Data.FlavorName
			}
			foundFlavor = true
			break
		}
	}
	if !foundFlavor {
		record.FlavorStats = append(record.FlavorStats, models.FlavorStat{
			FlavorID:      event.Data.FlavorID,
			Name:          event.Data.FlavorName,
			WastePortions: event.Data.Portions,
			CostLostMinor: event.Data.CostLostMinor,
		})
	}

	if err := s.repo.Save(ctx, record); err != nil {
		return err
	}

	// Mark event as processed
	if event.ID != "" && s.eventRepo != nil {
		_ = s.eventRepo.MarkProcessed(ctx, event.ID, "WasteRecorded")
	}

	return nil
}

// BatchQueryOrderItems streams multiple order IDs over client-streaming gRPC
// to Order Service to retrieve consolidated line items for batch analytics.
func (s *analyticsService) BatchQueryOrderItems(ctx context.Context, orderIDs []string) ([]models.OrderItemDetail, error) {
	if s.orderClient == nil {
		return nil, fmt.Errorf("order client is not configured for gRPC streaming")
	}
	return s.orderClient.StreamOrderItems(ctx, orderIDs)
}

// GetAnalyticsSummary fetches daily records for the given period from MongoDB
// and aggregates them into a single AnalyticsSummaryResponse matching the
// API_SPEC.md schema for GET /api/v1/analytics/summary.
func (s *analyticsService) GetAnalyticsSummary(ctx context.Context, period string) (*models.AnalyticsSummaryResponse, error) {
	endDate := time.Now()
	var startDate time.Time

	switch period {
	case "1d":
		startDate = endDate.AddDate(0, 0, -1)
	case "1w":
		startDate = endDate.AddDate(0, 0, -7)
	case "1m":
		startDate = endDate.AddDate(0, -1, 0)
	case "6m":
		startDate = endDate.AddDate(0, -6, 0)
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidPeriod, period)
	}

	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	records, err := s.repo.FindByDateRange(ctx, startStr, endStr)
	if err != nil {
		return nil, err
	}

	return factory.BuildAnalyticsSummaryResponse(records), nil
}
