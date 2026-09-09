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

`PATCH` continues to update only the provided fields. `DELETE` archives the flavor
and returns `204`; a repeated archive is a no-op. The archived record remains
visible through GET by ID and the unfiltered list. Use `?active=true` for the
currently offered catalog. These semantics preserve inventory references.

The canonical interface is `../contracts/openapi/catalog-service.v1.yaml`.
Catalog verifies the original Bearer token itself and never trusts user/role
headers supplied by a caller. Gateway route/auth changes belong to its owner.

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
| `PORT` | HTTP port, default `8082` |

The example values are local-development values only. Never commit a real `.env`
or reuse the development signing secret in another environment.

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
  -p 8082:8082 \
  -e REDIS_URL='redis://:catalog_dev_password@catalog-redis:6379/0' \
  -e JWT_SECRET='development_only_change_this_secret_32_bytes' \
  gelatoflow/catalog-service:local
```

The frontend demo may keep using mock flavor routes temporarily. API Gateway or a
frontend adapter should later compose Catalog metadata with availability returned
by Batch Inventory Service.
