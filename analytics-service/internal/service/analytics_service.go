package service

import (
	"context"
	"fmt"
	"time"

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
	ProcessWasteRecorded(ctx context.Context, event models.WasteRecordedEvent) error
	GetAnalyticsSummary(ctx context.Context, period string) (*models.AnalyticsSummaryResponse, error)
}

type analyticsService struct {
	repo repository.AnalyticsRepository
}

func NewAnalyticsService(repo repository.AnalyticsRepository) AnalyticsService {
	return &analyticsService{repo: repo}
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
// scoops sold, and per-flavor sales statistics.
func (s *analyticsService) ProcessOrderPlaced(ctx context.Context, event models.OrderPlacedEvent) error {
	date, err := extractDate(event.Timestamp)
	if err != nil {
		return err
	}

	record, err := s.getOrCreateAnalytics(ctx, date)
	if err != nil {
		return err
	}

	// Update financials
	record.Financials.GrossSales += event.Data.TotalAmount
	record.Financials.TotalOrders += 1
	record.Financials.AverageOrderValue = record.Financials.GrossSales / float64(record.Financials.TotalOrders)

	// Update operations and flavor stats
	for _, item := range event.Data.Items {
		record.Operations.ScoopsSold += item.Portions

		// Update FlavorStats
		found := false
		for i, fs := range record.FlavorStats {
			if fs.FlavorID == item.FlavorID {
				record.FlavorStats[i].ScoopsSold += item.Portions
				record.FlavorStats[i].Revenue += item.Subtotal
				// Always update the name in case it was missing from older records
				if item.FlavorName != "" {
					record.FlavorStats[i].Name = item.FlavorName
				}
				found = true
				break
			}
		}
		if !found {
			record.FlavorStats = append(record.FlavorStats, models.FlavorStat{
				FlavorID:   item.FlavorID,
				Name:       item.FlavorName,
				ScoopsSold: item.Portions,
				Revenue:    item.Subtotal,
			})
		}
	}

	// Recalculate WasteRate after ScoopsSold changes
	if record.Operations.ScoopsSold > 0 {
		record.Operations.WasteRate = float64(record.WasteStats.TotalWastePortions) / float64(record.Operations.ScoopsSold)
	}

	return s.repo.Save(ctx, record)
}

// ProcessWasteRecorded handles the WasteRecorded event published by the
// Batch Inventory Service when waste is logged (e.g., expired batch).
func (s *analyticsService) ProcessWasteRecorded(ctx context.Context, event models.WasteRecordedEvent) error {
	date, err := extractDate(event.Timestamp)
	if err != nil {
		return err
	}

	record, err := s.getOrCreateAnalytics(ctx, date)
	if err != nil {
		return err
	}

	// Update waste stats
	record.WasteStats.TotalWastePortions += event.Data.Portions
	if record.Operations.ScoopsSold > 0 {
		record.Operations.WasteRate = float64(record.WasteStats.TotalWastePortions) / float64(record.Operations.ScoopsSold)
	}

	// Update WasteByReason
	foundReason := false
	for i, wr := range record.WasteStats.WasteByReason {
		if wr.Reason == event.Data.Reason {
			record.WasteStats.WasteByReason[i].Portions += event.Data.Portions
			foundReason = true
			break
		}
	}
	if !foundReason {
		record.WasteStats.WasteByReason = append(record.WasteStats.WasteByReason, models.WasteByReason{
			Reason:   event.Data.Reason,
			Portions: event.Data.Portions,
		})
	}

	// Update FlavorStats
	foundFlavor := false
	for i, fs := range record.FlavorStats {
		if fs.FlavorID == event.Data.FlavorID {
			record.FlavorStats[i].WastePortions += event.Data.Portions
			// Update name if available
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
		})
	}

	return s.repo.Save(ctx, record)
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

	return aggregateSummary(records), nil
}

// aggregateSummary takes a slice of daily Analytics records and combines
// them into a single AnalyticsSummaryResponse with totals, per-flavor
// breakdowns, and a daily sales trend.
func aggregateSummary(records []models.Analytics) *models.AnalyticsSummaryResponse {
	response := &models.AnalyticsSummaryResponse{
		SalesByFlavor: []models.FlavorSales{},
		WasteByFlavor: []models.FlavorWaste{},
		SalesTrend:    []models.SalesTrendData{},
	}

	// Maps for aggregating per-flavor data across all daily records
	salesMap := make(map[string]*models.FlavorSales)
	wasteMap := make(map[string]*models.FlavorWaste)

	for _, record := range records {
		// Accumulate totals
		response.TotalRevenue += record.Financials.GrossSales
		response.TotalOrders += record.Financials.TotalOrders
		response.TotalScoops += record.Operations.ScoopsSold
		response.TotalWaste += record.WasteStats.TotalWastePortions

		// Build salesTrend entry for this day
		dateLabel := formatDateLabel(record.Date)
		response.SalesTrend = append(response.SalesTrend, models.SalesTrendData{
			Date:    record.Date,
			Label:   dateLabel,
			Revenue: record.Financials.GrossSales,
			Orders:  record.Financials.TotalOrders,
			Scoops:  record.Operations.ScoopsSold,
		})

		// Aggregate per-flavor stats
		for _, fs := range record.FlavorStats {
			// Sales aggregation
			if fs.ScoopsSold > 0 || fs.Revenue > 0 {
				if existing, ok := salesMap[fs.FlavorID]; ok {
					existing.Portions += fs.ScoopsSold
					existing.Revenue += fs.Revenue
					// Update name if we have a newer non-empty one
					if fs.Name != "" {
						existing.FlavorName = fs.Name
					}
				} else {
					salesMap[fs.FlavorID] = &models.FlavorSales{
						FlavorID:   fs.FlavorID,
						FlavorName: fs.Name,
						Portions:   fs.ScoopsSold,
						Revenue:    fs.Revenue,
					}
				}
			}

			// Waste aggregation
			if fs.WastePortions > 0 {
				if existing, ok := wasteMap[fs.FlavorID]; ok {
					existing.Portions += fs.WastePortions
					if fs.Name != "" {
						existing.FlavorName = fs.Name
					}
				} else {
					wasteMap[fs.FlavorID] = &models.FlavorWaste{
						FlavorID:   fs.FlavorID,
						FlavorName: fs.Name,
						Portions:   fs.WastePortions,
					}
				}
			}
		}
	}

	// Convert maps to slices
	for _, v := range salesMap {
		response.SalesByFlavor = append(response.SalesByFlavor, *v)
	}
	for _, v := range wasteMap {
		response.WasteByFlavor = append(response.WasteByFlavor, *v)
	}

	return response
}

// formatDateLabel converts "2026-09-01" to "Sep 1" for the salesTrend label.
func formatDateLabel(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	return t.Format("Jan 2")
}
