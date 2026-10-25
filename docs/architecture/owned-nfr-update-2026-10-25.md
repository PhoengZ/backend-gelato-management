# GelatoFlow owned-service progress update - 2026-10-25

Scope: Auth, Catalog, Batch Inventory and their local verification/documentation.
Delivery branch: `catalog-nfr-privacy-audit`. This update does not modify Gateway,
Order, shared contracts, broker bindings or shared Compose, deploy the project,
migrate a team database, record a video or submit an assignment.

The [four-page PDF addendum](progress/owned-nfr-update-2026-10-25.pdf) contains
the updated owned design, decisions, rollout and verification. The local course
package appends these pages to the original 25-page proposal without replacing
any original page; the original file remains unchanged.

## Source reconciliation

The supplied 25-page proposal `ilovepdf_merged-1033017-17919015503773.pdf` and its
`(1)` copy are byte-identical (SHA-256
`b6fbd067559a63cca0b5197f641376497589c2a522eee27d13fa811620a8137f`).
Section 8/page 11 defines NFR-01 through NFR-09. The separately supplied
`non-functional-requirements.md` expands the policy through NFR-13, including
the all-REST-mutations UUID-header statement in NFR-11. These are distinct
requirement sources; NFR-10 through NFR-13 were not in the original PDF table.
This addendum preserves the original proposal and corrects only owned-service
facts; it does not rewrite teammates' requirements or silently relax NFR-11.

## New implementation and accepted policy

| Area | Current owned behavior | Acceptance boundary |
| --- | --- | --- |
| Auth signup / NFR-11 | Optional UUID key; user, outbox and original 201 response share one transaction. Concurrent equal requests replay one user; changed input returns 409. Receipt expires after 24h, with bounded cleanup. | No-header callers retain old behavior. Login is a separate live credential operation; shared wording must adopt that clarification. |
| Auth audit / NFR-08 | Existing bounded retries, stable audit IDs, cancellation handling and failure diagnostics remain. The owner accepts best effort for this iteration. | Persistent audit-write failure can lose an audit; no guaranteed independent durable copy or fail-closed authentication policy is claimed. |
| Inventory history / NFR-08 | Migration 006 rejects UPDATE/DELETE/TRUNCATE on movements and waste; INSERT remains valid. Startup/readiness checks actual ALWAYS bindings, not just a migration marker. | DBA DDL authority remains; these are ordinary DML protections, not an external tamper-proof store. Old unknown actors stay NULL. |
| Catalog / NFR-07/08/11 | Previously delivered diagnostic privacy, shared REST/gRPC audit and atomic REST replay remain covered by regression tests. | No new Catalog runtime change in this update. |

Auth receipt password equality uses the original salted bcrypt hash, not a
plaintext password or fast password digest. The private proof expires with the
receipt; a DB outage can delay physical cleanup. Signup replay returns the
original operation snapshot and never grants a token. Login always validates
current credentials/status and issues tokens afresh.

## Corrections for proposal and NFR prose

- Auth User-Agent audit bound is **512 Unicode characters**, with safe invalid
  text handling and truncation metadata; it is not 255.
- Audit event names are `LOGIN_SUCCESS`, `LOGIN_FAILED`, `LOGIN_ERROR` and
  `USER_REGISTERED`; `LOGIN_FAILURE` is not the implementation name.
- bcrypt defaults to cost **12** (configurable 10-14); JWT lifetime defaults to
  **15 minutes** (configurable 1 second to 1 hour). These are defaults, not fixed
  constants that operators cannot change.
- Inventory flavor/order advisory keys use the first 64 bits of **SHA-256**,
  not PostgreSQL `hashtext`. Lock order is deterministic; stock allocation does
  not use SKIP LOCKED. Receipt/outbox work queues may use SKIP LOCKED separately.
- New human production/waste facts retain the verified actor. Legacy and trusted
  system facts can have no human actor; no historical identity is invented.
- Inventory implements `inventory.waste`, `inventory.low_stock` and
  `inventory.expiring` production, including local recovery evidence.
  Consumer deduplication/DLQ and notification delivery remain separate.
- Auth now has an opt-in outbox publisher collaborator. Its local event draft
  omits email/display name/password; activation still requires shared adoption,
  receiving topology and consumer agreement. Default publishing is disabled.
- ADR-006 is **Accepted in the supplied proposal** (page 15); the repository
  status is aligned to that source. ADR-007 remains **Proposed** (page 16).
  Neither status proves deployment or full checkout acceptance.

## Revised owned-service design

The [handoff diagram](../../batch-inventory-service/docs/owned-nfr-handoff.md)
shows Gateway REST, internal gRPC, private stores and both producer paths.
Shared Docker DNS resolves `auth-service`, `catalog-service` and
`batch-inventory-service`; the isolated fixture uses its own aliases.
Inventory validates an active Catalog flavor before locking stock. Order holds
its own machine credential and never reads Inventory storage directly. Auth and
Inventory commit producer outbox work with their owned facts; asynchronous
delivery is at least once and consumers must deduplicate stable event IDs.

REST/gRPC TLS 1.2+ and verified outbound Catalog TLS are configurable. Current
Order clients use plaintext credentials at the transport layer; their owners
must support TLS before shared inbound gRPC TLS is enabled. This patch leaves
shared transport defaults unchanged. Auth user JWTs and the Order machine
credential are separate controls; the latter must never reach a browser.

## Rollout and verification

Auth adds migration 004 in its own schema (startup migrates, or run the migrator
explicitly). Inventory must run its updated migrator through 006 before the new
binary. Migration 005 retains actor attribution; 006 preserves rows and adds
history guards. Coordinate the DDL window with Inventory writers. No production
or teammate migration is performed by this delivery.

The final local receipt records **516 passing tests/subtests**, **30 runtime
checks**, **22 validation gates** and four built service images. The native
macOS multi-peer test skips because 127.0.0.2 is unavailable; the same test
passes in Linux. Detailed evidence is recorded in
`owned-nfr-verification-2026-10-25.json` beside this document. Tests use private PostgreSQL/Redis/RabbitMQ,
actual service processes, unchanged Gateway/Order clients and synthetic data.
The actual Gateway smoke covers default and verified HTTPS upstream profiles,
signup/Catalog/Inventory replay, roles, lockout isolation, stock lifecycle and
TLS failures. Additional checks cover broker/process restart and pending work.

Remaining team acceptance: shared login-policy wording and Auth event adoption,
consumer deduplication/DLQ, coordinated TLS/CA rollout, Gateway external HTTPS,
Order p95/full payment lifecycle and Notification-outage behavior. The course
deliverable still requires recording a sub-five-minute clip and submitting the
updated proposal package; the local storyboard/PDF are preparation artifacts.
