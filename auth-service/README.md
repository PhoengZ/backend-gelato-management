# Auth Service

Auth Service implements the canonical GelatoFlow authentication contract:

- `POST /api/v1/auth/signup`
- `POST /api/v1/auth/login`
- `GET /api/v1/auth/me`
- `GET /health`

The service owns users, password hashes, roles, and account status. Public signup
always creates a `CUSTOMER`; the request decoder rejects unknown fields such as a
client-supplied role.

## Stack

- Go 1.25 and Fiber
- PostgreSQL 16 through pgx
- bcrypt password hashing
- HS256 JWT access tokens with required issuer, audience, issued-at, expiry, user
  ID, and role claims

## Configuration

Copy `.env.example` to `.env` for host-based development. Required security
settings are validated at startup:

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | Auth PostgreSQL connection string |
| `JWT_SECRET` | JWT signing secret with at least 32 bytes |
| `JWT_ISSUER` | Expected `iss` claim |
| `JWT_AUDIENCE` | Expected `aud` claim |
| `JWT_TTL` | Access-token lifetime, whole seconds from `1s` to `1h`, default `15m` |
| `BCRYPT_COST` | bcrypt work factor, allowed range 10-14 |
| `PORT` | HTTP port, default `3001`, matching Gateway's Auth upstream |
| `REQUEST_TIMEOUT` | Database request deadline, positive and at most `30s`, default `5s` |

The example values are for local development only. Never commit a real `.env` or
reuse the development signing secret outside a local machine.

Explicitly blank JWT issuer/audience values are rejected. Missing values use
`gelatoflow-auth` and `gelatoflow-api`, matching Catalog and Inventory. Token
validation uses no clock leeway and requires expiry later than issued-at; keep
service clocks synchronized. Startup creates a dummy bcrypt hash at the configured
cost so missing and disabled accounts do not skip password comparison work.

## Request boundaries

Credential endpoints accept an uncompressed `application/json` object up to
16 KiB. Every required field must occur once with its exact lowercase name and a
string value. Unknown fields, duplicate keys, case aliases, null values, nested
values and trailing JSON are rejected. Passwords contain at least eight Unicode
characters at signup and at most 72 UTF-8 bytes, the bcrypt input limit. Login
accepts at least one character to preserve access to existing accounts.

All Auth responses use `Cache-Control: no-store`. Duplicate Authorization headers
are rejected; `/me` reads current account status from PostgreSQL after validating
the token. Disabling an account prevents new logins and `/me` access, but tokens
already issued remain usable at services that verify JWTs locally until expiry.
Refresh tokens and immediate token revocation remain outside this version.

Signup and login share three per-process admission limits:

- A generous transport budget allows 600 requests per minute per actual TCP peer,
  including malformed bodies, before JSON parsing. It keeps at most 2,048 peer
  records. Forwarded IP headers never create a fresh budget.
- After strict JSON validation, a canonical email has 20 attempts per minute
  across all peers and both endpoints. Email normalization is the same trim and
  lowercase operation used by credential lookup; case or surrounding whitespace
  cannot reset the budget. Up to 4,096 account records are kept. Invalid email
  syntax does not allocate an account record or invoke credential work.
- At most four credential operations execute concurrently across both endpoints.
  Overload is rejected immediately, without queueing bcrypt work or spending an
  account attempt. The work slot is released when the operation completes.

Exhausted budgets return `429 RATE_LIMITED` with `Retry-After` in seconds (1–60);
concurrent-work overload uses one second. Expired records are removed and new
identities are rejected while their bounded map is full. A blocked account does
not block other accounts behind the same Next/Gateway peer, and moving between
peers cannot reset an account's attempt budget.

These are bounded local protections, not a distributed user quota. An attacker
who knows an email can still consume that account's short fixed-window budget;
the result is limited to that identity rather than every browser behind a BFF.
Sufficient traffic can still exhaust the generous shared transport or work cap.
Gateway must apply its own abuse limits, and deployment capacity determines
whether distributed limiting and higher transport/work capacity are needed.

Malformed credential objects return `400 INVALID_REQUEST`, unsupported media or
compression returns `415 UNSUPPORTED_MEDIA_TYPE`, and an oversized body returns
`413 REQUEST_TOO_LARGE`. Database operations receive a request deadline; bcrypt
work runs to completion and is bounded by the configured cost and request limit.

## Database migration

The migration command uses embedded, ordered `*.up.sql` files and a PostgreSQL
advisory lock so two instances cannot apply the same migration concurrently.

With Go installed:

```bash
go run ./cmd/migrate
```

With the local image on the shared Docker network:

```bash
docker run --rm \
  --network gelato-management_gelato_network \
  -e DATABASE_URL='postgres://auth_service:auth_dev_password@auth-postgres:5432/gelatoflow_auth?sslmode=disable' \
  gelatoflow/auth-service:local /app/migrate
```

## Run and test

```bash
go test ./...
go run ./cmd/api
```

Repository integration tests create and remove an isolated PostgreSQL schema. Set
`TEST_DATABASE_URL` to enable them; otherwise they are skipped.

```bash
TEST_DATABASE_URL='postgres://auth_service:auth_dev_password@localhost:5433/gelatoflow_auth?sslmode=disable' \
  go test -race ./...
```

With `TEST_DATABASE_URL` set, the suite also exercises signup, login, identity
lookup and account disabling through a real loopback HTTP listener and PostgreSQL.
Without it the integration tests explicitly skip; unit tests alone do not prove
database or frontend integration.

Build the local image:

```bash
docker build -t gelatoflow/auth-service:local ./auth-service
```

The frontend uses backend authentication by default through its server-side
`AUTH_SERVICE_URL`. The mock session exists only as an explicit non-production
demo mode; it is not an Auth Service identity. The bearer-token OpenAPI contract
remains the source of truth.

## Gateway integration boundary

API Gateway validates bearer JWTs before forwarding protected external requests.
It removes client-supplied `X-User-ID`, `X-User-Role`, `X-Gelato-User-ID`, and
`X-Gelato-User-Role` headers, then regenerates both identity-header forms from the
validated `sub` and `role` claims. It preserves the original Bearer token so each
service can validate it independently.
Auth Service owns users, roles, credential checks, and token issuance; Gateway
does not query the Auth database or create privileged roles.

`GET /api/v1/auth/me` still requires and validates the bearer token when Auth
Service is called directly. Supplying identity headers without a bearer token is
rejected.
