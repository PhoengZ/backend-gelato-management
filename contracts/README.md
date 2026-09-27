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
| Catalog gRPC API | Catalog Service | Direct clients; planned Order and Inventory integrations |
| Inventory REST API | Batch Inventory Service | API Gateway, staff and manager UI |
| Inventory gRPC API | Batch Inventory Service | Order Service |
| `order.placed` event | Order Service | Fulfillment, Notification, Analytics |
| `inventory.waste` event | Batch Inventory Service | Analytics Service |

## Catalog gRPC behavior

`proto/catalog/v1/catalog.proto` is the canonical `catalog.v1.CatalogService`
interface. The implementation uses the same application service and Redis as
Catalog REST. Order/Inventory clients are not integrated by this change.

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

## Event compatibility

Events use CloudEvents 1.0 structured JSON with domain fields nested under
`data`. `traceparent` is optional because scheduled work can produce an event
without an inbound request trace. Domain payload fields use `snake_case`, UUID
resource IDs, RFC 3339 UTC timestamps, and integer minor-unit money.

`OrderPlaced` is published to the `order` topic exchange with routing key
`order.placed`. `WasteRecorded` is published to the `inventory` topic exchange
with routing key `inventory.waste`.

The existing Analytics prototype's flat event shapes are legacy interfaces, not
the canonical version 1 contract. Its consumer and MongoDB projection require a
coordinated migration before canonical producers are enabled. In particular,
`total_amount`, `unit_price`, `subtotal`, and `cost_lost` decimal fields become
`*_minor` integers with an explicit currency.

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
