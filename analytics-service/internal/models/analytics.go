package models

import (
	"encoding/json"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

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
	GrossSalesMinor        int64   `bson:"gross_sales_minor" json:"gross_sales_minor"`
	GrossSales             float64 `bson:"gross_sales,omitempty" json:"gross_sales,omitempty"` // legacy fallback
	TotalOrders            int     `bson:"total_orders" json:"total_orders"`
	AverageOrderValueMinor int64   `bson:"average_order_value_minor" json:"average_order_value_minor"`
	AverageOrderValue      float64 `bson:"average_order_value,omitempty" json:"average_order_value,omitempty"`
	Currency               string  `bson:"currency,omitempty" json:"currency,omitempty"`
}

type Operations struct {
	ScoopsSold int     `bson:"scoops_sold" json:"scoops_sold"`
	WasteRate  float64 `bson:"waste_rate" json:"waste_rate"`
}

type WasteStats struct {
	TotalWastePortions int             `bson:"total_waste_portions" json:"total_waste_portions"`
	CostLostMinor      int64           `bson:"cost_lost_minor" json:"cost_lost_minor"`
	WasteByReason      []WasteByReason `bson:"waste_by_reason" json:"waste_by_reason"`
}

type WasteByReason struct {
	Reason        string `bson:"reason" json:"reason"`
	Portions      int    `bson:"portions" json:"portions"`
	CostLostMinor int64  `bson:"cost_lost_minor,omitempty" json:"cost_lost_minor,omitempty"`
}

type FlavorStat struct {
	FlavorID      string  `bson:"flavor_id" json:"flavor_id"`
	Name          string  `bson:"name" json:"name"`
	ScoopsSold    int     `bson:"scoops_sold" json:"scoops_sold"`
	RevenueMinor  int64   `bson:"revenue_minor" json:"revenue_minor"`
	Revenue       float64 `bson:"revenue,omitempty" json:"revenue,omitempty"` // legacy fallback
	WastePortions int     `bson:"waste_portions" json:"waste_portions"`
	CostLostMinor int64   `bson:"cost_lost_minor,omitempty" json:"cost_lost_minor,omitempty"`
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
	OrderID          string            `json:"orderId"`
	TotalAmount      float64           `json:"totalAmount"`
	TotalAmountMinor int64             `json:"totalAmountMinor"`
	Currency         string            `json:"currency"`
	Items            []OrderPlacedItem `json:"items"`
}

func (d *OrderPlacedData) UnmarshalJSON(b []byte) error {
	type Alias OrderPlacedData
	aux := struct {
		*Alias
		OrderIDSnake     string  `json:"order_id"`
		TotalAmountSnake float64 `json:"total_amount"`
		TotalAmountMinor int64   `json:"total_amount_minor"`
		Currency         string  `json:"currency"`
	}{
		Alias: (*Alias)(d),
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if d.OrderID == "" && aux.OrderIDSnake != "" {
		d.OrderID = aux.OrderIDSnake
	}
	if aux.Currency != "" {
		d.Currency = aux.Currency
	}
	if aux.TotalAmountMinor > 0 {
		d.TotalAmountMinor = aux.TotalAmountMinor
	}
	if d.TotalAmount == 0 {
		if d.TotalAmountMinor > 0 {
			d.TotalAmount = float64(d.TotalAmountMinor) / 100.0
		} else if aux.TotalAmountSnake > 0 {
			d.TotalAmount = aux.TotalAmountSnake
			d.TotalAmountMinor = int64(aux.TotalAmountSnake * 100.0)
		}
	} else if d.TotalAmountMinor == 0 {
		d.TotalAmountMinor = int64(d.TotalAmount * 100.0)
	}
	return nil
}

type OrderPlacedItem struct {
	FlavorID       string  `json:"flavorId"`
	FlavorName     string  `json:"flavorName"`
	Portions       int     `json:"portions"`
	UnitPrice      float64 `json:"unitPrice"`
	UnitPriceMinor int64   `json:"unitPriceMinor"`
	Subtotal       float64 `json:"subtotal"`
	SubtotalMinor  int64   `json:"subtotalMinor"`
}

func (item *OrderPlacedItem) UnmarshalJSON(b []byte) error {
	type Alias OrderPlacedItem
	aux := struct {
		*Alias
		FlavorIDSnake   string  `json:"flavor_id"`
		FlavorNameSnake string  `json:"flavor_name"`
		UnitPriceSnake  float64 `json:"unit_price"`
		UnitPriceMinor  int64   `json:"unit_price_minor"`
		SubtotalSnake   float64 `json:"subtotal"`
		SubtotalMinor   int64   `json:"subtotal_minor"`
	}{
		Alias: (*Alias)(item),
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if item.FlavorID == "" && aux.FlavorIDSnake != "" {
		item.FlavorID = aux.FlavorIDSnake
	}
	if item.FlavorName == "" && aux.FlavorNameSnake != "" {
		item.FlavorName = aux.FlavorNameSnake
	}
	if aux.UnitPriceMinor > 0 {
		item.UnitPriceMinor = aux.UnitPriceMinor
	}
	if aux.SubtotalMinor > 0 {
		item.SubtotalMinor = aux.SubtotalMinor
	}
	if item.UnitPrice == 0 {
		if item.UnitPriceMinor > 0 {
			item.UnitPrice = float64(item.UnitPriceMinor) / 100.0
		} else if aux.UnitPriceSnake > 0 {
			item.UnitPrice = aux.UnitPriceSnake
			item.UnitPriceMinor = int64(aux.UnitPriceSnake * 100.0)
		}
	} else if item.UnitPriceMinor == 0 {
		item.UnitPriceMinor = int64(item.UnitPrice * 100.0)
	}
	if item.Subtotal == 0 {
		if item.SubtotalMinor > 0 {
			item.Subtotal = float64(item.SubtotalMinor) / 100.0
		} else if aux.SubtotalSnake > 0 {
			item.Subtotal = aux.SubtotalSnake
			item.SubtotalMinor = int64(aux.SubtotalSnake * 100.0)
		}
	} else if item.SubtotalMinor == 0 {
		item.SubtotalMinor = int64(item.Subtotal * 100.0)
	}
	return nil
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

func (c *OrderCancelledData) UnmarshalJSON(b []byte) error {
	type Alias OrderCancelledData
	aux := struct {
		*Alias
		OrderIDSnake string `json:"order_id"`
	}{
		Alias: (*Alias)(c),
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if c.OrderID == "" && aux.OrderIDSnake != "" {
		c.OrderID = aux.OrderIDSnake
	}
	return nil
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
	WasteID       string `json:"wasteId"`
	BatchID       string `json:"batchId"`
	FlavorID      string `json:"flavorId"`
	FlavorName    string `json:"flavorName"`
	Portions      int    `json:"portions"`
	Reason        string `json:"reason"`
	CostLostMinor int64  `json:"costLostMinor"`
	Currency      string `json:"currency"`
}

func (w *WasteRecordedData) UnmarshalJSON(b []byte) error {
	type Alias WasteRecordedData
	aux := struct {
		*Alias
		WasteIDSnake       string `json:"waste_id"`
		BatchIDSnake       string `json:"batch_id"`
		FlavorIDSnake      string `json:"flavor_id"`
		FlavorNameSnake    string `json:"flavor_name"`
		CostLostMinorSnake int64  `json:"cost_lost_minor"`
		CurrencySnake      string `json:"currency"`
	}{
		Alias: (*Alias)(w),
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if w.WasteID == "" && aux.WasteIDSnake != "" {
		w.WasteID = aux.WasteIDSnake
	}
	if w.BatchID == "" && aux.BatchIDSnake != "" {
		w.BatchID = aux.BatchIDSnake
	}
	if w.FlavorID == "" && aux.FlavorIDSnake != "" {
		w.FlavorID = aux.FlavorIDSnake
	}
	if w.FlavorName == "" && aux.FlavorNameSnake != "" {
		w.FlavorName = aux.FlavorNameSnake
	}
	if aux.CostLostMinorSnake > 0 {
		w.CostLostMinor = aux.CostLostMinorSnake
	}
	if aux.CurrencySnake != "" {
		w.Currency = aux.CurrencySnake
	}
	return nil
}

// OrderDetails represents the order structure queried on-demand from Order Service via gRPC.
// Analytics Service never stores this in its local database.
type OrderDetails struct {
	OrderID          string            `json:"order_id"`
	CustomerID       string            `json:"customer_id"`
	Status           string            `json:"status"`
	CreatedAt        string            `json:"created_at"`
	TotalAmountMinor int64             `json:"total_amount_minor"`
	Currency         string            `json:"currency"`
	Items            []OrderItemDetail `json:"items"`
}

type OrderItemDetail struct {
	FlavorID       string `json:"flavor_id"`
	FlavorName     string `json:"flavor_name"`
	Portions       int    `json:"portions"`
	UnitPriceMinor int64  `json:"unit_price_minor"`
	SubtotalMinor  int64  `json:"subtotal_minor"`
}
