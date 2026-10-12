# Checkout webhooks and Order HTTP callbacks

Payment receives Stripe notifications at `POST /api/v1/payments/webhook` and
verifies the signature over the original body, timestamp tolerance and test/live
mode before making provider or Order calls. Customer JWTs and browser redirects
never authorize payment confirmation.

## Selected Checkout Session flow

New payments use PromptPay only in THB. A generated QR or a pending authorization
is not proof of payment: only verified provider success triggers Order's callback.
The webhook also settles already-created card Sessions with matching metadata;
changing the methods offered for new payments does not discard older results.

1. Order calls Payment's `CreateCheckoutSession` gRPC API with its immutable
   checkout facts and binds the returned Session before exposing the URL.
2. Stripe sends `checkout.session.completed` or an asynchronous result.
3. Payment retrieves the current Session with its PaymentIntent expanded. It
   checks IDs, mode, positive integer amount, ISO currency, client reference and
   the integration/order/customer/original-total/currency/checkout-key/deadline
   metadata persisted from the authenticated Order request. Session and
   PaymentIntent facts must agree with that original total and currency, not
   just with each other. Paid success also requires a
   complete Session, a succeeded intent and full `amount_received`.
4. Payment calls `POST /api/v1/orders/{order_id}/payment-succeeded` with
   `X-Payment-Service-Token`. The canonical callback has no request body.
5. Only Order's `204` acknowledges acceptance. Order owns Inventory confirmation,
   the PAID transition and its transactional outbox. Payment then returns 200 to
   Stripe. Duplicates repeat the callback; there is no in-memory accepted-event
   cache that could hide an earlier failure.

Supported Checkout events and handling:

| Event | Behavior after fresh provider reconciliation |
| --- | --- |
| `checkout.session.completed` | Confirm only a paid Session; unpaid/processing waits for an asynchronous result |
| `checkout.session.async_payment_succeeded` | Confirm a paid Session with a fully succeeded intent |
| `checkout.session.async_payment_failed` | Call `payment-failed` only for a terminal unpaid, complete Session with an uncharged failed/canceled intent |
| `checkout.session.expired` | Call `payment-failed` for an unpaid, expired Session; no intent is allowed when payment was never attempted |

A stale failure for a currently paid Session is ignored. Processing, partial
capture, inconsistent facts, missing expanded payment data and provider errors
are not interpreted as unpaid terminal failures. They return 502/503 without an
Order transition. Checkout's duplicate `payment_intent.*` events, identified by
`checkout_key` metadata, never use the legacy Order gRPC reporting path. A card
decline leaves Checkout open for another attempt; it does not release stock.

## Configuration and local demo

| Variable | Meaning |
| --- | --- |
| `PAYMENT_HTTP_CALLBACK_ENABLED` | Defaults to the effective `payment.checkout.enabled`; disabled callbacks fail closed on Checkout events |
| `ORDER_HTTP_BASE_URL` | Order origin, e.g. `http://localhost:3003` for host development; no path, credentials, query or fragment |
| `PAYMENT_SERVICE_TOKEN` | Match Order's reverse callback token; at least 32 UTF-8 bytes without whitespace, distinct from `PAYMENT_REQUEST_TOKEN` |
| `PAYMENT_CALLBACK_ALLOW_PLAINTEXT` | Defaults to false; explicitly enable only for isolated local HTTP development |
| `ORDER_HTTP_CALLBACK_TIMEOUT_MS` | Connect/request timeout, defaults to 5000, allowed 100–30000 |

Enabled callbacks fail startup on invalid credentials or origin configuration.
HTTP redirects are never followed, preventing service-token forwarding to another
origin. Response bodies and credentials are not included in error messages.

Use Stripe Sandbox/test credentials with `STRIPE_LIVE_MODE=false`. Forward the
Checkout events with the Stripe CLI:

```bash
stripe listen \
  --events checkout.session.completed,checkout.session.async_payment_succeeded,checkout.session.async_payment_failed,checkout.session.expired \
  --forward-to http://localhost:3007/api/v1/payments/webhook
```

Set the printed `whsec_...` as `STRIPE_WEBHOOK_SECRET` and restart Payment.
Complete the actual Session created through Order; generic `stripe trigger`
fixtures lack this integration's metadata. Browser return pages poll Order;
they never call payment result endpoints or receive the service token.

For Docker, use `host.docker.internal:3003` when Order runs on the host, or the
Order container's network name and port when it shares the network. Supply the
callback environment through `docker run --env-file`, or append the Payment-owned
`compose.checkout.yml` after the existing shared Compose files. Do not put keys
or tokens in source control. The legacy gRPC/JWT settings are still required by
the retained REST adapter in the same process.

## Delivery, late payment and remaining boundary limits

Timeouts, connection failures and any callback response except 204 return 503 so
Stripe retries. In particular, a 409 becomes `ORDER_PAYMENT_CONFLICT`; it is not
acknowledged as successful processing and no automatic refund is claimed.
Operator reconciliation is still needed if stock expired or Order was canceled
after Stripe accepted a charge. Payment sends verified late success to Order
rather than suppressing it based on its local clock.

New Sessions persist `amount_minor` and uppercase `currency` in both Session and
PaymentIntent metadata. Sessions created before this increment do not contain
that snapshot and are not accepted by the new Checkout webhook workflow. Use a
fresh Order/idempotency key for the demo. Changing Session-creation parameters
under an older provider key can conflict at Stripe; older Sessions require
operator reconciliation rather than silently bypassing validation.

Order's current callbacks accept only the order ID and token. They do not accept
or durably deduplicate provider event IDs, and its public GET does not expose the
bound Session ID. Payment verifies Stripe's Session/intent facts but cannot
independently compare them to Order's private durable binding through this
contract. State/Inventory operations provide replay handling; permanent event
deduplication, richer binding validation and durable refund/reconciliation remain
shared follow-up work. No Order, shared contract or Inventory code changes in
this increment. Real Stripe-to-Order end-to-end execution must still be verified
with the demo environment.

The retained PaymentIntent REST/webhook path still uses the proposed
`GetOrderDetails`, `BindPaymentIntent` and `ReportPaymentResult` RPCs and is not
the selected demo flow. Its Order gRPC server remains unavailable.

## Verification

The Payment suite covers signed raw-body notifications, paid/pending/failed and
expired states, mismatched metadata/amount/mode, duplicates, provider failure,
late success, real HTTP callback paths/token, non-204/redirect/conflict responses
and timeouts. A Spring HTTP test exercises the webhook controller through the
real HTTP callback client against a local Order stub. Stripe SDK retrieval is
tested against a local HTTP stub with an expanded PaymentIntent. No real
credentials or operational services are used by these tests.

References: [Stripe Checkout fulfillment](https://docs.stripe.com/checkout/fulfillment?payment-ui=stripe-hosted),
[Session retrieval](https://docs.stripe.com/api/checkout/sessions/retrieve),
[Stripe event types](https://docs.stripe.com/api/events/types).
