# Auth service integration review

Reviewed on 10 October 2026 against the fetched backend main `7c460298`,
Order `062d3149`, Payment `8cb3a3f3`, frontend main `ed67f1b` and the separate
local frontend `auth-integration` revision `6810698`.

Auth-issued tokens are compatible with the existing Gateway, Catalog, Inventory
REST and Payment JWT verifiers when their server configuration matches. This
review does not establish complete business-use-case integration.

## User token contract

Auth issues HS256 JWTs with `sub` (non-nil user UUID), `role`
(`CUSTOMER`, `STAFF`, `MANAGER`), `iss`, `aud`, `iat` and `exp`.
Defaults are `JWT_ISSUER=gelatoflow-auth`, `JWT_AUDIENCE=gelatoflow-api` and
`JWT_TTL=15m`. Every user-token verifier needs the same issuer, audience and
server-only `JWT_SECRET` of at least 32 bytes.

Use `Authorization: Bearer <access_token>` for REST, and one `authorization`
metadata value containing `Bearer <access_token>` for Catalog gRPC. Do not
derive trusted user identity from headers supplied by a browser.

Public signup accepts only `email`, `password`, and `display_name` and creates
an active Customer. It returns a User, then the client calls login for the token.
Login returns `access_token`, `token_type`, `expires_in`, and `user`.
`GET /api/v1/auth/me` verifies the token and reads the current active account.

Auth's source and canonical contract:
[token issuance](internal/auth/token.go),
[credential APIs](internal/service/auth_service.go),
[OpenAPI](../contracts/openapi/auth-service.v1.yaml).

## Existing consumers

| Consumer | Authentication and authorization | Review result |
|---|---|---|
| Gateway main | Validates canonical user JWT; signup/login public; Catalog/availability reads optional JWT; other protected routes require JWT | Token compatible. Gateway does not implement a role matrix or regenerate trusted user headers |
| Catalog REST/gRPC | Active reads public; supplied JWT must be valid; writes, recipe and inactive filter require Manager | Compatible; Auth-issued Customer/Staff/Manager role checks passed locally |
| Inventory REST | Availability public; batch reads/waste Staff or Manager; batch creation Manager | Compatible; protected-list role checks passed locally |
| Inventory gRPC | All RPCs require a static `ORDER_SERVICE_TOKEN` | Deliberately separate from user JWT; a Manager JWT is not a machine credential |
| Payment owner branch | HS256 and canonical claims; intent creation Customer only; JWT subject must equal order customer | Source compatible; Payment/Stripe transaction flow was not run |
| Order owner branch | Reads customer identity from `X-User-ID`; forwards user Bearer to Catalog | Requires a trusted identity handoff that current Gateway does not provide |
| Fulfillment / Analytics main | No service-level JWT/role guards; Gateway requires a valid JWT | Auth issuance does not establish Staff/Manager enforcement in these services |
| Frontend local `auth-integration` | Maps Go user fields; stores token in HttpOnly cookie; protected handlers call Go `/me` | Auth response compatible; separate frontend work, outside this backend PR |
| Frontend main | Local mock accounts and tokens with a different claim shape | Does not use this Go Auth service yet |

Source anchors on main:
[Gateway routes](../api-gateway/internal/router/router.go),
[Gateway identity sanitization](../api-gateway/internal/middleware/sanitize.go),
[Gateway JWT verification](../api-gateway/internal/auth/verifier.go),
[Catalog guards](../catalog-service/internal/transport/grpc/auth.go),
[Inventory guards](../batch-inventory-service/internal/transport/http.go),
[Inventory machine credential](../batch-inventory-service/internal/transport/grpc.go).

Owner-branch evidence:
[Order identity extraction](https://github.com/PhoengZ/backend-gelato-management/blob/062d3149478fb50e579b8e5863abff799e9cff0e/order-service/internal/handler/order_handler.go#L120),
[Payment JWT and role policy](https://github.com/PhoengZ/backend-gelato-management/blob/8cb3a3f3d197d068efc30af3c48930c9f7746a58/payment-service/src/main/java/com/gelatoflow/payment/config/SecurityConfig.java#L23).

## Machine credentials and session lifecycle

Order and Inventory need matching `ORDER_SERVICE_TOKEN` values for Inventory
RPCs. The value must be at least 32 bytes, contain no whitespace, and differ
from `JWT_SECRET`. Auth does not issue this credential.

Payment's Order gRPC credential and Order's current
`X-Payment-Service-Token` callback are another independently owned integration
boundary. The Payment branch expects Order gRPC methods while the reviewed
Order branch exposes HTTP callbacks; changing a user JWT cannot resolve that.

Disabling an account blocks login and Auth `/me`. Services that verify JWTs
locally may continue accepting the issued role until expiry. Browser logout
clears the cookie; copied tokens remain valid until expiry. Refresh tokens and
immediate revocation are outside v1.

## Work requiring the other service owners

1. Gateway and Order need to agree on trusted customer identity forwarding or
   Order-local JWT verification. Client-supplied `X-User-ID` is not a substitute.
2. Gateway currently requires a user JWT for `/api/v1/payments/webhook`.
   Payment intentionally permits that endpoint and verifies `Stripe-Signature`;
   its public webhook routing needs to be resolved by the owners.
3. Order and Payment need compatible transport, callback methods and machine
   credentials.
4. Fulfillment and Analytics need their intended role guards. A valid user
   token alone is not proof of the required role.
5. Frontend main needs real Auth integration. Its business adapter also needs
   to forward the HttpOnly session token server-side as Bearer; setting
   `NEXT_PUBLIC_API_BASE_URL` alone does not implement this. The local
   `auth-integration` branch still has mock business handlers and restricts
   Inventory reads/waste to Manager, whereas Batch REST also permits Staff.

These are recorded integration boundaries. This Auth PR does not change
Gateway, Order, Payment, Fulfillment, Analytics, shared infrastructure or
frontend source.

## Local verification

- Auth race suite with real isolated PostgreSQL: 50 top-level tests and
  155 passing outcomes; one cross-peer test skipped because the host has no
  `127.0.0.2` loopback alias.
- Real Auth login and `/me` for Customer, Staff and Manager; incorrect password
  401, anonymous identity 401, and public-signup role injection 400.
- 24 HTTP/gRPC interoperability cases passed using the unchanged main Gateway
  and the retained isolated Catalog/Inventory stack whose auth transports match
  main. Checks included Catalog gRPC Manager authorization, Inventory REST
  roles, token/header tampering rejection and static Inventory credentials.
- A controlled HTTP upstream probe confirmed Gateway preserves Bearer while
  omitting trusted identity headers. This was not a running Order integration
  test. Anonymous webhook rejection was checked at Gateway without calling
  Stripe.
- Formatting, vet, dependency verification, Auth OpenAPI lint, Buf lint/build,
  event examples, Compose configuration and Auth Docker build passed.

Runtime checks used loopback demo endpoints and read business data without
changing it. Payment compatibility above is source evidence, not a completed
checkout demonstration. Private credentials and JWT settings remain local.
