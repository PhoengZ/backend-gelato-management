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
