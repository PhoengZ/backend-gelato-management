package models

import "go.mongodb.org/mongo-driver/bson/primitive"

// --- MongoDB Storage Models ---

// Analytics represents a single daily analytics document stored in MongoDB.
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
	Reason   string `bson:"reason" json:"reason"`
	Portions int    `bson:"portions" json:"portions"`
}

type FlavorStat struct {
	FlavorID      string  `bson:"flavor_id" json:"flavor_id"`
	Name          string  `bson:"name" json:"name"`
	ScoopsSold    int     `bson:"scoops_sold" json:"scoops_sold"`
	Revenue       float64 `bson:"revenue" json:"revenue"`
	WastePortions int     `bson:"waste_portions" json:"waste_portions"`
}

// --- API Response DTOs (matching API_SPEC.md GET /api/v1/analytics/summary) ---

type AnalyticsSummaryResponse struct {
	TotalRevenue  float64          `json:"totalRevenue"`
	TotalOrders   int              `json:"totalOrders"`
	TotalScoops   int              `json:"totalScoops"`
	TotalWaste    int              `json:"totalWaste"`
	SalesByFlavor []FlavorSales    `json:"salesByFlavor"`
	WasteByFlavor []FlavorWaste    `json:"wasteByFlavor"`
	SalesTrend    []SalesTrendData `json:"salesTrend"`
}

type FlavorSales struct {
	FlavorID   string  `json:"flavorId"`
	FlavorName string  `json:"flavorName"`
	Portions   int     `json:"portions"`
	Revenue    float64 `json:"revenue"`
}

type FlavorWaste struct {
	FlavorID   string `json:"flavorId"`
	FlavorName string `json:"flavorName"`
	Portions   int    `json:"portions"`
}

type SalesTrendData struct {
	Date    string  `json:"date"`
	Label   string  `json:"label"`
	Revenue float64 `json:"revenue"`
	Orders  int     `json:"orders"`
	Scoops  int     `json:"scoops"`
}

// --- RabbitMQ Event Envelopes (CloudEvents-style) ---

// OrderPlacedEvent is the enriched event published by Order Service after
// successful payment. It contains all item details needed for analytics
// so the Analytics Service never needs to call back to Order/Catalog services.
type OrderPlacedEvent struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Time        string          `json:"time"`
	Source      string          `json:"source"`
	Traceparent string          `json:"traceparent"`
	Data        OrderPlacedData `json:"data"`
}

type OrderPlacedData struct {
	OrderID     string            `json:"orderId"`
	TotalAmount float64           `json:"totalAmount"`
	Items       []OrderPlacedItem `json:"items"`
}

type OrderPlacedItem struct {
	FlavorID   string  `json:"flavorId"`
	FlavorName string  `json:"flavorName"`
	Portions   int     `json:"portions"`
	UnitPrice  float64 `json:"unitPrice"`
	Subtotal   float64 `json:"subtotal"`
}

// OrderCancelledEvent is published by Order Service when an order is cancelled.
type OrderCancelledEvent struct {
	ID          string             `json:"id"`
	Type        string             `json:"type"`
	Time        string             `json:"time"`
	Source      string             `json:"source"`
	Traceparent string             `json:"traceparent"`
	Data        OrderCancelledData `json:"data"`
}

type OrderCancelledData struct {
	OrderID string `json:"orderId"`
	Reason  string `json:"reason"`
}

// WasteRecordedEvent is the event published by Batch Inventory Service
// when waste is recorded (e.g., expired batch, spoilage).
type WasteRecordedEvent struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Time        string            `json:"time"`
	Source      string            `json:"source"`
	Traceparent string            `json:"traceparent"`
	Data        WasteRecordedData `json:"data"`
}

type WasteRecordedData struct {
	WasteID    string `json:"wasteId"`
	BatchID    string `json:"batchId"`
	FlavorID   string `json:"flavorId"`
	FlavorName string `json:"flavorName"`
	Portions   int    `json:"portions"`
	Reason     string `json:"reason"`
}

// Order represents an order stored in the analytics database for reversal on cancellation.
type Order struct {
	ID          string            `bson:"_id" json:"id"`
	Status      string            `bson:"status" json:"status"`
	CreatedAt   string            `bson:"created_at" json:"created_at"`
	TotalAmount float64           `bson:"total_amount" json:"total_amount"`
	Items       []OrderPlacedItem `bson:"items" json:"items"`
}
