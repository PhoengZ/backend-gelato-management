# Catalog Service

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

## Configuration

Copy `.env.example` to `.env` for host-based development.

| Variable | Purpose |
| --- | --- |
| `REDIS_URL` | Catalog Redis connection URL |
| `REDIS_KEY_PREFIX` | Namespace for all Catalog keys, default `catalog:v1` |
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
