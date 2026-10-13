package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestServiceConstants(t *testing.T) {
	if LowStockThreshold != 10 {
		t.Fatalf("expected LowStockThreshold to be 10, got %d", LowStockThreshold)
	}
	if ExpiryAlertWindow != 48*time.Hour {
		t.Fatalf("expected ExpiryAlertWindow to be 48h, got %v", ExpiryAlertWindow)
	}
	if TypeLowStock != "com.gelatoflow.inventory.batch-low-stock.v1" {
		t.Fatalf("unexpected TypeLowStock: %s", TypeLowStock)
	}
	if TypeExpiring != "com.gelatoflow.inventory.batch-expiring.v1" {
		t.Fatalf("unexpected TypeExpiring: %s", TypeExpiring)
	}
	if RoutingKeyLowStock != "inventory.low_stock" {
		t.Fatalf("unexpected RoutingKeyLowStock: %s", RoutingKeyLowStock)
	}
	if RoutingKeyExpiring != "inventory.expiring" {
		t.Fatalf("unexpected RoutingKeyExpiring: %s", RoutingKeyExpiring)
	}
}

func TestLowStockCloudEventPayloadShape(t *testing.T) {
	batchID := uuid.NewString()
	flavorID := uuid.NewString()
	now := time.Now().UTC()

	event := map[string]any{
		"specversion":     "1.0",
		"id":              uuid.NewString(),
		"source":          EventSource,
		"type":            TypeLowStock,
		"time":            now.Format(time.RFC3339Nano),
		"datacontenttype": "application/json",
		"data": map[string]any{
			"batch_id":           batchID,
			"flavor_id":          flavorID,
			"remaining_portions": 7,
			"threshold_portions": LowStockThreshold,
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed struct {
		SpecVersion string `json:"specversion"`
		ID          string `json:"id"`
		Source      string `json:"source"`
		Type        string `json:"type"`
		Time        string `json:"time"`
		Data        struct {
			BatchID           string `json:"batch_id"`
			FlavorID          string `json:"flavor_id"`
			RemainingPortions int    `json:"remaining_portions"`
			ThresholdPortions int    `json:"threshold_portions"`
		} `json:"data"`
	}

	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed.SpecVersion != "1.0" {
		t.Errorf("expected specversion 1.0, got %s", parsed.SpecVersion)
	}
	if parsed.Source != "/gelatoflow/batch-inventory-service" {
		t.Errorf("expected source /gelatoflow/batch-inventory-service, got %s", parsed.Source)
	}
	if parsed.Type != TypeLowStock {
		t.Errorf("expected type %s, got %s", TypeLowStock, parsed.Type)
	}
	if parsed.Data.BatchID != batchID {
		t.Errorf("expected batch_id %s, got %s", batchID, parsed.Data.BatchID)
	}
	if parsed.Data.FlavorID != flavorID {
		t.Errorf("expected flavor_id %s, got %s", flavorID, parsed.Data.FlavorID)
	}
	if parsed.Data.RemainingPortions != 7 {
		t.Errorf("expected remaining_portions 7, got %d", parsed.Data.RemainingPortions)
	}
	if parsed.Data.ThresholdPortions != 10 {
		t.Errorf("expected threshold_portions 10, got %d", parsed.Data.ThresholdPortions)
	}
}

func TestExpiringCloudEventPayloadShape(t *testing.T) {
	batchID := uuid.NewString()
	flavorID := uuid.NewString()
	now := time.Now().UTC()
	exp := now.Add(24 * time.Hour)

	event := map[string]any{
		"specversion":     "1.0",
		"id":              uuid.NewString(),
		"source":          EventSource,
		"type":            TypeExpiring,
		"time":            now.Format(time.RFC3339Nano),
		"datacontenttype": "application/json",
		"data": map[string]any{
			"batch_id":   batchID,
			"flavor_id":  flavorID,
			"expires_at": exp.Format(time.RFC3339),
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed struct {
		SpecVersion string `json:"specversion"`
		ID          string `json:"id"`
		Source      string `json:"source"`
		Type        string `json:"type"`
		Time        string `json:"time"`
		Data        struct {
			BatchID   string `json:"batch_id"`
			FlavorID  string `json:"flavor_id"`
			ExpiresAt string `json:"expires_at"`
		} `json:"data"`
	}

	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed.SpecVersion != "1.0" {
		t.Errorf("expected specversion 1.0, got %s", parsed.SpecVersion)
	}
	if parsed.Type != TypeExpiring {
		t.Errorf("expected type %s, got %s", TypeExpiring, parsed.Type)
	}
	if parsed.Data.BatchID != batchID {
		t.Errorf("expected batch_id %s, got %s", batchID, parsed.Data.BatchID)
	}
	if parsed.Data.FlavorID != flavorID {
		t.Errorf("expected flavor_id %s, got %s", flavorID, parsed.Data.FlavorID)
	}
}
