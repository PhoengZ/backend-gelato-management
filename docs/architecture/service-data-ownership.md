# Service Data Ownership

GelatoFlow follows a database-per-service model. A service may expose data through
an API, gRPC method, or event, but another service must not read or write its
database directly.

The backend contracts in this repository are the source of truth. The API Gateway
or a frontend-facing adapter may compose responses for the demo UI without moving
ownership of the underlying data.

## Ownership matrix

| Service | Owned data | Storage | Does not own |
| --- | --- | --- | --- |
| Auth Service | Users, password hashes, roles, account status | PostgreSQL | Orders, flavor metadata, stock |
| Catalog Service | Flavor names, descriptions, prices, images, recipes, allergens, active status | Redis | Available portions, batches, reservations |
| Batch Inventory Service | Batches, portion balances, reservations, waste, inventory movements | PostgreSQL | Flavor descriptions and prices, orders, payments |
| Order Service | Orders, immutable checkout prices, reservation references, Checkout Session binding, order outbox | PostgreSQL | Current Catalog metadata, stock balances, Stripe credentials |
| Payment Service | Stripe integration and signature verification; Checkout result reporting implemented, durable reconciliation/refunds pending | None (stateless Checkout and legacy PaymentIntent adapters) | Order totals and status, stock balances |
| Fulfillment Service | Kitchen tickets, pickup time-slots, slot queue numbers, fulfillment status | PostgreSQL | Orders, inventory batches, payment processing |
| Analytics Service | Derived daily sales and waste aggregates in integer minor units | MongoDB | Operational source records |
| Notification Service | Nothing durable. Live SSE connections and, in memory only, a TTL-bounded `order_id` to `customer_id` routing map and recent event ids | None (stateless) | Orders, customer profiles, inventory batches |

## Notification routing exception

`order.cancelled` and `fulfillment.order_ready` carry no `customer_id`, and Order
Service's gRPC `GetOrderDetails` is not yet available to call. Notification
therefore remembers `order_id` to `customer_id` from `order.placed` in process
memory (identifiers only, never persisted, bounded and expiring). This is a
deliberate, temporary exception to rule 2 below. An order placed before the
process started, or older than `ORDER_OWNER_TTL`, produces no notification. Replace
the map with a `GetOrderDetails` call, or add `customer_id` to the thin events,
once Order Service owner agrees.

## Boundary rules

1. Direct database access and cross-service table replication are strictly prohibited.
   Services must not create shadow tables or duplicate collections in their own
   database to store current entities owned by another service. Services may
   persist the minimum immutable facts required to represent a completed business
   transaction. Order line items are such a snapshot: `flavor_id`, `flavor_name`,
   `unit_price_minor`, `subtotal_minor`, and currency as agreed at checkout. They
   do not replace or mirror the current Catalog flavor record.
2. When a service requires data owned by another service to fulfill a workflow or
   handle an event, it must query the authoritative service synchronously via gRPC
   instead of replicating data locally.
3. Catalog responses never contain an authoritative `available_portions` value.
4. Batch Inventory references a flavor by `flavor_id`; it does not duplicate the
   flavor name, price, recipe, or allergen list.
5. Order Service uses Batch Inventory gRPC methods to check and reserve portions.
   It must not query inventory tables.
6. Batch Inventory is the only service allowed to change available, reserved,
   sold, or wasted portion balances.
7. Analytics consumes versioned events and never participates in or blocks an
   operational checkout or inventory transaction. When Analytics requires order
   details to handle an `order.cancelled` thin event, it queries Order Service via
   unary gRPC `GetOrderDetails`. When Analytics requires line items for analytical
   reporting and aggregation, it queries Order Service via client-streaming gRPC
   `StreamOrderItems` (streaming order IDs to retrieve item breakdowns) rather than
   creating replicated order tables in its MongoDB database.
   These are contracted integration targets; Order does not yet implement
   either gRPC read method.
8. API Gateway may return a composed flavor view containing Catalog metadata and
   Inventory availability, but that view is not a new data owner.
9. Order orchestrates frontend checkout by calling Payment's versioned gRPC API
   after persisting the pending order. Payment creates the provider session from
   Order's immutable total; Order persists its binding before returning the URL.
   Payment's inbound Checkout gRPC and Stripe adapter are implemented, and verified
   Checkout result reporting to Order callbacks is implemented. See [checkout architecture](order-checkout.md).
10. Cancellation releases stock through Inventory and changes Order state locally.
    It does not currently close the Stripe session. Payment reconciliation/refund
    and later fulfillment transitions remain separate integration responsibilities.

## Shared conventions

In the selected flow, Order sends its immutable checkout facts to Payment's gRPC
server and durably binds the returned Checkout Session before exposing its URL.
Verified provider results go back to Order's authenticated HTTP callbacks
through Payment's implemented Checkout webhook adapter. Order owns payment state,
Inventory orchestration and outbox publication.

The retained PaymentIntent REST/webhook path instead retrieves Order details,
binds a provider intent and reports results through proposed Order gRPC methods.
Order does not implement those RPCs. See [Payment integration](payment-integration.md)
for the legacy dependency, migration and durable reconciliation work.

### Catalog transport status

Catalog exposes REST and `catalog.v1.CatalogService` gRPC through the same
application service and Redis repository. Neither transport exposes authoritative
stock. Read projections omit recipe; privileged writes and archive visibility
follow the Catalog JWT policy in both transports.

Inventory uses GetFlavor before creating a batch, without persisting Catalog
metadata. Order uses BatchGetFlavors to verify active status and obtain the
checkout price. It persists flavor name and integer minor-unit price as immutable
order transaction facts; this is not a copy of the mutable Catalog entity.

See [ADR-006](adr-006-catalog-rest-and-grpc.md), proposed for team review.

### Interface conventions

- Public REST APIs use `/api/v1`.
- Internal protobuf packages use a versioned namespace such as `inventory.v1`.
- Resource identifiers are UUID strings.
- Timestamps use RFC 3339 in UTC.
- Calendar dates use `YYYY-MM-DD`.
- Monetary values in operational APIs use integer minor units with an explicit
  ISO 4217 currency code.
- Monetary values in events and Analytics persistence also use integer minor
  units. Decimal display values are derived only at an API or UI boundary.
- Error responses use a stable machine-readable code, a human-readable message,
  and an optional field-level details map.

## Frontend compatibility

The current frontend is a demo and may keep its mock routes temporarily. When it
is integrated, its composed `Flavor` model should be built from:

- Catalog `Flavor`, which supplies metadata and price.
- Inventory `FlavorAvailability`, which supplies available portions.

This prevents demo-specific fields from becoming backend service ownership.
