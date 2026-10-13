# GelatoFlow architecture and checkout boundaries

This view follows the current Order implementation and canonical contracts.
Solid edges describe implemented interfaces; dashed edges describe integration
targets or boundaries whose counterpart is pending. Neither a contract nor a
diagram proves a deployed, verified end-to-end connection.

## System view

```mermaid
flowchart LR
    Customer["Customer Web"] --> Gateway["API Gateway"]
    Staff["Staff / Manager Dashboard"] --> Gateway
    Gateway --> Auth["Auth Service"]
    Gateway --> Catalog["Catalog Service"]
    Gateway --> Order["Order Service"]
    Gateway --> Inventory["Batch Inventory Service"]
    Gateway -. "Target: fulfillment API" .-> Fulfillment["Fulfillment Service"]
    Gateway --> Analytics["Analytics Service"]

    Auth --> AuthDB[("Auth PostgreSQL")]
    Catalog --> CatalogDB[("Catalog Redis")]
    Order --> OrderDB[("Order PostgreSQL: orders, items, session binding, outbox")]
    Inventory --> InventoryDB[("Inventory PostgreSQL")]
    Fulfillment --> FulfillmentDB[("Fulfillment PostgreSQL")]
    Analytics --> AnalyticsDB[("Analytics MongoDB projections")]

    Order -->|"gRPC: BatchGetFlavors"| Catalog
    Inventory -->|"gRPC: GetFlavor before batch creation"| Catalog
    Order -->|"gRPC: Reserve / Confirm / Release"| Inventory
    Order -->|"gRPC: CreateCheckoutSession"| Payment["Payment Service"]
    Payment -. "Target: Stripe Checkout Session" .-> Stripe["Stripe"]
    Customer -. "Target: redirect to checkout_url" .-> Stripe
    Stripe -. "Signed webhook through Gateway" .-> Gateway
    Gateway -. "Provider webhook route" .-> Payment
    Payment -->|"HTTP: payment-succeeded / payment-failed"| Order

    Order -->|"Outbox: order.placed / order.cancelled"| Rabbit["RabbitMQ"]
    Inventory -->|"Outbox: inventory.waste, publishing configurable"| Rabbit
    Rabbit -. "Consumer integration" .-> Fulfillment
    Rabbit -. "Consumer integration" .-> Notification["Notification Service"]
    Rabbit -. "Consumer compatibility and deduplication verification" .-> Analytics
    Analytics -. "StreamOrderItems: Order server pending" .-> Order
    Analytics -. "GetOrderDetails: Order server pending" .-> Order
    Fulfillment -. "GetOrderDetails: Order server pending" .-> Order
```

The selected frontend checkout calls Order only. The public PaymentIntent API
on `chelsea/payment-service` is a separate implementation to migrate; it is not
a second frontend call in this Checkout Session flow. Payment implements the
inbound gRPC API and connects verified webhook results to Order HTTP callbacks.

## Order internals

```mermaid
flowchart TD
    HTTP["Fiber HTTP: /api/v1/orders"] --> Handler["Handler: input, customer ownership, callback token"]
    Handler --> Logic["Application service: checkout and lifecycle"]
    Logic --> CatalogClient["Catalog gRPC client"]
    Logic --> InventoryClient["Inventory gRPC client"]
    Logic --> PaymentClient["Payment gRPC client: deadline and machine token"]
    Logic --> Repository["GORM repository"]
    Repository --> DB[("Owned PostgreSQL")]
    DB --> Worker["Outbox worker"]
    Worker --> Publisher["RabbitMQ publisher"]
    Publisher --> Exchange["order topic exchange"]
```

Order snapshots names and integer minor-unit prices at checkout. It owns no live
Catalog records or Inventory stock balances and never reads their stores. Gateway
must verify JWTs and replace untrusted identity headers before setting X-User-ID.

Order exposes HTTP only; its Analytics gRPC read contract is not yet a running
server. Pickup-slot allocation, queue numbers, later fulfillment transitions,
low-stock/expiring events remain integration/design work. Fulfillment's kitchen
tickets and per-slot queues and Notification's stateless SSE/consumer are
implemented; Gateway and broker runtime integration still need verification.
Notification keeps only bounded in-memory order-owner routing and event IDs,
with no database or replay after restart. See the service READMEs and the
temporary routing exception in service-data-ownership.md.
Order publisher acknowledgement matching and reconnect findings remain open.

See [sequence diagrams](sequence-diagram.md), [overview](overview-architecture.md),
[checkout boundary](architecture/order-checkout.md) and
[data ownership](architecture/service-data-ownership.md).
