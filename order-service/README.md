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
   Order then calls Payment `CreateCheckoutSession` through gRPC using its
   authoritative total and a deterministic checkout key. It persists the session
   binding before returning `checkout_url`; frontend redirects to that URL.
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

Configure `PAYMENT_GRPC_URL` (local example `127.0.0.1:50057`),
`PAYMENT_REQUEST_TOKEN` (Order-to-Payment machine identity) and
`PAYMENT_REQUEST_TIMEOUT` (default `5s`, at most `30s`). This request token is
separate from Inventory's token and the reverse `PAYMENT_SERVICE_TOKEN` callback.
Payment must implement `contracts/proto/payment/v1/payment.proto`. The existing
PaymentIntent implementation on `chelsea/payment-service` does not implement
this Checkout API; see [the boundary and migration guide](../docs/architecture/order-checkout.md).

The create response contains the existing order fields plus `checkout_url` for
an unexpired pending order. After a dependency timeout, retry the same body and
Idempotency-Key to resume checkout; the error includes `details.order_id`.
Payment failures leave the bounded stock hold for retry/Inventory expiry.
Order save failures also retain the hold to avoid releasing a concurrent retry's
stock. New checkout amounts are limited to 1–99,999,999 minor units. GET Order
does not expose the checkout URL. The create response is `Cache-Control: no-store`.

```json
{
  "order_id": "c19b830e-a733-41de-bf26-aeb123926e36",
  "status": "PENDING_PAYMENT",
  "checkout_url": "https://checkout.stripe.com/..."
}
```

This abbreviated example omits the unchanged order fields. Frontend redirects
with `window.location.assign(response.checkout_url)`, then reads GET Order after
returning from Stripe. The browser redirect is not proof of payment.

Order runs AutoMigrate at startup and requires PostgreSQL and RabbitMQ then.
gRPC clients connect lazily; Catalog, Inventory and Payment must be reachable
for checkout. Order runs a durable outbox dispatcher. RabbitMQ publication uses the durable
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
- Payment checkout: `contracts/proto/payment/v1/payment.proto` (canonical; Payment server pending)
- Regenerate the local clients with `scripts/generate-proto.sh` (Buf 1.73.0).
- `OrderPlaced`: `contracts/events/order-placed.v1.schema.json`
- Order gRPC read interface for Analytics remains a separate integration item;
  this HTTP implementation does not claim that client-streaming contract is
  implemented.
