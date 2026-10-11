# GelatoFlow architecture reference

This reference separates current Order behavior from system integration targets.
Versioned [contracts](../contracts/README.md) are authoritative.
[API_SPEC.md](API_SPEC.md) retains legacy demo examples and migration notes.

## Service responsibilities

| Service | Owned responsibility | Storage |
| --- | --- | --- |
| API Gateway | Routing, external JWT enforcement and rate limiting | None |
| Auth | Identity, credentials, roles and JWT issuance | PostgreSQL |
| Catalog | Metadata, prices, active status, recipes and allergens | Redis |
| Order | Orders, purchase snapshots, pickup_at, reservation references, session binding and outbox | PostgreSQL |
| Batch Inventory | Batches, availability, reservations, sold portions, expiry and waste | PostgreSQL |
| Payment | Stripe APIs, verified webhooks and provider reconciliation | No database in existing PaymentIntent adapter; Checkout pending |
| Fulfillment | Target preparation queues, pickup slots and collection | Owned PostgreSQL |
| Notification | Target customer and operational notifications | Service-owned design |
| Analytics | Derived sales and waste projections | MongoDB |

Services never read another service's storage. Order line items are immutable
purchase facts, not replicas of live Catalog records. Gateway can compose
metadata and availability without taking ownership of either.

## Current Order checkout

1. Frontend submits POST /api/v1/orders through Gateway with an Auth JWT, UUID
   Idempotency-Key, future pickup_at and unique flavor/portion items.
2. Gateway must validate JWTs and replace caller-supplied identity headers.
   Order trusts X-User-ID from that protected boundary.
3. Order checks customer/key and request hash. A matching retry resumes the
   existing order; changed data with the same key returns 409.
4. Order calls Catalog BatchGetFlavors, checks active flavors and THB prices,
   calculates minor-unit totals and snapshots names/prices.
5. Order calls Inventory ReservePortions. Inventory atomically allocates FEFO
   stock; insufficient portions reject the whole reservation.
6. Order persists the reservation ID, deadline, order and items in its own
   database as PENDING_PAYMENT before contacting Payment.
7. Order calls Payment CreateCheckoutSession with authoritative totals and a
   deterministic key. Its client is implemented; Payment's server and Stripe
   Checkout adapter must still be added.
8. Order validates the HTTPS URL and atomically binds one session to a pending,
   unexpired order before returning checkout_url.
9. Frontend redirects to Stripe. A verified Payment callback makes Order claim
   PAYMENT_CONFIRMING, confirm Inventory, then commit PAID and order.placed in one
   database transaction.
10. The outbox worker publishes asynchronously. After browser return, frontend
    reads authenticated GET /api/v1/orders/{id} for the current state.

Frontend does not make a second Payment API call in this flow. The existing
PaymentIntent/client_secret branch is a separate migration dependency, not an
interchangeable Checkout Session server. Checkout accepts totals between 1 and
99,999,999 THB minor units.

## Communication and implementation status

| Boundary | Transport | Current status |
| --- | --- | --- |
| Frontend to Order | Versioned REST through Gateway | Order endpoints implemented; trusted Gateway integration needs verification |
| Order to Catalog | BatchGetFlavors gRPC | Client and Catalog server implemented |
| Inventory to Catalog | GetFlavor gRPC | Batch validation implemented |
| Order to Inventory | Reserve/Confirm/Release gRPC | Client and server implemented; full checkout integration pending |
| Order to Payment | CreateCheckoutSession gRPC | Contract/client implemented; Payment server pending |
| Payment to Order | Token-authenticated HTTP callbacks | Order handlers implemented; Checkout webhook adapter pending |
| Order to RabbitMQ | CloudEvents through transactional outbox | Implemented; acknowledgement/reconnect findings open |
| Analytics/downstream to Order | Order read gRPC | Order server pending |

Order has a CheckAvailability client method, but checkout calls ReservePortions
directly: an availability read cannot guarantee a later reservation.
Local unit tests do not prove deployed end-to-end integration.

## Failure, cancellation and expiry

- Payment timeout/unavailability retains the pending order and bounded stock
  hold. The error includes details.order_id; retry the original body and key.
  Payment must return the same session for that checkout key.
- If order persistence fails after reserve, Order reconciles a possible committed
  result and otherwise leaves the hold to Inventory expiry. It does not release
  a reservation that a concurrent same-key request may still own.
- Customer cancellation checks ownership; payment failure uses a trusted service
  callback. Both first claim CANCEL_PENDING, blocking checkout and competing
  confirmation. They release Inventory using the existing key and ORDER_CANCELLED
  reason, then commit CANCELLED and cancellation outbox locally. A lifecycle
  worker resumes cancellation/confirmation intents after failure or restart.
- Release returns reserved portions to available while the batch is valid;
  expired-batch portions become waste. Stable keys prevent repeated stock changes.
- Inventory expires abandoned reservations independently. Order has no expiry
  worker, so a pending order may outlive its reservation.
- Cancellation does not currently expire a Stripe session. A later charge after
  cancellation/expiry must be reconciled/refunded and cannot revive stock.

## Fulfillment and downstream targets

The target journey adds pickup-slot selection, queue allocation, preparation,
ready notifications and collection. These are not current Order checkout fields:
it accepts pickup_at and returns no queue number or pickup-slot ID. PREPARING,
READY_FOR_PICKUP and COMPLETED exist in the model, but this branch has no handlers
that perform those transitions.

order.placed represents a paid sale after stock confirmation. Current
order.cancelled represents cancellation of a never-paid pending order and must
not automatically subtract a paid sale. Its schema and consumer semantics still
need owner review. Inventory implements inventory.waste; low-stock and expiring
notifications remain design targets. Analytics' Order gRPC dependency and consumer
compatibility/deduplication must be verified separately.

## Integrity and operational expectations

Inventory alone enforces initial = available + reserved + sold + wasted, FEFO,
non-negative balances and all-or-nothing reservations. One remaining portion
under twenty competing requests must yield one successful reservation.
Order's status/outbox transaction does not form a distributed transaction with
Inventory or Stripe; stable keys and reconciliation bridge those steps.

Order startup requires PostgreSQL and RabbitMQ. gRPC clients connect lazily.
/health reports liveness, not dependency readiness. Checkout has synchronous
dependencies and a configurable Payment deadline (default five seconds); latency
objectives require load measurement.

See [architecture diagrams](architecture-diagram.md),
[sequence diagrams](sequence-diagram.md), [checkout boundary](architecture/order-checkout.md),
[reservation lifecycle](architecture/inventory-reservation.md),
[authentication](architecture/auth-and-jwt.md) and [events](event-schema-spec.md).
ADR-006 and ADR-007 preserve their original proposal context; current integration
status is recorded here and in service READMEs.
