# Order Service

Order owns order records and purchase-time line item snapshots. It retrieves
current flavor names and prices from Catalog, reserves stock through the
versioned Inventory gRPC contract, and never accesses Inventory storage.

## Create and payment flow

1. `POST /api/v1/orders` requires `Idempotency-Key` (UUID), `X-User-ID` (the
   authenticated customer's UUID supplied by the trusted Gateway), pickup time,
   and flavor/portion items. Catalog supplies the saleable flavor and price.
2. Order creates a stable order UUID from customer plus idempotency key, then
   calls Inventory `ReservePortions` with the same key and persists the returned
   reservation ID and expiry. Inventory's expiry worker bounds abandoned holds.
3. The Payment Service calls `POST /api/v1/orders/{id}/payment-succeeded` with
   `X-Payment-Service-Token`. Order confirms the reservation and commits `PAID`
   plus an `OrderPlaced` CloudEvent outbox record in one PostgreSQL transaction.
4. A verified Payment Service failure calls `POST /api/v1/orders/{id}/payment-failed`,
   while customer cancellation uses `POST /api/v1/orders/{id}/cancel`. Both release
   the reservation before committing cancellation and its `order.cancelled` outbox
   event, and reuse stable Inventory idempotency keys on retries.

The API returns structured `{ "code", "message" }` errors. Insufficient stock,
expired reservations, invalid lifecycle transitions and idempotency conflicts
map to HTTP 409; invalid input maps to 400 and dependency timeout/unavailability
remain distinct from business errors.

## Run locally

Copy `.env.example` to `.env`, set secrets that match Inventory and Payment,
start PostgreSQL, RabbitMQ, Catalog and Inventory, then run:

```powershell
go run ./cmd/server
```

Order runs AutoMigrate at startup, requires all dependencies to be reachable,
and runs a durable outbox dispatcher. RabbitMQ publication uses the durable
`order` topic exchange, persistent messages, mandatory routing and publisher
confirms. Outbox delivery is at-least-once; consumers deduplicate CloudEvents by
`id`.

## Integration requirements

- The Gateway must verify JWT signature, issuer, audience and expiry, strip
  client-supplied identity headers, then set `X-User-ID` from the verified `sub`.
  Direct public access to Order must be blocked until that Gateway behavior is
  deployed. Catalog receives the original Bearer token when present.
- Configure Catalog gRPC and Inventory gRPC for isolated local networking. The
  current clients use plaintext gRPC for development; deployment needs agreed
  TLS/network controls and Auth-issued machine identity.
- If payment succeeds after Inventory expiry, Confirm returns a conflict. The
  Payment integration must refund/reconcile that payment; it must not mark the
  order paid or emit `OrderPlaced`.
- Order line items preserve flavor name and unit price as immutable transaction
  facts. This is a purchase snapshot, not a replicated current Catalog record;
  service ownership documentation should make that exception explicit.

## Contracts

- Inventory calls: `contracts/proto/inventory/v1/inventory.proto` (canonical)
- Catalog lookup: `contracts/proto/catalog/v1/catalog.proto` (canonical)
- Regenerate the local clients with `scripts/generate-proto.sh` (Buf 1.73.0).
- `OrderPlaced`: `contracts/events/order-placed.v1.schema.json`
- Order gRPC read interface for Analytics remains a separate integration item;
  this HTTP implementation does not claim that client-streaming contract is
  implemented.
