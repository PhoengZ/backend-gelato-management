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
	ProcessOrderSuccess(ctx context.Context, msg models.OrderSuccessMessage) error
	ProcessInventoryWaste(ctx context.Context, msg models.InventoryWasteMessage) error
	GetAnalytics(ctx context.Context, period string) ([]models.Analytics, error)
}

type analyticsService struct {
	repo repository.AnalyticsRepository
}

func NewAnalyticsService(repo repository.AnalyticsRepository) AnalyticsService {
	return &analyticsService{repo: repo}
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

func (s *analyticsService) ProcessOrderSuccess(ctx context.Context, msg models.OrderSuccessMessage) error {
	record, err := s.getOrCreateAnalytics(ctx, msg.Date)
	if err != nil {
		return err
	}

	// Update financials
	record.Financials.GrossSales += msg.TotalAmount
	record.Financials.TotalOrders += 1
	record.Financials.AverageOrderValue = record.Financials.GrossSales / float64(record.Financials.TotalOrders)

	// Update operations and flavor stats
	for _, item := range msg.OrderItems {
		record.Operations.ScoopsSold += item.Qty

		// Update FlavorStats
		found := false
		for i, fs := range record.FlavorStats {
			if fs.FlavorID == item.FlavorID {
				record.FlavorStats[i].ScoopsSold += item.Qty
				found = true
				break
			}
		}
		if !found {
			record.FlavorStats = append(record.FlavorStats, models.FlavorStat{
				FlavorID:   item.FlavorID,
				ScoopsSold: item.Qty,
			})
		}
	}

	// Recalculate WasteRate after ScoopsSold changes
	if record.Operations.ScoopsSold > 0 {
		record.Operations.WasteRate = float64(record.WasteStats.TotalWastePortions) / float64(record.Operations.ScoopsSold)
	}

	return s.repo.Save(ctx, record)
}

func (s *analyticsService) ProcessInventoryWaste(ctx context.Context, msg models.InventoryWasteMessage) error {
	record, err := s.getOrCreateAnalytics(ctx, msg.Date)
	if err != nil {
		return err
	}

	// Update waste stats
	record.WasteStats.TotalWastePortions += msg.Portions
	if record.Operations.ScoopsSold > 0 {
		record.Operations.WasteRate = float64(record.WasteStats.TotalWastePortions) / float64(record.Operations.ScoopsSold)
	}

	// Update WasteByReason
	foundReason := false
	for i, wr := range record.WasteStats.WasteByReason {
		if wr.Reason == msg.Reason {
			record.WasteStats.WasteByReason[i].Portions += msg.Portions
			record.WasteStats.WasteByReason[i].CostLost += msg.CostLost
			foundReason = true
			break
		}
	}
	if !foundReason {
		record.WasteStats.WasteByReason = append(record.WasteStats.WasteByReason, models.WasteByReason{
			Reason:   msg.Reason,
			Portions: msg.Portions,
			CostLost: msg.CostLost,
		})
	}

	// Update FlavorStats
	foundFlavor := false
	for i, fs := range record.FlavorStats {
		if fs.FlavorID == msg.FlavorID {
			record.FlavorStats[i].WastePortions += msg.Portions
			foundFlavor = true
			break
		}
	}
	if !foundFlavor {
		record.FlavorStats = append(record.FlavorStats, models.FlavorStat{
			FlavorID:      msg.FlavorID,
			WastePortions: msg.Portions,
		})
	}

	return s.repo.Save(ctx, record)
}

func (s *analyticsService) GetAnalytics(ctx context.Context, period string) ([]models.Analytics, error) {
	// period can be "1d", "1w", "1m", "6m"
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

	return s.repo.FindByDateRange(ctx, startStr, endStr)
}
