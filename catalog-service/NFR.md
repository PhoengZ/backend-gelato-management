# Catalog NFR ownership and verification

The attached Project Progress 2 PDF (section 8, page 11) defines NFR-01 through NFR-09. NFR-10 through NFR-13 are additional repository requirements in `../docs/architecture/non-functional-requirements.md`; they are not additional rows in that PDF. An isolated service test is not proof of production or the complete Order checkout.

| Requirement | Owned implementation and evidence | Remaining boundary |
| --- | --- | --- |
| 01/02 Stock consistency/concurrency | Catalog owns metadata, never stock. | Inventory owns reservation concurrency. |
| 03 Order p95 | REST/gRPC share Redis with bounded request deadlines and versioned batch reads. | Order latency needs an agreed load and complete checkout measurement; no service-only p95 is substituted. |
| 04/05 Fault isolation/messaging | Catalog has no Notification dependency or RabbitMQ consumer. Writes fail without required Redis/audit persistence. | Confirmed orders, event consumers and DLQ behavior are outside Catalog. |
| 06 Security | Verified Manager JWT for mutations; public projections hide recipes/archives; optional HTTPS and gRPC TLS 1.2+. | Callers must adopt HTTPS/gRPC CA trust before enabling TLS; production data-store TLS/network policy remains deployment work. |
| 07 Privacy | Bounded request/error/gRPC/HTTP2 diagnostics exclude raw request values and secrets. | See SECURITY.md and diagnostic process regressions. |
| 08 Auditability | REST/gRPC writes atomically append verified-actor history with before/after allergen/active state. | Old missing history cannot be reconstructed; Redis persistence/backups and administrator access remain deployment responsibilities. |
| 09 Ownership | Only Catalog Redis is accessed; other services use REST/gRPC. | Public contracts and teammate callers are unchanged. |
| 10 Money extension | Integer minor units and explicit currency, with input validation. | No stock or Order totals are calculated here. |
| 11 Idempotency extension | Optional POST key/hash/flavor/audit/original response commit in one Redis operation; concurrent/restart replay is verified. PUT and archive preserve documented no-op behavior. | The broader repo phrase about every mutation is not a new shared contract. Arbitrary PUT/PATCH/DELETE original-response replay and gRPC mutation retries require a separately reviewed contract; clients must not invent those guarantees. |
| 12/13 Deployment | No new database/broker dependencies or network ports. | Other services' statelessness and production network isolation are external responsibilities. |

## Optional TLS

For HTTPS set `HTTP_TLS_CERT_FILE` plus `HTTP_TLS_KEY_FILE`. For gRPC set `GRPC_TLS_CERT_FILE` plus `GRPC_TLS_KEY_FILE`. Each pair is independent; absent pairs preserve existing local transports. A partial, unreadable or malformed pair fails startup, never silently downgrades. All enabled listeners require TLS 1.2 or newer. gRPC TLS uses normal transport credentials and retains JWT authorization, deadlines and limits.

Mount readable PEM files and configure caller CA trust and a hostname matching the certificate. Never disable verification or commit private keys. Enable TLS only after coordinating caller configuration; the current Gateway/Order/demo code has not been changed by this work.

Inventory can opt into this Catalog TLS endpoint with `CATALOG_GRPC_TLS_ENABLED=true`, its CA file and optional server-name setting, as documented by Inventory.

## Verification

`go test -race ./...` with isolated `TEST_REDIS_URL` covers role/projection policies, Redis persistence, atomic audit/idempotency, diagnostics, restart and transport lifecycle. `internal/transportsecurity/tls_test.go` covers HTTP TLS trust/configuration. The actual owned-services smoke uses gRPC TLS and an Auth-issued Manager token; plaintext/untrusted/wrong-host requests are rejected.
