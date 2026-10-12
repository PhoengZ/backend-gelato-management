# Payment Service

Java 21 / Spring Boot adapter for Stripe PromptPay payments in THB. The inbound Order-to-Payment
`CreateCheckoutSession` gRPC API is implemented; see [its guide](GRPC.md).
Verified Checkout webhooks now report results through Order's existing HTTP
callbacks; see [the webhook guide](WEBHOOK.md). The retained legacy REST adapter
reads checkout totals and reports PaymentIntent outcomes through Order v1 gRPC.
It owns no database and does not access Inventory or publish business events.

See the [canonical REST contract](../contracts/openapi/payment-service.v1.yaml)
and [integration decision](../docs/architecture/payment-integration.md), including
the required Order behavior and migration from the frontend demo shape.

## Source layout

Java packages under `src/main/java/com/gelatoflow/payment/` are grouped by responsibility:

```text
PaymentApplication.java   Spring Boot entry point
config/                   Application settings, JWT security and Order transports
controller/               REST endpoints
service/                  Checkout and webhook workflows
client/order/             Checkout HTTP callbacks and legacy Order gRPC adapter
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
from the canonical Order and Payment v1 protos; build from this repository, not
an isolated copy of this directory. Generated sources and reports are ignored.
Tests use signed JWT/webhook fixtures, an in-process Order server and a local
HTTP Stripe stub; they require neither Stripe credentials nor Docker.

## Run locally

For a complete PowerShell walkthrough with Order/Catalog/Inventory and a Stripe
Sandbox payment, see [local integration testing](LOCAL_TEST.md). Its smoke script
opens the actual Order-created Checkout and verifies PAID and stock balances.

Supply `.env.example` values as environment variables. Spring does not load
`.env` automatically. Set real Stripe test keys, Auth's signing secret and a
dedicated `PAYMENT_SERVICE_TOKEN` matching Order; no usable secrets are committed.
Set `ORDER_HTTP_BASE_URL=http://localhost:3003` and
`PAYMENT_CALLBACK_ALLOW_PLAINTEXT=true` for isolated local development. Keep
`PAYMENT_REQUEST_TOKEN` separate for inbound gRPC. See `.env.example` for the
retained legacy gRPC/JWT settings; `ORDER_GRPC_TLS=false` applies only to that
local legacy channel, not the new HTTP callbacks.

```bash
./mvnw spring-boot:run
```

Port defaults to 3007, matching API Gateway's `PAYMENT_SERVICE_URL` default.
Liveness/readiness are exposed at `/actuator/health/liveness` and
`/actuator/health/readiness`. Health indicates process health; it does not assert
Order or Stripe availability. Dependency failures are handled per request.

## Selected Checkout and webhook flow

Frontend creates an Order and navigates to its `checkout_url`. It does not call
the legacy PaymentIntent endpoint. Payment verifies signed Checkout Session
events and current Stripe state, then calls Order's `payment-succeeded` or
terminal `payment-failed` HTTP callback. Only Order's 204 acknowledges the result.
Duplicates replay; provider/callback failures request Stripe redelivery. Pending
QR authorization never confirms payment or releases stock through this flow.
See [WEBHOOK.md](WEBHOOK.md) for validation and remaining boundary limitations.

For local Stripe forwarding:

```bash
stripe listen \
  --events checkout.session.completed,checkout.session.async_payment_succeeded,checkout.session.async_payment_failed,checkout.session.expired \
  --forward-to http://localhost:3007/api/v1/payments/webhook
```

Use its printed `whsec_...` as `STRIPE_WEBHOOK_SECRET` and restart Payment.
Enable PromptPay in your sandbox's [Payment methods settings](https://dashboard.stripe.com/settings/payment_methods).
Stripe's PromptPay Checkout requires a Thai business account and THB. New Sessions
allow only `promptpay`; there is no card fallback. Restart Payment and create a
fresh Order/key after changing payment methods: existing Sessions and their
idempotency parameters cannot be replaced by this change.
Complete the actual Order-created Session with **Generate QR code**. In Sandbox,
scan with a QR reader to open Stripe's test page, then authorize the test payment;
use a banking app only for live payments. See the [PromptPay Checkout guide](https://docs.stripe.com/payments/promptpay/accept-a-payment?payment-ui=checkout)
and [local walkthrough](LOCAL_TEST.md). Generic CLI fixtures lack the required
metadata. A browser success URL never marks Order paid. Already-created card
Sessions still receive verified webhook settlement.

## Legacy PaymentIntent endpoint

```bash
curl -X POST http://localhost:3007/api/v1/payments/create-payment-intent \
  -H "Authorization: Bearer $CUSTOMER_JWT" \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"c19b830e-a733-41de-bf26-aeb123926e36"}'
```

The 201 response includes `payment_intent_id`, `client_secret`, `amount_minor`
and uppercase `currency`, with `Cache-Control: no-store`. Send `client_secret`
to Stripe.js to confirm PromptPay with the customer's email and display its QR.
New intents accept only PromptPay in THB. Previously bound intents retain their
original methods; this adapter does not replace an existing payment binding.
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
Durable event deduplication is a requirement of that proposed legacy Order RPC
contract; it is not implemented by Order's current HTTP callback boundary.

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
  -f infra/docker/compose.payment.yml -f payment-service/compose.checkout.yml \
  up --build payment-service
```

For host-based Order, set `ORDER_HTTP_BASE_URL=http://host.docker.internal:3003`
in the Docker environment. The overrides use the shared network and bind Payment
HTTP and gRPC ports only on localhost. The Payment-owned override supplies the
new callback settings without modifying shared infrastructure files.
No Payment image is assumed to have been published to the team's registry.

## Integration dependency

Order can call the inbound Checkout RPC and receive verified results through
its existing HTTP callbacks. Only Payment code/configuration changes in this
increment. The current Order boundary has no provider event ID or exposed bound
Session ID; durable event deduplication, richer binding verification and late
payment reconciliation/refunds remain pending. Follow [WEBHOOK.md](WEBHOOK.md).
The legacy PaymentIntent flow still requires its owner to implement the
canonical GetOrderDetails, BindPaymentIntent and ReportPaymentResult RPCs,
machine authentication, durable deduplication, Inventory orchestration and
outbox. Late-payment compensation/refunds also require an Order decision.
The service fails closed when that integration is absent; a complete operational
checkout cannot be claimed until Order and real Stripe test-mode verification pass.
