# Payment integration (proposed boundary for owner review)

Payment is a stateless Java 21 / Spring Boot Stripe adapter. It has no database,
Inventory client, or RabbitMQ publisher. Order owns authoritative totals, payment
status, the provider intent binding and the workflow that confirms/releases stock.
The canonical public contract is `contracts/openapi/payment-service.v1.yaml`.
The additive Order RPC changes are in `contracts/proto/order/v1/order.proto`.
These shared interfaces require review by the Order and Gateway owners before
shared deployment. Their definitions do not implement Order's workflow.

## Checkout

1. Payment verifies the original HS256 Auth JWT (`sub`, `role`, issuer, audience,
   issued-at and expiry) and requires CUSTOMER. Forwarded identity headers are
   not trusted. Only `order_id` is accepted; the client cannot choose the charge.
2. Authenticated `GetOrderDetails` retrieves the authoritative order. Payment
   requires matching customer ownership, PENDING_PAYMENT, a positive total of
   at most 99,999,999 minor units and a valid uppercase ISO currency.
3. An existing `payment_intent_id` is retrieved from Stripe. Otherwise Payment
   creates a card intent with Order's immutable total, order/customer UUID
   metadata, and deterministic key `gelatoflow-payment-v1-<order_id>`.
4. `BindPaymentIntent` must commit the exact intent, amount and currency before
   Payment exposes `client_secret`. Order atomically permits one intent per
   order, rejects a different binding, and revalidates status and total under
   lock. Identical replays succeed. A failed or ambiguous binding returns no
   secret; retry first reads Order's binding.

Stripe idempotency keys can be pruned after 24 hours. Permanent safety therefore
depends on Order's durable binding, not a local cache or the provider key alone.
A never-bound orphan intent has no client secret exposed by this service. If a
later retry creates a different orphan, Order still allows only one bound intent
to be returned. Payment does not automatically cancel ambiguous intents; manual
provider reconciliation may be needed for orphan cleanup.

Order must freeze checkout totals once payment begins; editing a payable order
requires a separately designed cancellation/new-order workflow. V1 does not
replace canceled or completed intents with another charge for the same order.

## Provider notifications

The public webhook verifies `Stripe-Signature` using the raw UTF-8 body, endpoint
secret and a 300-second timestamp tolerance. It checks configured test/live mode,
limits the body to 256 KiB, and handles success and failure notifications.
Payment retrieves the intent from Stripe, validates GelatoFlow metadata and mode,
and forwards the current amount, currency, provider intent ID, event ID and event
creation time to Order. Success requires `succeeded` with full `amount_received`.
Failure is reported only while the current intent is `requires_payment_method`;
a stale failure after processing/success is acknowledged without a state change.

Stripe delivery is at least once and may be out of order. Payment intentionally
has no in-memory deduplication cache. Every repeated report carries the same
`provider_event_id`; Order must durably deduplicate it. `ReportPaymentResult`
must validate the bound intent and totals, use monotonic state transitions,
confirm Inventory for success or release for terminal failure, and write its
canonical events with a transactional outbox. It must not mark an event accepted
before the operational result or durable reconciliation work is committed.
Order must prevent an old failure from downgrading a paid order even if provider
state changes between Payment's retrieval and Order's processing.

Non-success from Order, including UNIMPLEMENTED, yields HTTP 503 so Stripe retries.
Provider reconciliation failures yield 502/503. Unsupported signed events and
superseded failures yield 200. Late success after release/expiry must become
durable Order reconciliation/compensation work; never confirm expired Inventory
stock or silently discard a charge. Refund/compensation automation is outside
this Payment v1 implementation and must be resolved by Order before deployment.

## Internal authentication and transport

Every Payment-to-Order RPC includes `authorization: Bearer <ORDER_SERVICE_TOKEN>`
and a bounded deadline (5 seconds by default). Order must recognize a dedicated
Payment machine identity and restrict Bind/Report to it. A customer or Manager
JWT must never authorize ReportPaymentResult. The deployment must use TLS;
plaintext is explicitly available only for isolated development. Align machine
identity deployment with the Inventory discussion in ADR-007.

Gateway already forwards `/api/v1/payments/*` to port 3007, preserving the path.
It must preserve the webhook bytes and signature, allow provider requests without
a user JWT, and enforce customer authentication/rate limits for checkout. Payment
also verifies JWTs locally. No Gateway code changes are made in this implementation.

## Demo migration and implementation status

The demo's `{amount: 250}` and `{clientSecret: ...}` examples are legacy shapes.
Use `{order_id: <UUID>}` and snake_case response fields with integer minor units.
THB 250.00 is 25,000 minor units. No compatibility alias accepts client totals.

Payment has application source, SDK/gRPC adapters, security, tests, a Dockerfile
and CI. Order's directory is currently empty: GetOrderDetails, durable binding,
result deduplication, Inventory orchestration and outbox must be implemented by
its owner. Full Stripe-to-Inventory checkout remains an integration dependency.

References: [Stripe idempotency](https://docs.stripe.com/api/idempotent_requests),
[Stripe signature verification](https://docs.stripe.com/webhooks/signature),
[Stripe Java SDK](https://github.com/stripe/stripe-java).
