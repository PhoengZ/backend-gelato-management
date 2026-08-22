package models

import "go.mongodb.org/mongo-driver/bson/primitive"

type Analytics struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Date        string             `bson:"date" json:"date"`
	Financials  Financials         `bson:"financials" json:"financials"`
	Operations  Operations         `bson:"operations" json:"operations"`
	WasteStats  WasteStats         `bson:"waste_stats" json:"waste_stats"`
	FlavorStats []FlavorStat       `bson:"flavor_stats" json:"flavor_stats"`
}

type Financials struct {
	GrossSales        float64 `bson:"gross_sales" json:"gross_sales"`
	TotalOrders       int     `bson:"total_orders" json:"total_orders"`
	AverageOrderValue float64 `bson:"average_order_value" json:"average_order_value"`
}

type Operations struct {
	ScoopsSold int     `bson:"scoops_sold" json:"scoops_sold"`
	WasteRate  float64 `bson:"waste_rate" json:"waste_rate"`
}

type WasteStats struct {
	TotalWastePortions int             `bson:"total_waste_portions" json:"total_waste_portions"`
	WasteByReason      []WasteByReason `bson:"waste_by_reason" json:"waste_by_reason"`
}

type WasteByReason struct {
	Reason   string  `bson:"reason" json:"reason"`
	Portions int     `bson:"portions" json:"portions"`
	CostLost float64 `bson:"cost_lost" json:"cost_lost"`
}

type FlavorStat struct {
	FlavorID      string `bson:"flavor_id" json:"flavor_id"`
	Name          string `bson:"name,omitempty" json:"name,omitempty"` // Note: name might not be available in message, maybe we just store ID for now, or fetch from somewhere else if needed.
	ScoopsSold    int    `bson:"scoops_sold" json:"scoops_sold"`
	WastePortions int    `bson:"waste_portions" json:"waste_portions"`
}

// Message Payloads
type OrderSuccessMessage struct {
	Date        string      `json:"date"`
	TotalAmount float64     `json:"total_amount"`
	OrderItems  []OrderItem `json:"order_items"`
}

type OrderItem struct {
	FlavorID string `json:"flavor_id"`
	Qty      int    `json:"qty"`
}

type InventoryWasteMessage struct {
	Date     string  `json:"date"` // Assume it might be provided or use current date if absent
	FlavorID string  `json:"flavor_id"`
	Portions int     `json:"portions"`
	BatchID  string  `json:"batch_id"`
	Reason   string  `json:"reason"`
	CostLost float64 `json:"cost_lost"`
}
