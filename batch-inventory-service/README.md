# Batch Inventory Service

Inventory owns batches, available/reserved/sold/wasted portions, reservations,
movements and waste. PostgreSQL is authoritative. REST and gRPC share the same
application service and transactions. Catalog metadata is never replicated.

## Implemented interfaces

| Interface | Operation | Authorization |
| --- | --- | --- |
| REST | GET /api/v1/inventory/availability | Public; supplied invalid JWT is rejected |
| REST | GET /api/v1/inventory/batches and /batches/{id} | Staff or Manager JWT |
| REST | POST /api/v1/inventory/batches | Manager JWT |
| REST | POST /api/v1/inventory/batches/{id}/waste | Staff or Manager JWT |
| gRPC | CheckAvailability, ReservePortions, ConfirmReservation, ReleaseReservation | Dedicated Order service token |

Contracts are in `../contracts/`. Batch creation calls Catalog's existing
`GetFlavor` gRPC before beginning its PostgreSQL transaction. Missing/archived
flavors are invalid input; unavailable Catalog fails closed with HTTP 503.
No Catalog, Gateway, Order, Fulfillment, or Analytics implementation changes are
needed for this service. An actual Order caller remains separate integration work.

There is no arbitrary stock PATCH or hard DELETE in the canonical Inventory API.
Release changes reservation state; it is not deletion. Catalog remains the full
REST/gRPC CRUD demonstration.

## Local setup

Use the existing Inventory PostgreSQL on host port 5434, and run Catalog with
its gRPC listener on 127.0.0.1:50052. Copy `.env.example` to `.env`, replace the
credentials, then export it in your shell (the executable does not load files):

```sh
set -a
. ./.env
set +a
go run ./cmd/migrate
go run ./cmd/api
```

The default REST port is 3004, matching Gateway's existing Inventory target.
The gRPC default is 127.0.0.1:50051. `GET /health` checks liveness; `GET /ready`
checks PostgreSQL connectivity. Catalog/RabbitMQ failures do not remove access
to existing stock: batch creation fails if Catalog is down, and outbox work waits
if RabbitMQ is down. Migrations run explicitly before startup, never implicitly
inside request handling. They are embedded, versioned and transactionally locked.
Use only this service's database/schema and credentials.

`JWT_SECRET`, issuer and audience must match Auth's user-token configuration.
`ORDER_SERVICE_TOKEN` is a separate opaque secret of at least 32 bytes, supplied
as exactly one `authorization: Bearer <token>` gRPC metadata entry. It is a local
integration credential for a trusted Order backend, not a user role or proof of
payment. Never distribute it to browser clients. Order must verify payment before
calling ConfirmReservation. The Auth-issued machine identity/TLS deployment
policy remains proposed in ADR-007.

Both gRPC connections are plaintext for isolated development only. Container
listeners need `HTTP_ADDR=:3004` and `GRPC_ADDR=0.0.0.0:50051`; publish development
ports to loopback. Do not expose these credentials over an untrusted network.

## Stock and reservation semantics

- Every batch satisfies `initial = available + reserved + sold + wasted`, with
  non-negative balances enforced in PostgreSQL as well as application code.
- FEFO sorts by expiry, creation timestamp, then UUID for deterministic ties.
  Inactive/expired batches are never allocated. Availability is a snapshot,
  not a reservation guarantee.
- A multi-flavor reservation commits in full or rolls back in full.
- Transaction-scoped advisory locks serialize writers by flavor, including
  creation, waste and expiry. Operations acquire order lock first, then sorted
  flavor locks, then reservation/batch row locks. Stock is never guarded only
  by an in-process mutex, so replicas share the same concurrency protection.
- Reserve, Confirm, Release and manual Waste use idempotency records committed
  with stock changes. Same operation/key and normalized request replays the
  original successful response. A changed request returns conflict. Failed
  operations are rolled back and do not claim the key permanently.
- Reserve item order and UUID representation are normalized. Original response
  replay can show ACTIVE even after subsequent confirmation; it is the original
  operation result, not a new reservation-state query.
- Version 1 allows one reservation per order ID. A new key for that same order
  returns conflict; create a new order for a new attempt after release/expiry.
- Confirm/Release verify both reservation and order ID. Repeating the same
  terminal operation with a new key is harmless; conflicting transitions fail.
- TTL defaults to 10 minutes and is capped by the earliest allocated batch
  expiry. Confirmation at/after expiry fails even if the worker has not run.
  Order handles late payment reconciliation; Inventory cannot restore a sale.
- Release after reservation expiry produces EXPIRED. Returns to available only
  if the batch is still valid; otherwise the portions become EXPIRED waste.
- A periodic durable sweep expires reservations and remaining available stock.
  Restart/multiple workers are safe. Reserved stock is settled through its
  reservation, never counted twice by batch expiry.
- Batch status becomes EXHAUSTED when available is zero and returns to ACTIVE
  after release if still valid. EXPIRED is visible immediately on reads. ARCHIVED
  is reserved in the existing contract; no archive endpoint is implemented.
- Production dates use YYYY-MM-DD; event reporting dates are UTC, consistent
  with the current Analytics event-time aggregation. Timestamps are RFC3339 UTC.
- Unit costs are THB minor units, with multiplication bounds checked on creation.
- REST CreateBatch has no idempotency key in v1. Do not automatically retry after
  an ambiguous timeout. No external dependency call is made under stock locks.
- List endpoints are unpaginated v1 contracts. gRPC availability above int32
  capacity returns OUT_OF_RANGE instead of truncating/wrapping the value.

## Waste events

Each waste record, movement and canonical CloudEvent outbox row commit atomically.
`PUBLISH_EVENTS=false` is the default: events remain durable but are not sent to
the team's broker until consumer integration is explicitly enabled.

For a local isolated broker, set `PUBLISH_EVENTS=true` and `RABBITMQ_URL`.
The publisher declares the durable `inventory` topic exchange and sends
`inventory.waste`. Provision a durable consumer queue/binding before publishing.
It requires publisher confirmations and mandatory routing; absent routing,
connection failures and negative/unconfirmed acknowledgements leave the event
pending with bounded exponential backoff. Publish calls have a five-second
network deadline. Outbox workers use SKIP LOCKED only on queue rows, not stock.

Delivery is at least once, not exactly once. A crash after broker acknowledgement
but before the PostgreSQL mark can resend the same event ID and payload. Consumers
must deduplicate event IDs atomically with their projection updates. No publisher
confirm proves that Analytics processed the event. Shared Analytics compatibility
and duplicate-safe processing must be validated with its owner before enabling
this producer against the team environment. No Analytics code is changed here.

## Generate, test and build

From repository root with Buf 1.73.0:

```sh
sh batch-inventory-service/scripts/generate-proto.sh
buf lint contracts
buf build contracts
git diff --exit-code -- batch-inventory-service/gen
```

Generated Catalog clients and Inventory interfaces come directly from canonical
schemas, using module-scoped Go package overrides. Never edit generated files.

For the complete suite, provide isolated PostgreSQL, RabbitMQ and Redis URLs:

```sh
export TEST_DATABASE_URL='postgres://inventory_test:inventory_test_only@localhost:5432/inventory_test?sslmode=disable'
export TEST_RABBITMQ_URL='amqp://inventory_test:inventory_test_only@localhost:5672/'
export TEST_REDIS_URL='redis://localhost:6379/15'
sh batch-inventory-service/scripts/test-integration.sh
```

The script builds and starts real Catalog on local ports 54800/54801, runs all
Inventory tests with race detection, and stops Catalog afterward. Those ports
must be unused. PostgreSQL tests create/drop only random test schemas; Redis
Catalog data uses a run-specific prefix and uniquely named flavors. The isolated
Redis instance can be removed after testing. Rabbit tests use an exclusive queue
and expect no unrelated `inventory.waste` bindings when checking unroutable sends.
Use disposable test infrastructure, never team/production data stores.

Without environment variables integration tests skip; `INTEGRATION_REQUIRED=1`
turns missing dependencies into failures. CI provisions all three dependencies.
Tests include two-instance stock contention, two actual processes and restart,
idempotent replay, cancellation while a DB row is locked, FEFO, expiry races,
REST/gRPC authorization, real Catalog validation, and real RabbitMQ delivery.

```sh
cd batch-inventory-service
go vet ./...
go mod verify
go test ./...  # unit tests only unless integration variables are supplied
docker build -t gelatoflow/batch-inventory:local .
```

## Demo

Run both services and PostgreSQL, export a valid `MANAGER_TOKEN` and matching
`ORDER_SERVICE_TOKEN`, then run `go run ./cmd/demo` from this module. Optional
addresses: `CATALOG_REST_URL`, `INVENTORY_REST_URL`, `INVENTORY_GRPC_ADDR`.
The demo creates a unique flavor and two batches, checks FEFO allocation, retries
a reservation, confirms a sale, releases a cancellation, and records waste. It
asserts final balances. Its Catalog flavor is archived; Inventory records and
audit history intentionally remain. Use an isolated demo database. A locally
signed development Manager JWT does not demonstrate Auth login integration.

The Docker image includes `/app/inventory`, `/app/migrate` and `/app/demo`.
Shared Compose and teammate service implementations are unchanged. Image
publication, deployment, Order client integration, low-stock/expiring notification
events and arbitrary batch adjustments are follow-up work.
