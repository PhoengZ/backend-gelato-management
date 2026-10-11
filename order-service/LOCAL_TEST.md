# Local service integration test (PowerShell)

This branch has real Order, Catalog and Inventory implementations. It does not
contain a Payment server implementing `payment.v1.PaymentService/CreateCheckoutSession`.
The existing PaymentIntent implementation is a different interface. The smoke
test below intentionally leaves Payment offline, expects 503/504 with order_id
after stock is reserved, then tests cancellation and a simulated trusted payment
callback. That callback proves Order-to-Inventory confirmation, not provider payment.

Use isolated local development data. The smoke test creates a uniquely named
flavor, one batch and two orders. It retains those records for inspection.
It signs local development JWTs and supplies the trusted Gateway identity header
directly; this does not test Auth login or Gateway authentication.

## 1. Start infrastructure from the repository root

Start Docker Desktop. Go, PowerShell and Docker Compose are required.

```powershell
docker compose --env-file infra/.env.example `
  -f infra/docker/compose.yml -f infra/docker/compose.dev.yml `
  up -d --wait rabbitmq catalog-redis inventory-postgres order-postgres
```

Compose starts storage and RabbitMQ; the Go services below run in separate
terminals. Default host ports: Redis 6380, Inventory PostgreSQL 5434, Order
PostgreSQL 5436, RabbitMQ 5672 and its management UI 15672.

## 2. Prepare local environment files

From the repository root, copy examples only when no local file exists:

```powershell
foreach ($service in @('catalog-service', 'batch-inventory-service', 'order-service')) {
    if (-not (Test-Path "$service/.env")) {
        Copy-Item "$service/.env.example" "$service/.env"
    }
}
```

Use the database/Redis/RabbitMQ connection values from the examples when using
the infrastructure above. Configure these local-only test values:

| Setting | Value | Files |
| --- | --- | --- |
| JWT_SECRET | development_only_change_this_secret_32_bytes | Catalog and Inventory |
| JWT_ISSUER | gelatoflow-auth | Catalog and Inventory |
| JWT_AUDIENCE | gelatoflow-api | Catalog and Inventory |
| ORDER_SERVICE_TOKEN | local_order_inventory_test_token_32_bytes | Inventory and Order |
| PAYMENT_SERVICE_TOKEN | local_payment_callback_test_token_32_bytes | Order |
| PAYMENT_REQUEST_TOKEN | local_order_payment_request_test_token_32_bytes | Order |
| PAYMENT_GRPC_URL | 127.0.0.1:50057 | Order; leave this port unused for the smoke test |
| PORT | 3002 / 3003 | Catalog / Order respectively |
| GRPC_ADDR | 127.0.0.1:50052 / 127.0.0.1:50051 | Catalog / Inventory respectively |
| CATALOG_GRPC_URL | 127.0.0.1:50052 | Order |
| BATCH_INVENTORY_GRPC_URL | 127.0.0.1:50051 | Order |
| CATALOG_GRPC_ADDR | 127.0.0.1:50052 | Inventory |
| PUBLISH_EVENTS | false | Inventory |

These are deliberately public development credentials, not production secrets.
Keep the service listeners on the local machine. Order's startup loads .env and
runs its database migration; Inventory requires explicit environment loading and
migration. Existing shell variables override .env for Order/Catalog, so use fresh
terminals when switching services.

## 3. Start Catalog, Inventory and Order in three terminals

Open each terminal at the repository root.

Catalog:

```powershell
Set-Location catalog-service
go run ./cmd/api
```

Inventory (its executable does not load .env itself):

```powershell
Set-Location batch-inventory-service
Get-Content .env | ForEach-Object {
    if ($_ -match '^\s*([^#\s=]+)=(.*)$') {
        [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process')
    }
}
go run ./cmd/migrate
if ($LASTEXITCODE -ne 0) { throw 'Inventory migration failed' }
go run ./cmd/api
```

Order:

```powershell
Set-Location order-service
go run ./cmd/server
```

The environment loader above supports the simple unquoted KEY=value format in
this repository's examples. It is not a general-purpose dotenv parser.

## 4. Run the cross-service smoke test in a fourth terminal

From the repository root:

```powershell
$env:JWT_SECRET = 'development_only_change_this_secret_32_bytes'
$env:PAYMENT_SERVICE_TOKEN = 'local_payment_callback_test_token_32_bytes'
& ./order-service/scripts/test-local.ps1
```

The script checks actual HTTP and gRPC effects:

- Inventory validates a newly created flavor through Catalog gRPC.
- Order snapshots Catalog's price and reserves two Inventory portions.
- Payment-unavailable retry reuses the same order and does not reserve twice.
- Cancellation returns the two portions; checkout replay returns no URL.
- An unauthenticated payment callback fails with 401.
- A simulated authenticated callback marks PAID and moves reserved portions to sold.
- Callback replay does not sell twice; cancelling a paid order fails with 409.

Expected batch balances: `10/0/0 -> 8/2/0 -> 10/0/0 -> 8/0/2`
for available/reserved/sold. All stages print PASS. Do not treat an arbitrary
503 as success: the script requires the Payment dependency error and order_id,
then checks the persisted Order and Inventory balances.

If PowerShell blocks local scripts, inspect the file and run it under a
process-scoped policy override: `powershell -ExecutionPolicy Bypass -File
./order-service/scripts/test-local.ps1`. The environment values above must remain
set in the calling terminal. This does not change machine-wide policy.

## 5. Verify failure/restart recovery and real repository transactions

From the Order directory:

```powershell
$env:ORDER_TEST_DATABASE_URL = 'postgres://order_service:order_db_dev_password@127.0.0.1:5436/gelatoflow_order?sslmode=disable'
go test -race ./...
go vet ./...
```

Repository tests create/drop only random test schemas. They check competing
claims and cancellation/outbox rollback in real PostgreSQL. Service regression
tests inject release/save failures, restart the service object, and exercise
worker recovery. Those failure-injection tests use doubles for Inventory; they
are separate from the live-service smoke test. Without ORDER_TEST_DATABASE_URL,
repository integration tests skip. Go race detection on Windows requires a
working C compiler (for example gcc).

For a manual worker check, stop the local Inventory **process** while Order runs.
Cancel a fresh pending order and read it with the same X-User-ID: status must be
CANCEL_PENDING and checkout replay must have no URL. Restart Inventory; the
worker should finish CANCELLED and release the portions without another cancel
request. Do not stop the database to perform this check, and do not stop
Inventory during the normal smoke test's balance assertions.

## 6. What remains for full integration

A successful checkout returning 201 and checkout_url requires Payment's actual
Checkout Session gRPC server at 50057, accepting the same PAYMENT_REQUEST_TOKEN
and implementing the canonical proto. Real payment success also requires verified
provider callbacks; manually calling the callback is not evidence of Stripe integration.
Auth login, Gateway header stripping/JWT enforcement, RabbitMQ publisher
confirmation recovery, and downstream event processing are separate tests.

RabbitMQ management is at http://localhost:15672. The repository provisions
Analytics bindings, but an outbox row marked published does not prove consumer
processing. Missing queue bindings can leave outbox events pending; inspect that
separately from the stock assertions. The RabbitMQ P1 remains open.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Catalog/Inventory 401 | Shared JWT secret, issuer/audience and system clock |
| DEPENDENCY_UNAUTHENTICATED | ORDER_SERVICE_TOKEN matches between Order and Inventory |
| CATALOG / INVENTORY unavailable before Payment | Go process running and correct gRPC port |
| Expected offline Payment but got 201 | Stop the Payment server for this test |
| Payment 503 without details.order_id | Earlier dependency failed; check error code and service logs |
| Order cannot start | Order PostgreSQL, RabbitMQ credentials and all required tokens |
| Stock conflict | New active flavor/batch with future expiry; run within the ten-minute hold |

Stopping the Go terminals with Ctrl+C leaves their data intact. To stop only the
test infrastructure, use the same Compose files with `stop rabbitmq catalog-redis
inventory-postgres order-postgres`. Do not remove volumes unless you intend to
erase the local data.
