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

See [owned NFR evidence and TLS setup](NFR.md) for requirement provenance,
service-local verification and deployment work requiring other owners.

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

Separately, five invalid-credential results lock only the normalized account for
15 minutes (`429` with `Retry-After` up to 900 seconds). A successful login resets
that account's failure count. Database/timeouts/internal errors and corrupt stored
password hashes return `500` without adding a credential failure. Audit records
distinguish `LOGIN_ERROR` from `LOGIN_FAILED`. Both admission and audit IPs use
the actual TCP peer; Auth never trusts `X-Forwarded-For` or similar headers. A
Gateway peer may represent many users, so it is never the 15-minute lockout key.

Audit User-Agent values are normalized to valid PostgreSQL text and bounded to
512 Unicode characters. Metadata explicitly marks truncation, invalid UTF-8 or
NUL replacement and includes the original character count when truncated.
Audit writes finish independently of request cancellation, preserving an active
request's storage deadline (at most 30s) or using a 500 ms grace period for an
already cancelled/expired or unbounded context. At most three attempts handle
transient storage/network failures.
Stable event UUIDs deduplicate ambiguous acknowledgements without updating
append-only evidence; reusing an event ID for different facts fails. Terminal
failures emit a bounded diagnostic while preserving login/signup responses.
A continuous outage of the only database remains best effort; a stronger guarantee
needs independent durable storage or an agreed fail-closed availability policy.
See [the isolated rollout and caller verification](../batch-inventory-service/docs/owned-nfr-handoff.md).

Malformed credential objects return `400 INVALID_REQUEST`, unsupported media or
compression returns `415 UNSUPPORTED_MEDIA_TYPE`, and an oversized body returns
`413 REQUEST_TOO_LARGE`. Database operations receive a request deadline; bcrypt
work runs to completion and is bounded by the configured cost and request limit.

## Auth outbox publisher (NFR-05)

Signup already stores `user.registered` with the user in the same transaction.
The API now includes an optional background publisher; broker outages do not
participate in signup/login requests or their health endpoint.

Publishing is disabled by default. Enable `OUTBOX_PUBLISH_ENABLED=true` only after
agreeing the event and receiving topology with its owners, and explicitly set
`OUTBOX_AMQP_URL` plus `OUTBOX_EXCHANGE`. Auth passively checks the exchange and
never creates an exchange, queue, binding or broker user. A durable receiving queue
must bind `user.registered`. Unroutable events stay pending even when the broker
ACKs them; only a positive confirm without a mandatory return records `published_at`.

The worker uses the existing outbox columns/index; **no migration is added**.
An atomic lease uses `FOR UPDATE SKIP LOCKED` only for this work queue, releases
the DB lock before network I/O, and expires after the publish timeout plus 10s.
Attempts fence old completions. Failed sends retry after 1s, 2s, 4s and so on,
capped at 5 minutes, without deleting pending events. Each attempt uses a fresh
AMQP connection/channel, so a restarted broker is retried automatically.

`OUTBOX_POLL_INTERVAL` defaults to 1s (50ms–1m); `OUTBOX_PUBLISH_TIMEOUT` defaults
to 5s (100ms–30s). Dial, TLS/AMQP handshake, publish and confirm are bounded by
that attempt. Shutdown cancels network I/O and allows at most 2s for the DB
acknowledgement/retry update before closing the pool.

Use `amqps://` outside an isolated trusted development network. It verifies the
broker hostname and CA with TLS 1.2 minimum; `OUTBOX_TLS_CA_FILE` can add a private
CA. No skip-verification or plaintext fallback is provided. Configuration and
worker diagnostics exclude the URL, credentials, payload and customer PII.

The [service-local event draft](events/user-registered.v1.schema.json) wraps old
rows into CloudEvents 1.0 using the persisted event ID/time. Only `user_id`, `role`
and `status` leave Auth; email, display name and credential fields are excluded.
This draft does not replace or edit `contracts/`. Shared adoption remains a
separate review. Duplicates are possible after a broker ACK followed by a crash/DB
failure; consumers must deduplicate the stable CloudEvents `id`. Invalid old rows
remain pending with backoff and a bounded `invalid_event` diagnostic.

With **isolated** PostgreSQL and RabbitMQ fixtures:

```sh
TEST_DATABASE_URL='postgres://fixture_user:fixture_password@127.0.0.1:5432/fixture_db?sslmode=disable' \
TEST_AUTH_AMQP_URL='amqp://fixture_user:fixture_password@127.0.0.1:5672/' \
  go test -race -count=1 ./...
```

Rabbit tests create/delete uniquely named `auth_test_*` exchanges/queues only.
They skip explicitly when `TEST_AUTH_AMQP_URL` is absent; no broker test is
claimed from the PostgreSQL-only CI fixture. See [the ADR](docs/adr-auth-outbox.md).
The reliability design follows RabbitMQ's [publisher confirms](https://www.rabbitmq.com/docs/confirms)
and [mandatory routing](https://www.rabbitmq.com/docs/publishers#unroutable) guidance.

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

The separate frontend `auth-integration` branch uses backend authentication by
default through its server-side `AUTH_SERVICE_URL`. Frontend `main` still uses
mock authentication; this backend PR does not update that repository. In the
integration branch, Auth uses a HttpOnly session cookie and real Go identity
checks, while business handlers still use mock data. Setting a business API URL
alone does not convert that cookie into a Gateway Bearer token. The backend
bearer-token OpenAPI contract remains the source of truth.

## Round 2 authentication demo

See [demo/README.md](demo/README.md) for an Auth-only stack, trusted local
Customer/Staff/Manager fixtures, real login/token verification and frontend
runtime configuration. It is based on the latest main and excludes the earlier
Gateway, Analytics and Fulfillment patches from `auth-hardening`. It does not
claim that the three business use cases are integrated or demo-ready.

## Gateway integration boundary

API Gateway on main `7c46029` validates bearer JWTs before forwarding protected
external requests. It removes client-supplied `X-User-*`, `X-Authenticated-*`,
`X-Role`, and `X-Roles` headers and preserves the original Bearer token so each
service can validate it independently. The verified subject and role are stored
in Gateway's process-local context; it does not regenerate trusted identity
headers for downstream services. Order's current header-based identity handoff
therefore remains integration work for the Gateway and Order owners.
Auth Service owns users, roles, credential checks, and token issuance; Gateway
does not query the Auth database or create privileged roles.

`GET /api/v1/auth/me` still requires and validates the bearer token when Auth
Service is called directly. Supplying identity headers without a bearer token is
rejected.

See [INTEGRATION.md](INTEGRATION.md) for the reviewed service revisions, JWT/role
compatibility, local verification and integration work outside this Auth PR.
