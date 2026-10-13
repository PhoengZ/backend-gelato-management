package outbox

import (
	"encoding/json"
	"testing"
)

func TestResolveRoutingKey(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		expected  string
	}{
		{
			name:      "waste event canonical",
			eventType: TypeWasteRecorded,
			expected:  RoutingKeyWaste,
		},
		{
			name:      "waste event short",
			eventType: RoutingKeyWaste,
			expected:  RoutingKeyWaste,
		},
		{
			name:      "low stock canonical",
			eventType: TypeLowStock,
			expected:  RoutingKeyLowStock,
		},
		{
			name:      "low stock alternative",
			eventType: "com.gelatoflow.inventory.low-stock.v1",
			expected:  RoutingKeyLowStock,
		},
		{
			name:      "low stock short",
			eventType: RoutingKeyLowStock,
			expected:  RoutingKeyLowStock,
		},
		{
			name:      "expiring canonical",
			eventType: TypeExpiring,
			expected:  RoutingKeyExpiring,
		},
		{
			name:      "expiring alternative",
			eventType: "com.gelatoflow.inventory.expiring.v1",
			expected:  RoutingKeyExpiring,
		},
		{
			name:      "expiring short",
			eventType: RoutingKeyExpiring,
			expected:  RoutingKeyExpiring,
		},
		{
			name:      "unknown fallback to waste",
			eventType: "unknown.event",
			expected:  RoutingKeyWaste,
		},
		{
			name:      "invalid json fallback to waste",
			eventType: "",
			expected:  RoutingKeyWaste,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			if tt.eventType != "" {
				body, _ = json.Marshal(map[string]string{"type": tt.eventType})
			} else {
				body = []byte("not-json")
			}
			actual := ResolveRoutingKey(body)
			if actual != tt.expected {
				t.Fatalf("expected routing key %q, got %q", tt.expected, actual)
			}
		})
	}
}
