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
| Analytics Service | Derived daily sales and waste aggregates | MongoDB | Operational source records |

## Boundary rules

1. Catalog responses never contain an authoritative `available_portions` value.
2. Batch Inventory references a flavor by `flavor_id`; it does not duplicate the
   flavor name, price, recipe, or allergen list.
3. Order Service uses Batch Inventory gRPC methods to check and reserve portions.
   It must not query inventory tables.
4. Batch Inventory is the only service allowed to change available, reserved,
   sold, or wasted portion balances.
5. Analytics consumes versioned events and never participates in a synchronous
   order or inventory transaction.
6. API Gateway may return a composed flavor view containing Catalog metadata and
   Inventory availability, but that view is not a new data owner.

## Shared conventions

- Public REST APIs use `/api/v1`.
- Internal protobuf packages use a versioned namespace such as `inventory.v1`.
- Resource identifiers are UUID strings.
- Timestamps use RFC 3339 in UTC.
- Calendar dates use `YYYY-MM-DD`.
- Monetary values in operational APIs use integer minor units with an explicit
  ISO 4217 currency code.
- Error responses use a stable machine-readable code, a human-readable message,
  and an optional field-level details map.

## Frontend compatibility

The current frontend is a demo and may keep its mock routes temporarily. When it
is integrated, its composed `Flavor` model should be built from:

- Catalog `Flavor`, which supplies metadata and price.
- Inventory `FlavorAvailability`, which supplies available portions.

This prevents demo-specific fields from becoming backend service ownership.
