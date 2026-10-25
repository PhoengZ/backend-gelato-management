# Batch Inventory NFR ownership and verification

The attached Project Progress 2 PDF (section 8, page 11) defines NFR-01 through NFR-09. NFR-10 through NFR-13 are additional repository requirements in `../docs/architecture/non-functional-requirements.md`; they are not additional rows in that PDF. An isolated service test is not proof of production or the complete Order checkout.

| Requirement | Owned implementation and evidence | Remaining boundary |
| --- | --- | --- |
| 01 Consistency | PostgreSQL constraints plus transactions preserve initial=available+reserved+sold+wasted and non-negative balances. | No service may write Inventory storage directly. |
| 02 Concurrency | Ordered flavor advisory/row locks; 20 contending reservations for one portion yield exactly one success. Multi-instance/process and race tests cover this boundary. | The complete Order caller is a separate integration layer. |
| 03 Order p95 | Bounded Catalog RPC, no network dependency call while stock locks are held, indexed queries. | Order checkout's p95 needs agreed load and its full path; this module cannot certify it. |
| 04 Fault isolation | Existing stock operations do not require Catalog/Notification/RabbitMQ; unavailable Catalog rejects new batches, and pending outbox work retries without losing committed stock. | Confirmed-order/Notification outage semantics require Order integration. |
| 05 Messaging | Stock/waste and CloudEvents outbox commit together; publisher confirms, mandatory routing and bounded retry leave unsuccessful sends pending. | Inventory is a producer, not a consumer. Downstream deduplication and DLQ are owned by consumers/shared broker configuration. |
| 06 Security | Manager creates; Staff/Manager waste/read; dedicated Order credential for gRPC. Optional HTTPS/gRPC TLS and verified outbound Catalog TLS. | Caller trust, certificate provisioning and production PostgreSQL/RabbitMQ transport/network settings must be coordinated. |
| 07 Privacy | Bounded worker/startup errors and gRPC/HTTP/HTTP2 diagnostics exclude arbitrary metadata, panic text and handshake reasons. | No logging policy is asserted for teammate services. |
| 08 Auditability | Verified HTTP actor is bound privately and stored with new PRODUCED/WASTED movements in the same transaction. Shared Create rejects missing/non-Manager identity. Waste records retain their actor. Migration 006 rejects UPDATE/DELETE/TRUNCATE of movements and waste records through ALWAYS triggers, including replica mode. | Historical actors remain NULL, never fabricated; trusted internal/background work without a human actor remains distinguishable. A DB administrator can still change DDL/disable protections; this is not a tamper-proof external audit store. |
| 09 Ownership | Only Inventory PostgreSQL is queried; active flavor facts come from Catalog gRPC before the DB transaction. | No cross-database FK/query or shadow table is added. |
| 10 Money extension | int64 minor units, explicit THB and checked multiplication for batch/waste values. | Order totals/payment amounts are outside this service. |
| 11 Idempotency extension | Reserve/Confirm/Release and manual Waste retain their persisted original result. Optional REST header keys additionally cover CreateBatch and Waste aliases using the existing idempotency table and stock transaction. Hash conflicts reject duplicate keys. | The Waste body key remains required. Headers remain optional; existing callers and RPCs retain their contracts. |
| 12/13 Deployment | Current operational state remains in owned PostgreSQL, not process memory. | Payment/Notification statelessness and public data-store/broker port policy require their owners/shared deployment. |

## Additive migration 005

Run the updated Inventory migrator before this binary. Migration `005_movement_actor.sql` adds nullable actor UUID/role to existing movement rows and paired-value constraints; it preserves UUIDs, balances, timestamps and old unknown identities. Stop old Inventory writers before cutover if every future human production action must be attributed. Startup and `/ready` require every embedded migration and physically probe the movement actor columns.

New production writes require `inventory.WithActor(ctx, verifiedSubject, "MANAGER")` at the shared application boundary. HTTP binds this only after local JWT verification. No actor field or identity header is accepted as authority from client JSON. Other trusted internal callers must pass an explicit verified context. There is no new Auth database dependency or cross-service foreign key.

## Append-only history migration 006

Run `006_append_only_history.sql` through the updated Inventory migrator before
starting this binary. It adds only a trigger function and four triggers on
`inventory_movements` and `waste_records`; existing rows, balances, actor NULLs
and timestamps remain intact. Inserts used by production, reservations and waste
continue to work. Corrections require a new compensating business operation,
not rewriting or deleting history. The guards reject UPDATE/DELETE and whole
table TRUNCATE with SQLSTATE 23514, even under replica session mode.

Startup and `/ready` require the migration marker and the actual four ALWAYS
trigger/function/event bindings. A dropped, disabled or replica-only trigger
fails readiness. Applying migration 006 does not read teammate storage or change
any API, event or shared deployment setting. DDL briefly locks the owned tables;
schedule rollout with Inventory writers. DB administrators retain DDL authority.

`tests/history_immutability_test.go` exercises all three prohibited DML commands,
unchanged full row content, legitimate later writes, legacy upgrade, repeatable
migration, replica-mode enforcement and readiness drift against PostgreSQL.

## Optional TLS

- HTTPS: `HTTP_TLS_CERT_FILE` and `HTTP_TLS_KEY_FILE`.
- Inbound Order gRPC: `GRPC_TLS_CERT_FILE` and `GRPC_TLS_KEY_FILE`.
- Outbound Catalog gRPC: `CATALOG_GRPC_TLS_ENABLED=true`; optional `CATALOG_GRPC_TLS_CA_FILE` adds PEM roots to system trust and `CATALOG_GRPC_TLS_SERVER_NAME` selects the verified DNS identity.

Absent server pairs/client mode preserve existing local plaintext behavior. Invalid/partial files or inactive client trust settings fail configuration. An enabled client has no skip-verification or plaintext retry fallback; an untrusted Catalog fails CreateBatch with 503 without committing stock. Enabled transports require TLS 1.2+. Mount files readable by the image's service user and use certificates valid for the caller's service hostname.

Coordinate Order/Gateway/demo trust settings before enabling TLS; those callers are unchanged. Machine-token authorization stays separate from user roles and must still be supplied over the encrypted channel.

## Verification

Use isolated PostgreSQL, Redis, RabbitMQ and real Catalog with `INTEGRATION_REQUIRED=1 go test -race ./...`. Tests cover stock/concurrency/FEFO/replay, real broker failure/recovery, migrations, actor attribution/spoof rejection, audit-write rollback, diagnostics, processes and restart.

`tests/production_audit_test.go` checks actor identity and atomicity plus legacy upgrade/drift. `tests/grpc_log_process_integration_test.go` runs real HTTP2/protobuf diagnostics. TLS package tests verify configuration/trust/version floor; the owned process smoke verifies Auth -> Catalog -> Inventory, separate machine credentials, reserve/confirm and wrong-CA rejection over actual TLS.

## Optional REST header keys

`POST /api/v1/inventory/batches` accepts one non-nil UUID `Idempotency-Key`. Keys are scoped to the verified Manager and CreateBatch operation. A successful retry returns the original batch snapshot, even after stock changes or Catalog becomes unavailable. The validated input hash, batch, movement actor and response commit together in the existing `idempotency_records` transaction; no new migration is required.

Waste keeps its required `idempotency_key` body field. An optional header is an additional alias: it need not equal the body UUID, and both records bind to the same input/result in one transaction. Legacy body-only retries and new header aliases cannot waste stock or publish events twice. Header/body claims use a deterministic lock order. A changed request under an existing key returns 409; malformed/multiple supplied headers return 400 after authorization. No-header behavior remains supported.

See `tests/http_idempotency_test.go`, the [service-local ADR](docs/adr-optional-rest-replay.md), and [owned-service rollout/verification guide](docs/owned-nfr-handoff.md).
