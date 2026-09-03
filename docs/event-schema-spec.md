# GelatoFlow Event Schema Specification

This document defines the canonical envelope, payload conventions, and ownership
rules for asynchronous messages published through RabbitMQ. JSON Schemas under
`contracts/events/` are the machine-readable source of truth.

## 1. CloudEvents envelope

GelatoFlow uses CloudEvents 1.0 structured JSON. Every event must contain:

| Field | Requirement |
| --- | --- |
| `specversion` | Always `1.0` |
| `id` | UUID identifying this event; consumers use it for deduplication |
| `source` | URI-reference for the producing service |
| `type` | Versioned event type such as `com.gelatoflow.order.placed.v1` |
| `time` | RFC 3339 UTC timestamp for the domain event |
| `datacontenttype` | Always `application/json` in version 1 |
| `data` | Domain payload owned by the producer |
| `traceparent` | Optional W3C trace context when an inbound trace exists |

`traceparent` is optional because events can originate from scheduled expiry or
maintenance jobs without an inbound HTTP request. A producer must propagate a
valid trace context when one exists and must not invent an invalid placeholder.

```json
{
  "specversion": "1.0",
  "id": "c3f8510a-6c17-48b2-b1ef-24901b0f5556",
  "source": "/gelatoflow/order-service",
  "type": "com.gelatoflow.order.placed.v1",
  "time": "2026-09-02T14:30:00Z",
  "datacontenttype": "application/json",
  "traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
  "data": {}
}
```

## 2. Payload conventions

- JSON field names use `snake_case`.
- Resource IDs use UUID strings; prefixed IDs such as `ORD-12345` are not part of
  the backend contract.
- Timestamps use RFC 3339 UTC and calendar dates use `YYYY-MM-DD`.
- Monetary values are integers in minor units with an ISO 4217 currency. For
  example, THB 12.50 is `1250` satang, represented as
  `total_amount_minor: 1250` and `currency: "THB"`.
- A money-bearing payload must not use JSON decimal fields such as `total_amount`,
  `unit_price`, `subtotal`, or `cost_lost`.
- Producers publish through a transactional outbox after the domain transaction
  commits. Delivery is at least once, so each consumer records `id` and processes
  a duplicate event as a no-op.
- Consumers ignore unknown envelope and payload fields within a compatible event
  version. A removed field or changed meaning requires a new event version.

## 3. Event catalog

| Event type | Routing key | Publisher | Consumers | Payload strategy |
| --- | --- | --- | --- | --- |
| `com.gelatoflow.order.placed.v1` | `order.placed` | Order | Fulfillment, Notification, Analytics | Fat sale snapshot |
| `com.gelatoflow.order.cancelled.v1` | `order.cancelled` | Order | Fulfillment, Notification, Analytics | Thin follow-up |
| `com.gelatoflow.fulfillment.order-ready.v1` | `fulfillment.order_ready` | Fulfillment | Notification | Thin follow-up |
| `com.gelatoflow.fulfillment.order-picked-up.v1` | `fulfillment.order_picked_up` | Fulfillment | None in MVP | Thin follow-up |
| `com.gelatoflow.inventory.batch-low-stock.v1` | `inventory.low_stock` | Batch Inventory | Notification | Inventory snapshot |
| `com.gelatoflow.inventory.batch-expiring.v1` | `inventory.expiring` | Batch Inventory | Notification | Inventory snapshot |
| `com.gelatoflow.inventory.waste-recorded.v1` | `inventory.waste` | Batch Inventory | Analytics | Fat waste snapshot |

Only the owning service publishes its domain events. RabbitMQ credentials should
limit Order, Fulfillment, and Batch Inventory to their own exchanges. A consumer
must not publish a replacement event using another service's event type.

### 3.1 OrderPlaced

`OrderPlaced` is emitted only after payment succeeds and the Inventory
reservation is confirmed. Analytics can therefore count this event as a paid
sale. Its item names and prices are immutable order-line snapshots, not live
Catalog fields.

```json
{
  "specversion": "1.0",
  "id": "c3f8510a-6c17-48b2-b1ef-24901b0f5556",
  "source": "/gelatoflow/order-service",
  "type": "com.gelatoflow.order.placed.v1",
  "time": "2026-09-02T14:30:00Z",
  "datacontenttype": "application/json",
  "data": {
    "order_id": "8d29d6cf-bd4d-48ca-9e4f-dcd8ec54e594",
    "customer_id": "8dc81c31-a354-4774-b835-36ea0411bc54",
    "status": "PAID",
    "pickup_at": "2026-09-02T15:00:00Z",
    "total_amount_minor": 1250,
    "currency": "THB",
    "items": [
      {
        "flavor_id": "0f3bca11-1eb2-4a86-908a-e60d7656c79b",
        "flavor_name": "Sicilian Pistachio",
        "portions": 2,
        "unit_price_minor": 625,
        "subtotal_minor": 1250
      }
    ]
  }
}
```

The sum of item `subtotal_minor` values must equal `total_amount_minor`. All
items in version 1 use the same event-level currency.

### 3.2 OrderCancelled

The payload contains `order_id` and a stable `reason` code. Services that need
the paid amount or line items keep an event-fed local projection created from
`OrderPlaced` and look up the order there. They do not call Order synchronously
while handling the event.

This is local state materialization, not full event sourcing: the Order database
remains the operational source of truth.

### 3.3 Fulfillment events

- `OrderReady` contains `order_id`, `queue_number`, and `pickup_slot_id`.
- `OrderPickedUp` contains `order_id` and `picked_up_at`.

Both use UUIDs for resource references. Queue numbers remain display strings.

### 3.4 Inventory notification events

- `BatchLowStock` contains `batch_id`, `flavor_id`, `remaining_portions`, and
  `threshold_portions`.
- `BatchExpiring` contains `batch_id`, `flavor_id`, and `expires_at`.

Batch Inventory publishes only identifiers and inventory facts it owns. It does
not copy a live `flavor_name`, price, recipe, or allergen list from Catalog.

### 3.5 WasteRecorded

`WasteRecorded` contains the immutable cost impact calculated from the batch's
unit cost. The value is stored and aggregated as `cost_lost_minor`, never as a
floating-point amount.

```json
{
  "specversion": "1.0",
  "id": "b76c52fd-3373-49da-9283-85456ba3c53c",
  "source": "/gelatoflow/batch-inventory-service",
  "type": "com.gelatoflow.inventory.waste-recorded.v1",
  "time": "2026-08-31T10:15:00Z",
  "datacontenttype": "application/json",
  "data": {
    "waste_id": "03db6f89-369a-4cd1-a04f-830330f99f97",
    "batch_id": "36d9f415-d169-447b-9f81-37075fb19cdd",
    "flavor_id": "0f3bca11-1eb2-4a86-908a-e60d7656c79b",
    "date": "2026-08-31",
    "portions": 3,
    "reason": "EXPIRED",
    "cost_lost_minor": 7500,
    "currency": "THB"
  }
}
```

## 4. Analytics projection rules

Analytics stores sales and lost-cost aggregates as integer minor units, for
example `gross_sales_minor`, `average_order_value_minor`, and
`cost_lost_minor`. Decimal display values are derived only at the API or UI
boundary.

The current Analytics prototype still consumes legacy flat `order.success` and
`inventory.waste` payloads containing floating-point fields. It must add the
CloudEvents envelope, event-ID deduplication, integer storage, and the
`order.placed` binding before the canonical contracts are enabled in production.
That consumer migration is intentionally outside the Contracts and
Infrastructure pull request.

## 5. Ordering and failure handling

- Consumers cannot assume cross-exchange global ordering.
- A consumer that receives a thin follow-up before its corresponding fat event
  should retry with bounded backoff or park the message in a dead-letter queue.
- Poison messages are rejected without infinite requeue loops.
- Processed event IDs are retained long enough to cover the broker's redelivery
  and retry window.
