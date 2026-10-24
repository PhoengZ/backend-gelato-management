# Catalog Service

Catalog also exposes a protobuf gRPC CRUD API at `127.0.0.1:50052` by default,
running beside REST with shared application logic and Redis records. See the
[gRPC guide](GRPC.md) for methods, Manager authorization, generation, examples,
tests and local-only plaintext limitations. Order/Inventory clients remain
planned integrations.

Catalog Service implements the canonical GelatoFlow flavor metadata contract:

- `GET /api/v1/flavors`
- `POST /api/v1/flavors` (`MANAGER`)
- `GET /api/v1/flavors/{flavor_id}`
- `PUT /api/v1/flavors/{flavor_id}` (`MANAGER`, full replacement)
- `PATCH /api/v1/flavors/{flavor_id}` (`MANAGER`)
- `DELETE /api/v1/flavors/{flavor_id}` (`MANAGER`)
- `GET /api/v1/flavors/{flavor_id}/recipe` (`MANAGER`)
- `GET /health`

The service owns flavor names, descriptions, prices, images, recipes, allergens,
and active status. It never owns or returns authoritative stock, batch, or
availability values.

## Stack and persistence

- Go 1.25 and Fiber
- Redis 7 with append-only persistence in shared development infrastructure
- HS256 JWT verification using the issuer, audience, and signing secret shared
  with Auth Service at runtime

Each flavor is stored as a Redis JSON value. A flavor-ID set supports listing,
and a normalized-name index prevents duplicate names regardless of letter case.
Create and rename operations use Redis Lua scripts so the record and unique-name
index change atomically.

Recipes are retained in the owned record but projected out of every public
response. Archive requests set `active=false`; they do not delete a flavor ID
that may already be referenced by inventory history.

## Update and archive semantics

`PUT` replaces the complete editable state of an existing flavor. Send `name`,
`description`, `price` (`amount_minor` and `currency`), `allergens`, `recipe`, and
`active`. `image_url` is optional; omitting it removes the old image. The server
preserves `id` and `created_at`. Repeating an identical replacement also preserves
`updated_at`. Missing IDs return `404`; name conflicts return `409`.

Redis compares the previously read document atomically before each write. When
another request wins the race, PUT/PATCH/DELETE return `409 FLAVOR_UPDATE_CONFLICT`
without overwriting its data or leaving a stale name index. Reload before retrying.

`PATCH` continues to update only the provided fields. `DELETE` archives the flavor
and returns `204`; a repeated archive is a no-op. The stored record and references
are retained, but public GET by ID now returns `404` and public lists omit archived
flavors. A verified Manager can read archived metadata and recipes and explicitly
reactivate a flavor with PUT/PATCH `active=true`.

| Read operation | Anonymous / CUSTOMER / STAFF | MANAGER with valid JWT |
| --- | --- | --- |
| GET list, no filter | Active flavors only | All flavors |
| GET list, `active=true` | Active flavors only | Active flavors only |
| GET list, `active=false` | 401 anonymous / 403 other roles | Archived flavors |
| GET or HEAD archived ID | 404, same as missing ID | 200 |
| GET or HEAD recipe | 401 anonymous / 403 other roles | 200, active or archived |

Invalid Authorization values fail with `401` even on public reads. Caller-supplied
role headers and cookies never grant access. Duplicate Authorization headers and
duplicate `active` query parameters are rejected. All flavor-route responses carry
`Cache-Control: no-store` and `Vary: Authorization` to prevent reuse of Manager
representations or stale catalog data through compliant caches.

JSON bodies must use the exact contract field names. Duplicate keys (including
escaped spellings), alternate capitalization, nulls and unknown fields are
rejected. A provided `price` must contain both `amount_minor` and `currency`.
JWT verification checks HS256, signature, issuer, audience, expiry, valid user/role,
required issued-at, and that the token was not issued in the future.

The canonical interface is `../contracts/openapi/catalog-service.v1.yaml`.
Catalog verifies the original Bearer token itself and never trusts user/role
headers supplied by a caller. Gateway route/auth changes belong to its owner.

Contract 1.2.0 tightens archive visibility compared with 1.1.0. Consumers that read
archived flavors must use an authorized Manager flow; do not compensate by trusting
headers or putting Manager tokens into customer-facing clients. Review the read
policy with Gateway/frontend owners before merging the shared Catalog contract.
See [the security audit](SECURITY.md) for verification scope and remaining limits.

## Privacy and mutation audit (NFR-07 and NFR-08)

Request logs contain only an allowlisted HTTP method, registered Catalog route
template, status, and duration. Raw URLs/IDs in paths, query strings, headers,
bodies, flavor names, recipes, and storage error values are not logged. The
existing redactor remains defense in depth; safe logging does not depend on
recognizing every spelling or encoding of a sensitive query/header.
The gRPC library's independent logger also uses bounded severity/source-call-site
records, including when its diagnostic severity/verbosity environment options are
enabled; unauthenticated metadata and unsupported content subtypes are excluded.
HTTP/2 frame diagnostics enabled with `GODEBUG=http2debug=1/2` are reduced to a
timestamped transport event without decoded headers or protocol payloads.

REST and gRPC bind the actor from the locally verified Manager JWT into a private
typed context. Client-supplied identity headers/metadata never determine audit
attribution. Direct repository writers must also supply an explicit trusted
Manager context; otherwise the write fails.

Each committed CREATE, PATCH (`UPDATE`), PUT (`REPLACE`), or archive (`DELETE`)
appends a JSON record to the Catalog-owned Redis list
`<REDIS_KEY_PREFIX>:flavor_audit:<flavor_uuid>` in the same Lua operation as the
flavor/index mutation. Records have a unique ID, actor UUID/role, resource UUID,
UTC time, changed field names, and before/after allergens and active status.
Freeform text, recipes, URLs, JWTs, and credentials are not copied into the
ledger or diagnostic audit receipts. Inspect history through authorized Catalog
storage administration, for example `LRANGE <audit_key> 0 99`; no public history
endpoint is introduced.

The scripts check key types and Redis 7 command/key ACL permissions before any
writes. If audit storage is unavailable or `RPUSH` is denied, the mutation fails
without changing the flavor/name indexes. The Catalog Redis identity therefore
needs `TYPE`, `RPUSH`, and access to the audit namespace in addition to its
existing scripting/read/write permissions. Conflicts, denied/invalid requests,
identical PUTs, repeated archives, and POST idempotency replays do not append
additional committed-mutation records.

Audit keys have no automatic expiry and the application only appends to them.
They use the same Redis AOF/volume durability as Catalog data, and survive a
Catalog process restart. Deployment must preserve Redis persistence/backups and
restrict privileged access; application append-only behavior does not protect
against a Redis administrator changing storage. Existing flavors require no
JSON migration. Historical events from before this version cannot be recovered;
their first subsequent mutation begins a new history with its prior safety
state captured.

## Atomic REST create idempotency

Manager `POST /api/v1/flavors` accepts one optional UUID `Idempotency-Key`.
Equivalent UUID spellings share a canonical key. The Redis Lua create operation
checks the raw-body SHA-256 hash and commits the flavor, indexes, audit record and
original `201` response together. Same-key/same-body retries return that original
body with `X-Idempotent-Replay: true` for 24 hours, including after rename, archive,
lost replies or a process restart. A different body returns
`409 IDEMPOTENCY_CONFLICT`; unavailable/corrupt response storage fails closed with
`503 IDEMPOTENCY_UNAVAILABLE`. There is no in-memory fallback or post-create cache
write. gRPC has no idempotency-key field in its current contract.

For upgrade, stop old Catalog writers before starting this binary. Startup uses
`SCAN` to reconcile every legacy UUID spelling in the existing `idempotency:`
namespace into its canonical key before opening listeners, retaining original
responses and expiry times. Conflicting historical results block that UUID with
`503` until expiry or explicit administrator reconciliation. Existing duplicate
flavors created by the old implementation need separate review. Redis permissions
must include `SCAN`, `PTTL`, scripting and read/write access to `idempotency:*`.
The upgrade does not delete legacy keys or refresh their original retention.

## Configuration

See [owned NFR evidence and TLS setup](NFR.md) for requirement provenance,
optional HTTPS/gRPC TLS and the remaining caller/deployment boundary.

Copy `.env.example` to `.env` for host-based development.

| Variable | Purpose |
| --- | --- |
| `REDIS_URL` | Catalog Redis connection URL |
| `REDIS_KEY_PREFIX` | Namespace for flavor/index/audit keys, default `catalog:v1`; replay keys retain `idempotency:` for compatibility |
| `JWT_SECRET` | Shared JWT secret with at least 32 bytes |
| `JWT_ISSUER` | Expected `iss` claim |
| `JWT_AUDIENCE` | Expected `aud` claim |
| `REQUEST_TIMEOUT` | Maximum Redis-backed request duration, default `3s` |
| `PORT` | HTTP port, default `3002`, matching the current Gateway target |

The example values are local-development values only. Never commit a real `.env`
or reuse the development signing secret in another environment.

The current Gateway defaults to `http://localhost:3002` for Catalog. Catalog's
default port and example environment match that existing setting. A local `.env`
from the earlier Catalog branch may still set `PORT=8082`; change that local value
to `3002`, or intentionally configure the caller for another port. No Gateway,
Auth, shared Compose, or broker changes are required by this Catalog work.

## Run and test

With Go installed:

```bash
go test ./...
go run ./cmd/api
```

Redis integration tests use a random key namespace and remove only that namespace
afterward. Set `TEST_REDIS_URL` to enable them; otherwise they are skipped.

```bash
TEST_REDIS_URL='redis://:catalog_dev_password@localhost:6380/15' \
  go test -v ./tests -run Redis
```

Build and run the local image on the shared infrastructure network:

```bash
docker build -t gelatoflow/catalog-service:local ./catalog-service
docker run --rm \
  --network gelato-management_gelato_network \
  -p 127.0.0.1:3002:3002 \
  -e REDIS_URL='redis://:catalog_dev_password@catalog-redis:6379/0' \
  -e JWT_SECRET='development_only_change_this_secret_32_bytes' \
  gelatoflow/catalog-service:local
```

The frontend demo may keep using mock flavor routes temporarily. API Gateway or a
frontend adapter should later compose Catalog metadata with availability returned
by Batch Inventory Service.
