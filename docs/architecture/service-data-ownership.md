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
| Analytics Service | Derived daily sales and waste aggregates in integer minor units | MongoDB | Operational source records |

## Boundary rules

1. Direct database access and cross-service table replication are strictly prohibited.
   Services must not create shadow tables or duplicate collections in their own
   database to store copies of data owned by another service. Only foreign
   identifiers (such as `flavor_id`, `order_id`, `user_id`) may be persisted.
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
   operational checkout or inventory transaction. When Analytics requires line
   items or order details for analytics reporting and aggregation, it queries
   Order Service via client-streaming gRPC (streaming order IDs to retrieve item
   breakdowns) rather than creating replicated order tables in its MongoDB database.
8. API Gateway may return a composed flavor view containing Catalog metadata and
   Inventory availability, but that view is not a new data owner.

## Shared conventions

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
