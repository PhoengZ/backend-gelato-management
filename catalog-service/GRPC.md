# Catalog gRPC

`catalog.v1.CatalogService` runs beside REST in the same Catalog process. Both
transports call the same application service and Redis repository. It is a real
protobuf server; no custom JSON codec or reflection is required.

## Run locally

Use the existing Catalog `.env` and Redis setup. Add `GRPC_ADDR` if overriding the
loopback default. From catalog-service:

```sh
go run ./cmd/api
```

| Setting | Default / behavior |
| --- | --- |
| PORT | 3002, existing REST listener |
| GRPC_ADDR | 127.0.0.1:50052 |
| REQUEST_TIMEOUT | 3s per operation; an earlier client deadline wins |
| JWT_SECRET / JWT_ISSUER / JWT_AUDIENCE | Shared with the existing Catalog verifier |
| REDIS_URL / REDIS_KEY_PREFIX | Same records and indexes for REST and gRPC |

Both ports must bind successfully before serving. SIGINT/SIGTERM drains both
transports for up to five seconds, force-stops remaining gRPC requests if needed,
then closes Redis. Redis client operations respect request deadlines.

Plaintext is only intended for loopback or isolated local development. A
container must set `GRPC_ADDR=0.0.0.0:50052` to be reachable through its published
port; publish it to `127.0.0.1` on the host for a local demo. Before remote
deployment, arrange TLS and network exposure controls. `EXPOSE` alone does not
publish a port. No shared Compose or teammate-service configuration is changed.

Limits: 1 MiB inbound protobuf message, 4 MiB outbound message, 16 KiB metadata.
ListFlavors currently returns the complete visible catalog; a response beyond the
limit fails with RESOURCE_EXHAUSTED. Pagination and deployment rate limits remain
follow-up work. Reflection and gRPC health service are not registered; REST
`/health` is available.

## Methods and access

| RPC | Meaning | Access |
| --- | --- | --- |
| CreateFlavor | Create metadata; active defaults to true | Manager |
| GetFlavor | Read by UUID; no recipe | Public active / Manager all |
| ListFlavors | Optional active filter; no recipe | Explicit active=false requires Manager |
| BatchGetFlavors | 1–100 unique UUIDs, request order | Same visibility as GetFlavor |
| UpdateFlavor | Full replacement, equivalent to REST PUT | Manager |
| DeleteFlavor | Archive with active=false | Manager |

Read credentials are optional, but an invalid supplied token fails closed.
Writes take exactly one `authorization: Bearer <JWT>` metadata entry. Catalog
verifies it itself and ignores asserted user/role headers. JWT claims and roles
are identical to REST; this is not a machine-identity mechanism.

Manager can read archived metadata; other callers receive NOT_FOUND, just like a
missing UUID. Recipes remain excluded from all gRPC read messages. Create/Update
return the admin representation, including recipe. The existing Manager-only
REST recipe endpoint remains available.

BatchGet fails the entire request for a missing or hidden ID. Empty lists,
duplicate UUIDs (including equivalent representations), malformed IDs and more
than 100 IDs are invalid. BatchGet does not lock prices or offer a transactional
snapshot. Consumers must check active status even if using a Manager credential.

## Write semantics and protobuf presence

Create requires name, description, price, allergens and recipe. Update requires
those fields plus active. Empty description, amount_minor=0, active=false and an
explicitly empty allergen list are valid values. Presence is represented with
optional scalars and the allergens wrapper, so omitted required fields fail
validation instead of silently becoming zero values. Currency must be THB and
prices are integer minor units. UNSPECIFIED/unknown/duplicate allergens fail.

Update replaces the complete editable state; it is not a PATCH or FieldMask API.
Omitting image_url clears the image. An identical replacement keeps updated_at.
Delete retains the ID, recipe and name index. Repeated delete of an existing
archive succeeds without changing updated_at; unknown IDs return NOT_FOUND.

The application validations are shared with REST. Binary protobuf's normal
unknown-field/duplicate-field behavior differs from strict REST JSON parsing;
there is no claim of identical wire-format validation.

## grpcurl examples

Run from the backend repository root with grpcurl installed. Set `CATALOG_TOKEN`
to a valid Manager token for the same issuer/audience/secret. For a local demo
without Auth, a locally signed development JWT can exercise Catalog verification,
but it does not demonstrate Auth login integration. Never commit real credentials.

```sh
grpcurl -plaintext -import-path contracts/proto \
  -proto catalog/v1/catalog.proto \
  -H "authorization: Bearer $CATALOG_TOKEN" \
  -d '{"flavor":{"name":"gRPC Vanilla","description":"Classic vanilla","price":{"amount_minor":"6000","currency":"THB"},"allergens":{"values":["ALLERGEN_MILK"]},"recipe":"milk; vanilla"}}' \
  127.0.0.1:50052 catalog.v1.CatalogService/CreateFlavor
```

Copy the returned `flavor.flavor.id` into `FLAVOR_ID`. Choose a new name for each
demo, because an archived name remains reserved.

```sh
grpcurl -plaintext -import-path contracts/proto -proto catalog/v1/catalog.proto \
  -d "{\"flavor_id\":\"$FLAVOR_ID\"}" \
  127.0.0.1:50052 catalog.v1.CatalogService/GetFlavor

grpcurl -plaintext -import-path contracts/proto -proto catalog/v1/catalog.proto \
  -d '{"active":true}' \
  127.0.0.1:50052 catalog.v1.CatalogService/ListFlavors

grpcurl -plaintext -import-path contracts/proto -proto catalog/v1/catalog.proto \
  -d "{\"flavor_ids\":[\"$FLAVOR_ID\"]}" \
  127.0.0.1:50052 catalog.v1.CatalogService/BatchGetFlavors
```

Full update and archive:

```sh
grpcurl -plaintext -import-path contracts/proto -proto catalog/v1/catalog.proto \
  -H "authorization: Bearer $CATALOG_TOKEN" \
  -d "{\"flavor_id\":\"$FLAVOR_ID\",\"flavor\":{\"name\":\"gRPC Vanilla Updated\",\"description\":\"Updated recipe\",\"price\":{\"amount_minor\":\"7500\",\"currency\":\"THB\"},\"allergens\":{\"values\":[\"ALLERGEN_MILK\"]},\"recipe\":\"milk; vanilla\",\"active\":true}}" \
  127.0.0.1:50052 catalog.v1.CatalogService/UpdateFlavor

grpcurl -plaintext -import-path contracts/proto -proto catalog/v1/catalog.proto \
  -H "authorization: Bearer $CATALOG_TOKEN" \
  -d "{\"flavor_id\":\"$FLAVOR_ID\"}" \
  127.0.0.1:50052 catalog.v1.CatalogService/DeleteFlavor
```

After deletion, public Get returns NOT_FOUND and the active list hides the item.
Get with Manager metadata returns active=false. REST
`GET /api/v1/flavors/$FLAVOR_ID` observes the same visibility and persisted data.

## Errors and retries

| Status | Stable ErrorInfo reason |
| --- | --- |
| INVALID_ARGUMENT | INVALID_ARGUMENT |
| NOT_FOUND | FLAVOR_NOT_FOUND |
| ALREADY_EXISTS | FLAVOR_NAME_CONFLICT |
| ABORTED | FLAVOR_UPDATE_CONFLICT |
| UNAUTHENTICATED | UNAUTHORIZED |
| PERMISSION_DENIED | FORBIDDEN |
| UNAVAILABLE | CATALOG_UNAVAILABLE |
| DEADLINE_EXCEEDED / CANCELLED | DEADLINE_EXCEEDED / CANCELLED |
| INTERNAL | INTERNAL_ERROR |

Application errors include google.rpc.ErrorInfo with domain `catalog.gelatoflow`.
Transport-generated errors, such as a payload limit or client cancellation, may
have no application details. Internal Redis errors are not returned to callers.
After ABORTED, read again before deciding whether to retry. Redis CAS protects
the server read/write interval; it is not a client ETag/precondition mechanism.
Create has no idempotency key. Do not automatically retry writes after an
ambiguous failure: timeout may happen after persistence; inspect the result first.

## Generate and test

From the repository root, with Buf 1.73.0 and Go installed:

```sh
sh catalog-service/scripts/generate-proto.sh
buf lint contracts
buf build contracts
git diff --exit-code -- catalog-service/gen
```

The script reads only the canonical Catalog schema. Its template pins
protoc-gen-go v1.36.11 and protoc-gen-go-grpc v1.5.1 and overrides only Catalog's
Go package into `catalog-service/gen/catalog/v1`. Do not edit generated files or
copy the proto into another service; consumers should generate from contracts
with their own module-specific package override. No shared Go module is assumed.

From catalog-service, use an isolated Redis database for integration tests:

```sh
TEST_REDIS_URL=redis://127.0.0.1:16380/15 go test -race -count=1 ./...
go vet ./...
go mod verify
docker build -t gelatoflow/catalog-grpc:local .
```

Tests use random prefixes and clean only their own keys. Without TEST_REDIS_URL,
real Redis tests are skipped; that is not evidence of integration success. The
suite exercises generated clients over TCP, REST/gRPC persistence, role and
archive visibility, presence checks, conflicts, deadlines, listener failures,
bounded shutdown, and a built Catalog executable restart using the same Redis.

Order/Inventory consumers are planned integrations. See
[ADR-006](../docs/architecture/adr-006-catalog-rest-and-grpc.md). A standalone
client demonstration does not establish those services are already connected.
