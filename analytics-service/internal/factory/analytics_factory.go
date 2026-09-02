package factory

import (
	"time"

	"analytics-service/internal/models"
)

// BuildAnalyticsSummaryResponse takes a slice of daily Analytics records (Domain/Data models)
// and transforms them into an AnalyticsSummaryResponse DTO.
// This implements the DTO Assembler/Factory pattern, keeping mapping logic out of the Service layer.
func BuildAnalyticsSummaryResponse(records []models.Analytics) *models.AnalyticsSummaryResponse {
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
