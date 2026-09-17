# Catalog security review — 2026-09-09

## Scope and access policy

This review covers Catalog HTTP routing, JWT verification, DTOs, validation,
service logic, Redis persistence, and Go dependencies. The implementation and
tests are local changes; deployment status must be checked separately. Postman
collections and completed assignment/demo files are outside this change.

`DELETE` is an archive operation: `active=false` retains the ID, recipe and
history for references from other services. This is not physical data erasure.
Public, CUSTOMER and STAFF reads must hide archived resources. A verified MANAGER
can inspect archived metadata and recipes, and can explicitly reactivate a flavor
through PUT/PATCH. Existing active=false records use the same policy; no migration
or deletion of stored data is required.

Catalog verifies the original signed Bearer token locally. It never derives
privileges from caller-supplied identity headers, cookies or query parameters.
The HTTP layer selects an explicit service read scope; zero and unknown scopes
hide archived data. The Redis repository retains access to stored records for
authorized operations and reference preservation.

## Confirmed findings and changes

| Finding | Before | After |
| --- | --- | --- |
| Archived metadata exposed publicly | Anonymous GET by ID returned 200 after DELETE; unfiltered GET and active=false included archived records. | Public GET/HEAD by archived ID returns the same 404 as a missing ID. Public lists expose active records only. active=false requires MANAGER: 401 anonymous, 403 CUSTOMER/STAFF. |
| JSON validation bypass | A Manager PATCH with `{"Price":{"currency":"THB"}}` bypassed the exact-key presence check because Go accepts case-insensitive struct keys, changing the amount to zero. Duplicate keys were also accepted. | Exact contract keys and complete nested price are required. Duplicate decoded keys, alternate case, unknown fields, nulls, trailing documents and excessive nesting fail with 400 before any write. |
| Missing cache isolation | Catalog did not explicitly forbid storing responses. Adding role-dependent visibility without cache controls could reuse a privileged representation. No actual cache leak was demonstrated. | Flavor routes set `Cache-Control: no-store`, `Vary: Authorization` and `X-Content-Type-Options: nosniff`, including authorization/error responses. |
| Ambiguous or invalid authentication | Public reads ignored supplied invalid tokens. Future issued-at was not validated, although issued-at was required. | Optional authentication rejects invalid supplied tokens; duplicate Authorization headers are rejected. JWT validation also rejects future issued-at. This does not imply an attacker could forge a valid signature previously. |
| Ambiguous active filter | Repeated active parameters could be interpreted differently by different components. | Repeated decoded active keys fail with 400. |

Recipes were already excluded from metadata DTOs and protected by Manager
authorization. This review did not reproduce anonymous recipe disclosure. Write
routes were already Manager-only; the JSON bypass above required that privilege.
The fix preserves recipe separation and verifies all write methods against
anonymous callers, invalid tokens, CUSTOMER and STAFF.

## Dependency findings

Before updating dependencies, govulncheck found no vulnerabilities reachable from
Catalog calls, but reported affected dependencies at package/module scope:

| Dependency | Previous | Updated | Advisory |
| --- | --- | --- | --- |
| fasthttp | 1.51.0 | 1.70.0 | [GO-2026-4950](https://pkg.go.dev/vuln/GO-2026-4950), file-serving path bypass; Catalog does not serve files. |
| klauspost/compress | 1.17.9 | 1.18.7 | [GO-2026-5841](https://pkg.go.dev/vuln/GO-2026-5841), s2 dictionary parsing; affected calls were not reachable. |
| x/sys | 0.30.0 | 0.44.0 | [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024), Windows string conversion; not the Linux runtime path. |

The fasthttp update also updates its brotli dependency to 1.2.1 and removes the
unused tcplisten requirement. Go minimum remains 1.25 (normalized to 1.25.0).
See [govulncheck documentation](https://go.dev/doc/tutorial/govulncheck) for the
difference between known vulnerable dependencies and reachable vulnerable calls.

## Regression verification

- `tests/security_test.go`: role matrix for list and archived GET/HEAD, protected
  recipe GET/HEAD, spoofed identity, invalid tokens, cache headers, every write
  method, and fail-closed service scopes.
- `tests/request_security_test.go`: duplicate/case-aliased JSON keys, nested price,
  immutable/unknown fields, nulls, malformed/trailing/deep JSON, duplicate headers
  and active filters; rejected requests must leave data unchanged.
- `internal/auth/verifier_test.go`: expiry, issued-at, not-before, subject, role,
  issuer, audience, algorithm, signature and tampering checks.
- `tests/http_redis_integration_test.go`: actual Redis persistence through create,
  replace and archive, rejected ambiguous PATCH requests, public invisibility and
  authorized Manager history access. Existing concurrent-write/index tests remain.

Run from `catalog-service` with a test Redis database:

```sh
TEST_REDIS_URL=redis://127.0.0.1:16380/15 go test -race -count=1 ./...
go vet ./...
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 -show verbose ./...
```

Redis integration tests use a random `catalog:test:<uuid>` namespace and clean up
only that namespace. They must not use or reset a production/demo data prefix.
The Go race detector checks in-process concurrency; the Redis integration tests
separately check optimistic write conflicts and name-index consistency.

Verified locally on 2026-09-09:

| Check | Result |
| --- | --- |
| Full Go suite with Redis DB 15 and race detector | Passed; Redis integration tests executed. |
| Additional all-write-method authorization regression | Passed with race detector. |
| go vet, go mod verify, gofmt, git diff --check | Passed. |
| Source govulncheck with Go 1.27.1 | No known vulnerabilities found after dependency updates. |
| Docker build | Passed; resulting Linux/arm64 binary uses Go 1.25.14. |
| Binary govulncheck against that exact Docker binary | No known vulnerabilities found. |
| Isolated Docker HTTP smoke test | 36 requests passed, including CRUD, PATCH, all read roles, denied writes, preserved Redis archive/recipe and explicit Manager reactivation. Temporary API/Redis containers and network were removed afterward. |
| Repository OpenAPI lint, protobuf lint/build, event examples, Compose config | Passed. |

The original running assignment image was not replaced. Its port 18082 retains
the previous behavior until a separate rebuild/restart. No Postman configuration,
demo file, existing Redis data or teammate service was changed by this review.

## Compatibility and remaining boundaries

- Catalog contract 1.2.0 changes archive read visibility. Gateway/frontend owners
  must review affected consumers before merging. Do not distribute Manager tokens
  to customer clients to restore the previous behavior.
- Existing running images retain the old behavior until rebuilt and restarted.
  This local review does not establish a production deployment or remote CI result.
- A downloaded response cannot be revoked. Previously cached data may require
  removal at the cache owner; `no-store` governs subsequent compliant caching.
- Redis network access/credentials and TLS must be enforced by deployment owners;
  HTTP authorization does not protect data from someone with direct Redis access.
- Gateway-wide authentication/rate limiting, token issuance/revocation and frontend
  rendering belong to other owners and are not modified here. The current Gateway
  proxy does not supply the documented rate-limiting layer. Catalog has a 1 MiB
  request-body limit and HTTP/request timeouts, but GET ALL still loads the full
  catalog; pagination and deployment rate limits remain capacity considerations.
- This review and known-vulnerability scanning cover the code and paths tested,
  not a guarantee against every possible vulnerability or an OS/container audit.
