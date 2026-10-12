# GelatoFlow Contracts

This directory contains the versioned interfaces shared between GelatoFlow
services. Contract changes are reviewed before service or frontend implementation
changes.

## Contract types

- `openapi/`: REST APIs exposed through API Gateway.
- `proto/`: Internal synchronous service-to-service APIs.
- `events/`: Asynchronous RabbitMQ message schemas.
- `examples/`: Review and validation fixtures.

## Ownership

| Contract | Producer or owner | Primary consumers |
| --- | --- | --- |
| Auth REST API | Auth Service | API Gateway, frontend |
| Catalog REST API | Catalog Service | API Gateway, frontend |
| Catalog gRPC API | Catalog Service | Direct clients, Inventory batch validation, Order checkout |
| Inventory REST API | Batch Inventory Service | API Gateway, staff and manager UI |
| Fulfillment REST API | Fulfillment Service | API Gateway, staff UI |
| Inventory gRPC API | Batch Inventory Service | Order Service |
| Notification SSE API | Notification Service | Frontend (Gateway route pending) |
| Order REST API | Order Service | API Gateway, frontend, Payment Service |
| Payment checkout gRPC API | Payment Service | Order Service |
| Payment REST API (legacy PaymentIntent path) | Payment Service | API Gateway, customer frontend, Stripe webhooks |
| Order payment gRPC methods (proposed; Order server pending) | Order Service | Legacy PaymentIntent adapter |
| `order.placed` event | Order Service | Fulfillment, Notification, Analytics |
| `order.cancelled` event | Order Service | Fulfillment, Notification, Analytics |
| `fulfillment.order_ready` event | Fulfillment Service | Notification Service |
| `fulfillment.order_picked_up` event | Fulfillment Service | None in MVP |
| `inventory.waste` event | Batch Inventory Service | Analytics Service |

## Catalog gRPC behavior

`proto/catalog/v1/catalog.proto` is the canonical `catalog.v1.CatalogService`
interface. The implementation uses the same application service and Redis as
Catalog REST. Inventory calls GetFlavor before creating a batch, and Order uses
BatchGetFlavors before reserving stock to validate active flavors and snapshot
the agreed purchase price.

- Unary methods: CreateFlavor, GetFlavor, ListFlavors, BatchGetFlavors,
  UpdateFlavor, DeleteFlavor.
- UpdateFlavor is a full replacement, equivalent to REST PUT. It requires all
  editable fields including active; omitted image_url clears the previous image.
- DeleteFlavor archives (active=false), preserving references and the name index.
  Repeating delete on an existing archive succeeds; missing IDs return NOT_FOUND.
- Writes require a verified Manager JWT in authorization metadata. Reads follow
  REST visibility: anonymous/customer/staff see active flavors only. Manager can
  see archived metadata. An explicit active=false filter requires Manager.
- Read representations never include recipe. Invalid supplied credentials fail
  closed even for public reads. Callers cannot select their own read scope.
- BatchGetFlavors accepts 1–100 unique UUIDs and preserves input order. Any
  missing/hidden ID fails the whole call; it is not an atomic price snapshot.
- Standard gRPC statuses carry ErrorInfo with domain `catalog.gelatoflow` and a
  stable reason. Validation is INVALID_ARGUMENT, missing/hidden resources are
  NOT_FOUND, duplicate names are ALREADY_EXISTS, and concurrent writes are ABORTED
  with reason FLAVOR_UPDATE_CONFLICT. Authentication/authorization use
  UNAUTHENTICATED/PERMISSION_DENIED. Storage connection failures are UNAVAILABLE;
  deadlines/cancellation remain DEADLINE_EXCEEDED/CANCELLED.
- Create has no idempotency key. Clients must not automatically retry mutations
  after an ambiguous timeout; reconcile the result first.

See [Catalog gRPC guide](../catalog-service/GRPC.md) for request examples,
generation, runtime limits and local verification. The canonical proto's Go
package follows the shared contract convention; it does not imply a published Go
module. Each Go consumer generates into its own module using a scoped import
override, as Catalog's generation template demonstrates.

## Inventory gRPC behavior

The gRPC service uses standard status codes:

| Code | Meaning |
| --- | --- |
| `INVALID_ARGUMENT` | Missing IDs, duplicate flavors, or non-positive portions |
| `NOT_FOUND` | Reservation does not exist |
| `FAILED_PRECONDITION` | Insufficient stock or invalid reservation state |
| `ALREADY_EXISTS` | An idempotency key is reused with a different request |
| `UNAVAILABLE` | Inventory storage is temporarily unavailable |

Repeating an operation with the same idempotency key and the same request returns
the original successful response.

Inventory implementation details are in [its README](../batch-inventory-service/README.md)
and [proposed ADR-007](../docs/architecture/adr-007-inventory-transactions-and-callers.md).
Every Inventory RPC requires a dedicated Order service token in authorization
metadata for isolated development; missing/invalid credentials are UNAUTHENTICATED.
UUIDs must be non-nil. Reservation items are limited to 1–100 distinct flavors.
Same-key replay returns the original successful response, even after a later
reservation transition. V1 allows one reservation per order ID; a different key
for that order is ALREADY_EXISTS. Confirm/Release require a matching order ID.
TTL defaults to 10 minutes, capped by the earliest allocated batch expiry.
Late confirmation is FAILED_PRECONDITION; release after expiry returns EXPIRED.
Availability exceeding the proto int32 capacity is OUT_OF_RANGE.

## Order-orchestrated checkout

`proto/payment/v1/payment.proto` defines the new internal Checkout Session API.
Order calls Payment after persisting the pending order and returns `checkout_url`
from `openapi/order-service.v1.yaml`. Payment's gRPC server authenticates the Order
machine identity, validates immutable request facts and creates/retrieves a Stripe
Checkout Session using provider idempotency. Order owns immutable totals and the
persisted session binding. The stateless adapter's replay protection is bounded
by provider retention; permanent deduplication requires durable reconciliation.
Checkout webhook reporting to Order's HTTP callbacks and reconciliation/refunds
remain pending. See [checkout architecture](../docs/architecture/order-checkout.md)
and the [Payment gRPC guide](../payment-service/GRPC.md) for configuration, retry
and migration rules. Shared deployment requires review by Order, Payment, Gateway
and frontend owners and end-to-end verification.

Order REST v1.2 adds `CANCEL_PENDING` and `PAYMENT_CONFIRMING` status values.
Order durably claims either workflow before contacting Inventory, blocks checkout
for both, and resumes unfinished work in a lifecycle worker. No database columns
are added. Release requests and cancellation events use the constant reason
`ORDER_CANCELLED`, so different cancellation entry points replay identical facts.
Review the enum additions and generic cancellation reason with affected owners;
reconcile unfinished operations from older deployments before rollout. See the
checkout architecture for recovery and payment-refund responsibilities.

## Legacy PaymentIntent integration

`openapi/payment-service.v1.yaml` defines the legacy order-based PaymentIntent
endpoint and signed Stripe webhooks. Client totals are never accepted. Responses
use snake_case and integer minor units. The frontend demo's amount/clientSecret shape requires explicit
migration; see [Payment integration](../docs/architecture/payment-integration.md).

`proto/order/v1/order.proto` adds `payment_intent_id` to GetOrderDetails and the
Payment-only BindPaymentIntent and ReportPaymentResult RPCs. Order must atomically
bind one intent, verify totals, durably deduplicate provider event IDs and own
Inventory confirmation/release and outbox. These additions are proposed for
Order/Gateway owner review; Order does not implement these RPCs. Its selected
Checkout Session flow instead uses Order-to-Payment gRPC and reverse HTTP result
callbacks. The legacy REST/webhook adapter cannot complete that flow unchanged.

## Event compatibility

Events use CloudEvents 1.0 structured JSON with domain fields nested under
`data`. `traceparent` is optional because scheduled work can produce an event
without an inbound request trace. Domain payload fields use `snake_case`, UUID
resource IDs, RFC 3339 UTC timestamps, and integer minor-unit money.

`OrderPlaced` is published to the `order` topic exchange with routing key
`order.placed`. `OrderReady` and `OrderPickedUp` are published to the
`fulfillment` topic exchange with routing keys `fulfillment.order_ready` and
`fulfillment.order_picked_up`. `WasteRecorded` is published to the `inventory` topic exchange
with routing key `inventory.waste`.

The Analytics implementation currently accepts the canonical waste routing key
and snake_case waste identifiers and minor-unit cost fields, alongside legacy
shapes. Parsing support alone does not prove end-to-end compatibility or atomic
deduplication. Validate its projection, date semantics and duplicate handling
with its owner before enabling the Inventory producer on the shared broker.
Canonical monetary fields remain integer `*_minor` values with explicit currency;
legacy decimal fields are not the v1 contract.

Consumers must ignore unknown fields so compatible metadata can be added without
breaking existing readers. Removing a field or changing its meaning requires a
new event version and a routing-key migration plan.

## Validation

From the repository root:

```bash
npx --yes @redocly/cli@2.49.0 lint

buf lint contracts
buf build contracts

python3 -m pip install jsonschema==4.26.0
python3 contracts/scripts/validate_event_examples.py
```

CI runs the same lint, build, event fixture, and Docker Compose configuration
checks for changes to contracts or infrastructure.

## Frontend integration

The frontend demo is not required to adopt these contracts in the first pull
request. Later integration should compose Catalog metadata with Inventory
availability at API Gateway or a frontend-facing adapter.
