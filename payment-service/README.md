# Payment Service

Stateless Java 21 / Spring Boot adapter for Stripe card payments. It reads
checkout totals and reports verified payment outcomes through Order v1 gRPC.
It owns no database and does not access Inventory or publish business events.

See the [canonical REST contract](../contracts/openapi/payment-service.v1.yaml)
and [integration decision](../docs/architecture/payment-integration.md), including
the required Order behavior and migration from the frontend demo shape.

## Source layout

Java packages under `src/main/java/com/gelatoflow/payment/` are grouped by responsibility:

```text
PaymentApplication.java   Spring Boot entry point
config/                   Application settings, JWT security and Order gRPC channel
controller/               REST endpoints
service/                  Checkout and webhook workflows
client/order/             Order gRPC adapter and port
client/stripe/            Stripe SDK adapter and port
dto/                      REST request and response records
exception/                Application errors and REST exception handling
validation/               Shared UUID validation
```

Tests follow the same package layout under `src/test/java/`. Reusable fixtures
and webhook signing helpers live in `testsupport/`.

## Build and test

From `payment-service/`, with Java 21:

```bash
./mvnw spotless:apply
./mvnw spotless:check verify
```

Windows PowerShell uses `./mvnw.cmd`. Maven generates Java gRPC sources directly
from `../contracts/proto/order/v1/order.proto`; build from this repository, not
an isolated copy of this directory. Generated sources and reports are ignored.
Tests use signed JWT/webhook fixtures, an in-process Order server and a local
HTTP Stripe stub; they require neither Stripe credentials nor Docker.

## Run locally

Supply `.env.example` values as environment variables. Spring does not load
`.env` automatically. Set real Stripe test keys, Auth's signing secret and a
dedicated Payment-to-Order token; no usable secrets are committed. TLS defaults
to enabled; set `ORDER_GRPC_TLS=false` only for a local plaintext Order server.

```bash
./mvnw spring-boot:run
```

Port defaults to 3007, matching API Gateway's `PAYMENT_SERVICE_URL` default.
Liveness/readiness are exposed at `/actuator/health/liveness` and
`/actuator/health/readiness`. Health indicates process health; it does not assert
Order or Stripe availability. Dependency failures are handled per request.

## Checkout and webhook

```bash
curl -X POST http://localhost:3007/api/v1/payments/create-payment-intent \
  -H "Authorization: Bearer $CUSTOMER_JWT" \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"c19b830e-a733-41de-bf26-aeb123926e36"}'
```

The 201 response includes `payment_intent_id`, `client_secret`, `amount_minor`
and uppercase `currency`, with `Cache-Control: no-store`. Send `client_secret`
to Stripe.js to collect card details; never send card data to this service.
Repeated checkout returns the bound intent. Requesting a completed or processing
intent returns 409. No client-supplied amount or currency is accepted.

For a local Stripe CLI session:

```bash
stripe listen --events payment_intent.succeeded,payment_intent.payment_failed \
  --forward-to http://localhost:3007/api/v1/payments/webhook
```

Use the printed `whsec_...` as `STRIPE_WEBHOOK_SECRET` and restart the application.
Complete the actual intent from checkout in Stripe test mode. Generic CLI
`stripe trigger` intents lack this integration's order binding and metadata.
Configure the production webhook API version to the pinned SDK's version;
signed notifications are reconciled through a fresh provider retrieval.

HTTP 400 rejects invalid signatures; 502/503 requests redelivery on provider or
Order failure. Signed unsupported events and superseded failures return 200.
Deduplication is durable in Order by provider event ID, never in Payment memory.

## Docker

Build from the repository root:

```bash
docker build -f payment-service/Dockerfile -t gelatoflow/payment-service:local .
docker run --rm --env-file payment-service/.env -p 127.0.0.1:3007:3007 \
  gelatoflow/payment-service:local
```

For a host-based Order server, set `ORDER_GRPC_TARGET=host.docker.internal:50054`
in the Docker environment. The runtime image uses Java 21 and a non-root user.
An optional local Compose build is also available; export the required Payment
secrets in your shell before running from the repository root:

```bash
docker compose --env-file infra/.env.example \
  -f infra/docker/compose.yml -f infra/docker/compose.dev.yml \
  -f infra/docker/compose.payment.yml up --build payment-service
```

The override uses the shared network and binds port 3007 only on localhost.
No Payment image is assumed to have been published to the team's registry.

## Integration dependency

Order has no implementation in this checkout. Its owner must implement the
canonical GetOrderDetails, BindPaymentIntent and ReportPaymentResult RPCs,
machine authentication, durable deduplication, Inventory orchestration and
outbox. Late-payment compensation/refunds also require an Order decision.
The service fails closed when that integration is absent; a complete operational
checkout cannot be claimed until Order and real Stripe test-mode verification pass.
