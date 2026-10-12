# Auth and JWT Decision

Auth Service owns user identity and role assignment. API Gateway authenticates
external requests by validating the access token issued by Auth Service.

## Version 1 authentication flow

1. A user signs up or logs in through Auth Service (`POST /api/v1/auth/signup`, `POST /api/v1/auth/login`).
2. Auth Service verifies the credentials and issues a short-lived JWT access
   token.
3. The client sends the token as `Authorization: Bearer <token>`. For idempotent mutations,
   the client may also send `Idempotency-Key` or `X-Idempotency-Key`, which API Gateway
   explicitly permits in its CORS configuration.
4. API Gateway verifies the access token signature (HMAC-SHA256), expiration (`exp`),
   issued-at (`iat`), issuer (`iss`), audience (`aud`), UUID subject (`sub`), and valid
   role claims before forwarding requests to protected endpoints.
5. API Gateway sanitizes untrusted client headers by stripping any client-supplied
   identity headers (such as `X-User-*`, `X-Role*`, `X-Authenticated-*`) to prevent spoofing,
   and forwards the original `Authorization: Bearer <token>` downstream.
6. Downstream services (such as Catalog and Inventory REST/gRPC) locally verify the
   original Bearer JWT to derive the caller's identity and enforce role capabilities,
   rather than trusting forwarded identity headers.

Catalog implementation clarification: both its REST and gRPC transports verify
the original Bearer JWT locally and derive roles from verified claims. Catalog
does not trust forwarded user/role headers. gRPC uses authorization metadata;
public reads may omit it, while mutations and archived-list filters require
Manager. This does not introduce machine identities or change Auth/Gateway code.

Inventory implementation clarification: REST verifies the original user JWT
locally, with roles matching the table below. Its internal gRPC currently uses a
distinct configured Order service token for isolated development, not forwarded
user headers or Manager credentials. The machine-identity deployment decision is
proposed in [ADR-007](adr-007-inventory-transactions-and-callers.md).

Order implementation clarification: HTTP customer handlers read X-User-ID; they
do not verify JWT signatures locally. Gateway must validate signature, issuer,
audience and expiry, strip untrusted identity headers, and set the canonical user
UUID from sub. Customer GET/cancel enforce order ownership. Direct public access
to this trusted-header interface must be blocked until Gateway integration is
verified. Order forwards the original Bearer token to Catalog when present.

## Checkout machine authentication

| Direction | Credential | Purpose |
| --- | --- | --- |
| Order -> Inventory gRPC | ORDER_SERVICE_TOKEN in authorization Bearer metadata | Reserve/confirm/release stock |
| Order -> Payment gRPC | PAYMENT_REQUEST_TOKEN in authorization Bearer metadata | CreateCheckoutSession; authenticated Payment server implemented |
| Payment -> Order HTTP | PAYMENT_SERVICE_TOKEN in X-Payment-Service-Token | Verified success/failure callback |

The Order-to-Payment token is distinct from both the Inventory token and the
reverse callback token, contains at least 32 bytes and has no whitespace. These
credentials are never browser credentials. A customer request cannot authorize
payment-succeeded/payment-failed by providing its user JWT.

Stripe signatures are verified by Payment over the original webhook bytes, not
by Order or frontend. Gateway must preserve the body/signature and allow that
provider endpoint without a customer JWT. Payment's Checkout adapter must first
verify payment and associate the session with the correct order before calling
Order. Browser return pages only read customer-authorized Order status.

The configured opaque tokens and plaintext gRPC are isolated-development
mechanisms. Agreed TLS and machine identities are required for deployment.
Payment's inbound gRPC server defaults to TLS and requires certificate/key
configuration. Order's current client uses plaintext; local integration must
explicitly disable Payment TLS, and deployment needs a matching TLS client.
See the [Payment gRPC guide](../../payment-service/GRPC.md) for configuration.
See [checkout integration](order-checkout.md).

The frontend demo may continue using its mock session cookie until integration.
The backend bearer-token contract remains canonical.

## Required claims

| Claim | Meaning |
| --- | --- |
| `sub` | User UUID |
| `role` | `CUSTOMER`, `STAFF`, or `MANAGER` |
| `iss` | Token issuer, fixed to the Auth Service |
| `aud` | Intended GelatoFlow API audience |
| `iat` | Issued-at time |
| `exp` | Expiration time |

Version 1 uses a short-lived access token. Logging out means deleting the token on
the client. Refresh tokens and server-side token revocation are deliberately
deferred until the team needs longer sessions.

## Authorization baseline

| Capability | Customer | Staff | Manager |
| --- | :---: | :---: | :---: |
| Read active flavor catalog | Yes | Yes | Yes |
| Read availability | Yes | Yes | Yes |
| Manage fulfillment queue | No | Yes | Yes |
| Manage flavors and allergens | No | No | Yes |
| Read batches and record waste | No | Yes | Yes |
| Create or adjust batches | No | No | Yes |
| View analytics | No | No | Yes |

## Security requirements

- Passwords are stored only as adaptive password hashes, never as plaintext.
- JWT signing secrets or keys are supplied through runtime configuration and are
  never committed to the repository.
- Login failures return one generic credential error to avoid account discovery.
- Role assignment during public signup is always `CUSTOMER`; privileged roles
  require an administrative operation.
- Rate limiting is applied by API Gateway, while Auth Service remains responsible
  for credential validation and token issuance.
- API Gateway strips client-injected identity headers and strictly forwards the
  original Bearer token to protect downstream services against header spoofing.
- CORS policy explicitly allows `Idempotency-Key` and `X-Idempotency-Key` headers
  to support deduplication from browser and mobile frontends.
