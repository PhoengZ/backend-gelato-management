# Order-to-Payment Checkout gRPC

Payment implements `payment.v1.PaymentService/CreateCheckoutSession` from
`../contracts/proto/payment/v1/payment.proto`. This is the inbound API used by
Order after it has persisted a pending order and reserved Inventory. Payment
accepts Order's immutable facts; it does not query Order, Catalog or Inventory
to create the session. Order must bind the returned session before exposing its URL.

## Configuration

| Variable | Meaning |
| --- | --- |
| `PAYMENT_CHECKOUT_ENABLED` | Defaults to `true`; `false` runs only the legacy REST adapter |
| `PAYMENT_GRPC_PORT` | Defaults to `50057`, matching Order's `PAYMENT_GRPC_URL` |
| `PAYMENT_REQUEST_TOKEN` | Dedicated Order-to-Payment token, at least 32 UTF-8 bytes; match Order's value |
| `PAYMENT_GRPC_TLS` | Defaults to `true`; disable only for isolated local development |
| `PAYMENT_GRPC_CERTIFICATE` / `PAYMENT_GRPC_PRIVATE_KEY` | Mounted PEM certificate/private-key paths when TLS is enabled |
| `STRIPE_CHECKOUT_SUCCESS_URL` / `STRIPE_CHECKOUT_CANCEL_URL` | Trusted frontend redirects; success may include `{CHECKOUT_SESSION_ID}` |

Redirects require HTTPS. With development plaintext transport, loopback HTTP
redirects are also accepted. Empty tokens, missing TLS paths and invalid redirect
URLs fail startup. Supply the existing Stripe/JWT/legacy Order settings as well;
the REST endpoints still start in the same process. Checkout webhook callbacks
also require `ORDER_HTTP_BASE_URL`, `PAYMENT_SERVICE_TOKEN` and, for isolated HTTP
development, `PAYMENT_CALLBACK_ALLOW_PLAINTEXT=true`; see [the webhook guide](WEBHOOK.md).
These variables are separate
from the legacy `ORDER_SERVICE_TOKEN` and Order's reverse HTTP
`PAYMENT_SERVICE_TOKEN`. Production also requires a TLS-capable Order caller;
the current Order client uses plaintext for development.

The listener has a 16 KiB inbound-message limit and shuts down with Spring.
No reflection service is enabled. Docker exposes port 50057, and the Payment
Compose override binds it to localhost. Mount certificate files through a private
deployment override when using TLS; certificates are not part of the image.

## Local request

Export the `.env.example` settings in PowerShell or your shell; Spring does not
load the file itself. Set Order's `PAYMENT_GRPC_URL=127.0.0.1:50057` and use the
same `PAYMENT_REQUEST_TOKEN` on both sides. From the repository root:

```bash
grpcurl -plaintext \
  -import-path contracts/proto -proto payment/v1/payment.proto \
  -H "authorization: Bearer $PAYMENT_REQUEST_TOKEN" \
  -d '{"order_id":"c19b830e-a733-41de-bf26-aeb123926e36","customer_id":"c90e35eb-4116-4d94-88ba-d4da278cda59","amount_minor":25000,"currency":"THB","idempotency_key":"33ed7b7b-7441-473c-a9bc-0de7c899275c","reservation_expires_at":"2030-01-01T00:00:00Z"}' \
  127.0.0.1:50057 payment.v1.PaymentService/CreateCheckoutSession
```

Replace the example deadline with the actual Inventory reservation deadline.
Protobuf JSON accepts snake_case input; grpcurl renders response fields using
the standard protobuf JSON names `checkoutSessionId` and `checkoutUrl`.

## Validation, replay and provider behavior

- IDs and checkout key are non-nil UUIDs, normalized before provider calls.
- Amount is 1–99,999,999 integer minor units; currency is an uppercase ISO code.
- A valid, future protobuf reservation timestamp is mandatory. An expired hold
  is rejected before any provider mutation, and checked again after creation.
- Stripe receives one line item for the exact immutable Order total, card-only
  payment, trusted redirects and order/customer/original-amount/currency/integration/checkout-key/deadline
  metadata on both the Session and its PaymentIntent. The RPC has no item list.
- Provider key is `gelatoflow-checkout-v1-<idempotency_key>`. All request facts,
  including deadline, are stable Stripe parameters. Same-key replays reuse the
  Session, including across Payment restarts; changed facts conflict at Stripe.
  Keep redirect configuration stable while checkout requests may be replayed.
- Every create/replay also retrieves the Session's current state, because Stripe's
  idempotent create response is a snapshot. Only an open Session with matching
  identifiers, metadata, amount, currency, mode and a Stripe HTTPS URL is returned.
- Each provider call has a 1-second connect timeout, 4-second read timeout and no
  automatic mutation retries. An ambiguous RPC timeout must be replayed with the
  exact same request/key. Creation may still have succeeded when the caller timed out.

| gRPC status | Meaning |
| --- | --- |
| `UNAUTHENTICATED` | Missing or incorrect Order machine token |
| `INVALID_ARGUMENT` | Invalid ID, amount, currency or reservation timestamp |
| `FAILED_PRECONDITION` | Hold expired, or Session is complete/expired |
| `ALREADY_EXISTS` | Stripe rejected reuse of the checkout key with different parameters |
| `ABORTED` | Stripe is concurrently processing that key; replay the original request |
| `UNAVAILABLE` | Provider transport/reconciliation failure; replay the original request |
| `INTERNAL` | Unexpected failure; no provider details or credentials are exposed |

This adapter currently relies on Stripe's idempotency retention (at least 24
hours), not a Payment database. Original requests with expired stock deadlines
fail closed, but permanently detecting reused keys with new deadlines after
provider key pruning still requires durable reconciliation. Do not treat the
provider key as a permanent application idempotency store.

The Session uses Stripe's default expiry; the ten-minute stock deadline is
metadata, because Stripe cannot directly use such a short `expires_at`.
Cancellation/stock expiry can therefore leave a payable URL. Checkout webhooks
now report verified results through Order's existing HTTP callbacks; retryable
Checkout card failures do not cancel an order. Permanent event deduplication,
Order's private binding validation and durable refunds remain shared follow-up
work. The retained PaymentIntent webhook path still uses the legacy Order gRPC
API. See [WEBHOOK.md](WEBHOOK.md) for demo configuration and delivery semantics;
deployed Stripe-to-Order integration has not been verified here.

The Checkout webhook increment adds original amount/currency metadata to the
creation parameters. Use fresh Orders/checkout keys for demo migration; older
provider keys can conflict with changed parameters and older Sessions need
explicit reconciliation as described in WEBHOOK.md.

## Verification

`./mvnw spotless:check verify` covers an actual Spring-managed TCP gRPC listener,
machine authentication, request/provider validation, error mapping, and a local
Stripe HTTP stub exercising SDK parameter encoding, stable replay, changed-fact
conflicts and fresh state retrieval. Tests use no real Stripe credentials.
