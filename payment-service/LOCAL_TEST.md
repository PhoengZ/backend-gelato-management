# Local Order + Payment + Stripe Sandbox test (PowerShell)

This walkthrough runs the services on the host and their databases/broker in
Docker. It verifies actual Stripe Sandbox Checkout and signed webhook delivery
to Payment, Order's existing HTTP callback and Inventory's sold balance. It
does not verify Auth login, Gateway, Fulfillment or Notification. Local JWTs and
trusted identity headers are simulated only for isolated loopback testing.

The old `order-service/scripts/test-local.ps1` deliberately leaves Payment
offline and simulates payment callbacks. Do not use that script for this test.

## 1. Prerequisites and Stripe Sandbox

Use Docker Desktop, Go, a Java runtime compatible with this project's Java 21
target, PowerShell and the [Stripe CLI](https://docs.stripe.com/cli/install).
The Maven wrapper is included. All example commands start at the repository root
unless a service directory is specified. Open a fresh terminal for each service
to avoid leaking another service's `PORT` or database settings into it.

In the [Stripe Dashboard](https://dashboard.stripe.com), open the account picker,
select **Switch to sandbox**, then create/open a sandbox. From that sandbox's API
keys page, copy the standard secret key beginning `sk_test_`. `pk_test_` is not
the backend secret key. Keep it in your local Payment environment, not Git/chat.
Hosted Checkout does not require a publishable key in this smoke script.

Payment now offers **PromptPay only** with `THB`. In the same sandbox, enable
PromptPay under [Settings > Payment methods](https://dashboard.stripe.com/settings/payment_methods).
Stripe's PromptPay Checkout supports Thai business accounts. The allowed-method
filter cannot enable a disabled or ineligible method and never falls back to cards.
No extra Payment environment variable or new Stripe key is needed for this switch.
Restart Payment and create a fresh Order/key; existing card Sessions cannot be
converted in place and replaying old creation parameters can conflict at Stripe.

Install the CLI using its official installation instructions. The current Stripe
docs support npm installation:

```powershell
npm install -g @stripe/cli
stripe --version
```

Then run:

```powershell
stripe login
```

Select the same sandbox used by the Payment `sk_test_...` key when authorizing
the CLI in the browser. If your installed CLI offers `stripe login list` and
`stripe switch`, use them to inspect/select the active sandbox. CLI permissions
and commands vary by installed version; use `stripe --help` if unavailable.
Do not use `--live`.

Sources: [manage sandboxes](https://docs.stripe.com/sandboxes/dashboard/manage),
[API keys](https://docs.stripe.com/keys), [CLI login](https://docs.stripe.com/cli/login).

## 2. Start local dependencies

```powershell
docker compose --env-file infra/.env.example `
  -f infra/docker/compose.yml -f infra/docker/compose.dev.yml `
  up -d --wait rabbitmq catalog-redis inventory-postgres order-postgres
```

| Dependency | Host port |
| --- | --- |
| Catalog Redis | 6380 |
| Inventory PostgreSQL | 5434 |
| Order PostgreSQL | 5436 |
| RabbitMQ / management UI | 5672 / 15672 |

Existing volumes are retained. If you initialized them with other credentials,
use those credentials rather than removing volumes to make examples match.

## 3. Configure the four local services

Copy examples only when a local file does not already exist:

```powershell
foreach ($service in @('catalog-service', 'batch-inventory-service', 'order-service', 'payment-service')) {
    if (-not (Test-Path "$service/.env")) {
        Copy-Item "$service/.env.example" "$service/.env"
    }
}
```

Retain each example's database/Redis/RabbitMQ settings. Set these explicitly
public development-only values in the indicated `.env` files:

| Variable | Value | Services |
| --- | --- | --- |
| `JWT_SECRET` | `development_only_change_this_secret_32_bytes` | Catalog, Inventory, Payment |
| `JWT_ISSUER` / `JWT_AUDIENCE` | `gelatoflow-auth` / `gelatoflow-api` | Catalog, Inventory, Payment |
| `ORDER_SERVICE_TOKEN` | `local_order_inventory_test_token_32_bytes` | Inventory and Order |
| `PAYMENT_REQUEST_TOKEN` | `local_order_payment_request_test_token_32_bytes` | Order and Payment |
| `PAYMENT_SERVICE_TOKEN` | `local_payment_callback_test_token_32_bytes` | Order and Payment |
| `PAYMENT_GRPC_URL` | `127.0.0.1:50057` | Order |
| `PAYMENT_REQUEST_TIMEOUT` | `15s` (local Sandbox latency; max 30s) | Order |
| `CATALOG_GRPC_URL` | `127.0.0.1:50052` | Order |
| `BATCH_INVENTORY_GRPC_URL` | `127.0.0.1:50051` | Order |
| `CATALOG_GRPC_ADDR` | `127.0.0.1:50052` | Inventory |
| `GRPC_ADDR` | `127.0.0.1:50052` / `127.0.0.1:50051` | Catalog / Inventory |
| `PUBLISH_EVENTS` | `false` | Inventory (Order still uses RabbitMQ) |

The three machine tokens have different purposes; do not copy one value into
all three. Payment's retained `ORDER_SERVICE_TOKEN`/`ORDER_GRPC_TARGET` settings
belong to the legacy PaymentIntent adapter, not the new Checkout callback. Leave
them configured from its example, with `ORDER_GRPC_TLS=false` locally. The
selected Checkout flow does not need an Order gRPC server on port 50054.

In `payment-service/.env`, also set:

```dotenv
PORT=3007
PAYMENT_CHECKOUT_ENABLED=true
PAYMENT_GRPC_PORT=50057
PAYMENT_GRPC_TLS=false
PAYMENT_HTTP_CALLBACK_ENABLED=true
ORDER_HTTP_BASE_URL=http://localhost:3003
PAYMENT_CALLBACK_ALLOW_PLAINTEXT=true
ORDER_HTTP_CALLBACK_TIMEOUT_MS=5000
STRIPE_LIVE_MODE=false
STRIPE_SECRET_KEY=sk_test_YOUR_SANDBOX_SECRET
STRIPE_WEBHOOK_SECRET=whsec_FROM_THE_LISTENER_IN_STEP_4
STRIPE_CHECKOUT_SUCCESS_URL=http://localhost:3000/payment/success?session_id={CHECKOUT_SESSION_ID}
STRIPE_CHECKOUT_CANCEL_URL=http://localhost:3000/payment/cancel
```

Use simple unquoted `KEY=value` lines for the PowerShell loader below. Go's
Order/Catalog load their own `.env`; Inventory and Spring Payment require
explicit environment loading. This loader is not a general-purpose dotenv parser.

## 4. Start webhook forwarding in a terminal that stays open

```powershell
stripe listen `
  --events checkout.session.completed,checkout.session.async_payment_succeeded,checkout.session.async_payment_failed,checkout.session.expired `
  --forward-to http://localhost:3007/api/v1/payments/webhook
```

Copy the listener's printed `whsec_...` into Payment's `STRIPE_WEBHOOK_SECRET`.
Use the listener secret, not a different Dashboard webhook endpoint's secret.
CLI forwarding does not require a public URL, ngrok or a registered Dashboard
webhook endpoint. Keep the CLI connected to the same sandbox as the backend key.
Restart Payment after changing its secret. [Stripe CLI listen](https://docs.stripe.com/cli/listen).

## 5. Start Catalog, Inventory, Payment and Order in separate terminals

Catalog terminal:

```powershell
Set-Location catalog-service
go run ./cmd/api
```

Inventory terminal:

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

Payment terminal:

```powershell
Set-Location payment-service
Get-Content .env | ForEach-Object {
    if ($_ -match '^\s*([^#\s=]+)=(.*)$') {
        [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process')
    }
}
.\mvnw.cmd spring-boot:run
```

Order terminal:

```powershell
Set-Location order-service
go run ./cmd/server
```

Order auto-migrates its database and connects RabbitMQ at startup. Dependency
gRPC clients connect lazily; health alone does not prove checkout is ready.

| Service | HTTP | gRPC |
| --- | --- | --- |
| Catalog | 3002 | 50052 |
| Inventory | 3004 | 50051 |
| Order | 3003 | none required in this flow |
| Payment | 3007 | 50057 |

This is a host-based walkthrough. If Payment runs in Docker while Order runs on
the host, its callback origin must be `http://host.docker.internal:3003`, not
container localhost; append `payment-service/compose.checkout.yml` as documented
in README. Order on the host still targets the published Payment port 50057.

## 6. Run the interactive Sandbox smoke test

In a new terminal at the repository root:

```powershell
Get-Content catalog-service/.env | ForEach-Object {
    if ($_ -match '^\s*JWT_SECRET=(.*)$') {
        $env:JWT_SECRET = $matches[1]
    }
}
& ./payment-service/scripts/test-local-stripe.ps1
```

The script first checks process health and verifies Manager authentication with
both Catalog and Inventory before creating data. It creates a uniquely named active flavor
priced at 7500 THB minor units (75 THB) and a new batch of 10 portions, then
orders two portions for pickup one hour ahead. It reuses the exact request/key
if checkout times out and receives a recoverable Order dependency error.

Expected sequence:

1. Order returns `PENDING_PAYMENT`, total 15000 THB minor units (150 THB) and a
   Stripe HTTPS Checkout URL; stock becomes available=8, reserved=2, sold=0.
2. The script opens that actual Checkout in your browser. Enter an email and click
   **Generate QR code** for PromptPay. Scan it with a QR reader (for example, your
   phone camera) to open Stripe's hosted test page, then authorize the test payment.
   You can also choose failure there to test the failed path, but this smoke script
   expects success. Sandbox QR payloads contain a test URL; banking apps are for
   live PromptPay payments. No real money is transferred in Sandbox.
   [Stripe PromptPay testing](https://docs.stripe.com/payments/promptpay/accept-a-payment?payment-ui=checkout).
3. The CLI forwards `checkout.session.completed` and shows Payment's response.
   Payment verifies it and calls Order using the dedicated callback token.
4. The script polls Order for up to three minutes, expects `PAID` and stock
   available=8, reserved=0, sold=2, then replays checkout without selling twice.

It never manually calls a payment result endpoint. No Stripe API key or
callback token is needed by the script itself. It prints Order/customer/batch
IDs for later inspection. To wait longer, pass `-WaitSeconds 300`; to avoid
automatic browser opening, pass `-NoOpenBrowser` (that mode prints the sensitive
Checkout URL for you to open locally). If PowerShell blocks scripts, inspect the
file and use a process-only execution-policy override; do not change the global
policy for this demo.

No frontend is required for the backend test. If nothing serves localhost:3000,
the browser's return page can fail after payment; the webhook and Order polling
still determine payment state. Set success/cancel URLs to your running frontend
when testing the UI.

The script creates and retains demo records and a Stripe Sandbox Session; it
does not remove stock, databases or volumes afterward. Inventory expires an
abandoned hold after its configured TTL, normally 10 minutes. Always use a fresh
Order for this increment because older Sessions lack original-total metadata.

## 7. Inspect the result manually

Use the Order ID and customer UUID printed by the script:

```powershell
Invoke-RestMethod -Method Get `
  -Uri 'http://localhost:3003/api/v1/orders/PRINTED_ORDER_UUID' `
  -Headers @{ 'X-User-ID' = 'PRINTED_CUSTOMER_UUID' }
```

The expected status is `PAID`. The GET response intentionally does not return
checkout_url or the private Session binding. In the same Stripe Sandbox,
inspect the Payment/Checkout and its order metadata.

Order writes `order.placed` into its transactional outbox. To verify consumption,
run Fulfillment and Notification separately; their runtime integration and
immediate queue-number notification are not part of this smoke test. Missing
broker bindings can keep outbox publication pending even when stock/Order are
already finalized.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Payment fails startup | Actual `sk_test_` and listener `whsec_`, callback token >=32 bytes, local plaintext flags, redirect URLs |
| Payment gRPC UNAUTHENTICATED | `PAYMENT_REQUEST_TOKEN` matches Order/Payment and is different from the other machine tokens |
| gRPC TLS/handshake errors | Order's local client is plaintext; Payment needs `PAYMENT_GRPC_TLS=false` |
| Inventory dependency UNAUTHENTICATED | Inventory/Order `ORDER_SERVICE_TOKEN` match |
| Catalog/Inventory 401 or 403 | Load the demo JWT_SECRET from Catalog's local `.env`; Inventory must use the same value. A different Payment JWT secret cannot sign these management requests. Also check role, issuer/audience and system clock |
| Stripe 401 / provider 502 | Backend key belongs to the selected sandbox; it is a secret key, not `pk_test_` |
| No webhook delivery | CLI running, connected to the same sandbox as backend key, Checkout events selected |
| Webhook 400 INVALID_WEBHOOK | `STRIPE_WEBHOOK_SECRET` is the current listener's secret; re-import environment and restart Payment |
| Webhook 503 ORDER_UNAVAILABLE | Order reachable on 3003; correct callback origin/token; inspect Order's logs for Inventory failure |
| Webhook 503 ORDER_PAYMENT_CONFLICT | Stock hold expired or Order canceled; reconcile Sandbox payment, do not force PAID |
| Order stays pending | Inspect CLI response, Payment and Order logs; browser return is not payment proof |
| Expected failure from old Order smoke script | That script requires Payment offline; use `test-local-stripe.ps1` for the real Checkout flow |

The PowerShell helper is syntax-checked. Real Sandbox execution requires your
account authorization, local dependencies and test keys; no live payment or
Stripe account setup has been run by the coding agent.
