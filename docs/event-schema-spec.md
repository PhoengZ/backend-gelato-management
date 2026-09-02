# GelatoFlow Event Schema Specification

This document defines the standardized event schema for all asynchronous messages published to RabbitMQ across the GelatoFlow microservices architecture.

## 1. Global Standards & Field Justifications

To ensure high interoperability, scalability, and observability, GelatoFlow adopts a standardized event envelope inspired by the **CloudEvents (CNCF)** specification and the **W3C Trace Context** standard.

Every event published to the broker MUST contain the following mandatory fields:

| Field | Description | Global Standard Justification |
| :--- | :--- | :--- |
| `id` | A unique identifier for the event (e.g., UUIDv4). | **CloudEvents (`id`):** Ensures idempotency. Consumers use this to detect and discard duplicate messages, preventing unintended side effects (e.g., processing an order twice). |
| `source` | A URI-reference identifying the context/service where the event originated (e.g., `gelatoflow/order-service`). | **CloudEvents (`source`):** Allows consumers to confidently identify the producer. Essential for routing, filtering, and debugging system-wide event flows. |
| `type` | The exact event name/type (e.g., `OrderPlaced.v1`). | **CloudEvents (`type`):** Enables schema evolution and evaluation. Consumers use this to determine how to deserialize the payload (`data`) and route the event to the correct internal handler. |
| `time` | A Timestamp in RFC 3339 / ISO 8601 format indicating when the event occurred (e.g., `2026-09-02T14:30:00Z`). | **CloudEvents (`time`):** Critical for establishing strict chronological order, debugging race conditions, and determining metrics like event propagation latency. |
| `traceparent` | The W3C Trace Context identifier tied to the initial API Gateway request (e.g., `00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01`). | **W3C Trace Context:** Essential for **Distributed Tracing**. By passing this ID across API boundaries, gRPC calls, and async events, we can visualize the entire lifecycle of a request in tools like Jaeger, Datadog, or OpenTelemetry. |
| `data` | The domain-specific payload of the event. Can be a "Fat" or "Thin" payload. | **CloudEvents (`data`):** Encapsulates the actual business information. Separating the envelope from the `data` allows infrastructure to process the message without understanding the business logic. |

## 2. Base Schema Envelope

```json
{
  "id": "uuid-v4",
  "source": "gelatoflow/<service-name>",
  "type": "<EventName>.v<Version>",
  "time": "YYYY-MM-DDThh:mm:ss.sssZ",
  "traceparent": "00-<trace-id>-<parent-id>-<trace-flags>",
  "data": {}
}
```

## 3. Event Catalog

### 3.1 OrderPlaced (Fat Event)
* **Publisher:** `Order Service`
* **Consumers:** `Fulfillment Service`, `Notification Service`, `Analytics Service`
* **Strategy (Fat Event):** We use a fat payload here so the Notification Service and Fulfillment Service have all necessary customer and order details without needing to query the Order Service, preventing a "thundering herd" of synchronous callbacks.

```json
{
  "id": "c3f8510a-6c17-48b2-b1ef-24901b0f5556",
  "source": "gelatoflow/order-service",
  "type": "OrderPlaced.v1",
  "time": "2026-09-02T14:30:00Z",
  "traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
  "data": {
    "orderId": "ORD-12345",
    "totalAmount": 12.50,
    "items": [
      {
        "flavorId": "FLV-PISTACHIO",
        "flavorName": "Sicilian Pistachio",
        "portions": 2,
        "unitPrice": 6.25,
        "subtotal": 12.50
      }
    ]
  }
}
```

### 3.2 OrderCancelled (Thin Event)
* **Publisher:** `Order Service`
* **Consumers:** `Fulfillment Service`, `Notification Service`, `Analytics Service`
* **Strategy (Thin Event / Local State Materialization):** A minimal payload is deliberately used to reduce network overhead. 
  * **How it prevents gRPC Callbacks:** To prevent the Analytics Service from needing to make synchronous gRPC calls back to the Order Service to fetch the `totalAmount` or `items` (which would tightly couple the services and cause load), we utilize the **Event Sourcing / Local State Materialization** pattern. 
  * **Implementation Feasibility:** The Analytics Service must consume the fat `OrderPlaced` event first and store those granular order details in its own local MongoDB. When this thin `OrderCancelled` event arrives, the Analytics Service simply queries its *own* local database using the `orderId` to retrieve the associated revenue and items, and then applies the negative offset to the analytics metrics.

```json
{
  "id": "d4a7812b-7d28-49c3-c2fa-35812c1g6667",
  "source": "gelatoflow/order-service",
  "type": "OrderCancelled.v1",
  "time": "2026-09-02T14:35:00Z",
  "traceparent": "00-1bg8762027de54ee9559fc322d91420d-c8be7c8270314442-01",
  "data": {
    "orderId": "ORD-12345",
    "reason": "PAYMENT_TIMEOUT"
  }
}
```

### 3.3 OrderReady (Thin Event)
* **Publisher:** `Fulfillment Service`
* **Consumers:** `Notification Service`
* **Strategy (Thin Event):** Only the specific identifiers are needed to trigger the notification template.

```json
{
  "id": "e5b8923c-8e39-50d4-d3gb-46923d2h7778",
  "source": "gelatoflow/fulfillment-service",
  "type": "OrderReady.v1",
  "time": "2026-09-02T14:05:00Z",
  "traceparent": "00-2ch9873138ef65ff0660gd433e02531e-d9cf8d9381425553-01",
  "data": {
    "orderId": "ORD-12345",
    "queueNumber": "01",
    "timeSlotId": "TS-004"
  }
}
```

### 3.4 OrderPickedUp (Thin Event)
* **Publisher:** `Fulfillment Service`
* **Consumers:** `Analytics Service`
* **Strategy (Thin Event):** Simple state transition marker for data warehousing.

```json
{
  "id": "f6c9034d-9f40-61e5-e4hc-57034e3i8889",
  "source": "gelatoflow/fulfillment-service",
  "type": "OrderPickedUp.v1",
  "time": "2026-09-02T14:10:00Z",
  "traceparent": "00-3di0984249fg76gg1771he544f13642f-ead09ea492536664-01",
  "data": {
    "orderId": "ORD-12345"
  }
}
```

### 3.5 BatchLowStock (Fat Event)
* **Publisher:** `Batch Inventory Service`
* **Consumers:** `Notification Service`
* **Strategy (Fat Event):** Requires context about the flavor, current portions, and threshold so the notification has all the necessary information without querying the catalog/inventory again.

```json
{
  "id": "a1b2c3d4-e5f6-7890-abcd-1234567890ab",
  "source": "gelatoflow/batch-inventory-service",
  "type": "BatchLowStock.v1",
  "time": "2026-09-02T10:00:00Z",
  "traceparent": "00-4ej1095350gh87hh2882if655g24753g-fbe10fb503647775-01",
  "data": {
    "batchId": "BAT-PIST-0902",
    "flavorId": "FLV-PISTACHIO",
    "flavorName": "Sicilian Pistachio",
    "remainingPortions": 5,
    "threshold": 10
  }
}
```

### 3.6 BatchExpiring (Fat Event)
* **Publisher:** `Batch Inventory Service`
* **Consumers:** `Notification Service`
* **Strategy (Fat Event):** Needs specific details about the batch and its expiration time.

```json
{
  "id": "b2c3d4e5-f6a7-8901-bcde-2345678901bc",
  "source": "gelatoflow/batch-inventory-service",
  "type": "BatchExpiring.v1",
  "time": "2026-09-02T22:00:00Z",
  "traceparent": "00-5fk2106461hi98ii3993jg766h35864h-gcf21gc614758886-01",
  "data": {
    "batchId": "BAT-STRAW-0901",
    "flavorId": "FLV-STRAWBERRY",
    "flavorName": "Fresh Strawberry",
    "expiryDate": "2026-09-02T23:59:59Z"
  }
}
```

### 3.7 WasteRecorded (Fat Event)
* **Publisher:** `Batch Inventory Service`
* **Consumers:** `Analytics Service`
* **Strategy (Fat Event):** Carries the cost implication and reason for waste to allow the Analytics Service to build detailed reports directly from the event data.

```json
{
  "id": "c3d4e5f6-a7b8-9012-cdef-3456789012cd",
  "source": "gelatoflow/batch-inventory-service",
  "type": "WasteRecorded.v1",
  "time": "2026-09-02T23:45:00Z",
  "traceparent": "00-6gl3217572ij09jj4004kh877i46975i-hdg32hd725869997-01",
  "data": {
    "batchId": "BAT-STRAW-0901",
    "flavorId": "FLV-STRAWBERRY",
    "portionsWasted": 12,
    "reason": "EXPIRED",
    "recordedBy": "STAFF-002"
  }
}
```
