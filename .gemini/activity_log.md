# Activity Log

## [2026-09-04] Infrastructure Compose and Environment Alignment

- **What was attempted:**
  - Standardized `infra/docker/compose.yml` to use explicit environment variable interpolation for RabbitMQ and MongoDB.
  - Re-added `required: false` to `analytics-service` env_file in compose.yml to fix CI Docker Compose validation.
  - Added MongoDB initial root credential definitions to `infra/.env.example`.
  - Updated `infra/README.md` with production deployment instructions, `.env` linking practices, and security guidelines.
- **The hypothesis being tested:**
  - Explicit environment variable interpolation (`${VAR:?required}`) ensures least-privilege secret isolation, avoids path coupling, and allows CI to validate compose config using `--env-file infra/.env.example` without requiring untracked `.env` files.
- **The observed result/outcome:**
  - Successfully verified with `docker compose --env-file infra/.env.example -f infra/docker/compose.yml -f infra/docker/compose.dev.yml config --quiet` (exit code 0) and production compose validation (exit code 0). `git diff --check` passed cleanly with normalized line endings.

## [2026-09-09] Analytics Service Error Handling and Comprehensive Standards Alignment

- **What was attempted:**
  - Switched to feature branch `fix/analytics-comprehensive-standards-alignment`.
  - Implemented `ApiErrorResponse` model in `analytics-service/internal/models/error.go` matching `docs/API_SPEC.md` Section 8 (`{"message": "...", "code": "..."}`).
  - Implemented centralized Fiber `CustomErrorHandler` in `analytics-service/internal/handler/error.go` and mounted in `cmd/api/main.go`.
  - Updated `analytics-service/internal/handler/v1/analytics.go` to return structured errors with `HTTP_400` and `UNKNOWN_ERROR`.
  - Enabled dual-binding in `analytics-service/internal/messaging/consumer.go` supporting both canonical (`order.placed`, `order.cancelled`, `inventory.waste`) and legacy (`OrderPlaced`, `OrderCancelled`, `WasteRecorded`) routing keys.
  - Implemented flexible unmarshaling in `analytics-service/internal/models/analytics.go` supporting both `snake_case` (canonical) and `camelCase` (legacy) as well as integer minor units (`*_minor`).
  - Added event deduplication check in `analytics-service/internal/service/analytics_service.go` to ensure idempotency.
  - Synchronized `analytics-service/README.md` with `docs/API_SPEC.md` for endpoint path, response schema, and error format.
  - Created HTTP handler test suite in `analytics-service/tests/handler_test.go` and expanded `analytics-service/tests/service_test.go`.
- **The hypothesis being tested:**
  - Standardizing error formats and routing keys makes `analytics-service` resilient, backwards-compatible, and fully conforming to `docs/API_SPEC.md` and repository standards.
- **The observed result/outcome:**
  - All 9 unit and handler tests passed cleanly (`go test -v ./tests/...`).
  - `git diff --check` and `docker compose ... config --quiet` passed with exit code 0.

## [2026-09-24] Docs: Cross-Service gRPC Architecture and Analytics Client Streaming

- **What was attempted:**
  - Switched to branch `docs/grpc-cross-service-and-analytics-streaming`.
  - Added `.gemini/` to `.gitignore`.
  - Updated `docs/architecture/service-data-ownership.md` Boundary Rules to prohibit cross-service table replication (no shadow/duplicate tables) and mandate gRPC queries on demand. Added Analytics client-streaming gRPC rule.
  - Updated `docs/overview-architecture.md` Section 1 and Section 3.B to formalize inter-service gRPC data access and Analytics-to-Order client-streaming RPC (`StreamOrderItems`).
  - Updated `docs/event-schema-spec.md` Section 3.2 (`OrderCancelled`) and Section 4 (Analytics projection rules) to replace local table materialization with direct gRPC queries and client streaming.
  - Updated `docs/architecture-diagram.md` and `docs/sequence-diagram.md` to depict inter-service gRPC calls and Analytics client-streaming interaction.
- **The hypothesis being tested:**
  - Replacing cross-service replicated tables with on-demand gRPC queries establishes single authoritative data ownership, eliminates state drift and storage duplication, and fulfills client-streaming requirements for analytics batch reporting.
- **The observed result/outcome:**
  - All documentation references updated consistently.
  - `git diff --check` passed cleanly with exit code 0.

## [2026-09-24] Analytics Service: Remove Shadow Order Table, Add gRPC Client Streaming, and Align with Docs

- **What was attempted:**
  - Created and switched to branch `feature/analytics-service-grpc-and-no-shadow-table`.
  - Deleted `analytics-service/internal/repository/order_repo.go` and purged the shadow `orders` MongoDB collection.
  - Created `analytics-service/internal/repository/event_repo.go` implementing `ProcessedEventRepository` for CloudEvents deduplication by `event.id`.
  - Created `contracts/proto/order/v1/order.proto` defining `StreamOrderItems` (client-streaming) and `GetOrderDetails` (unary).
  - Implemented `analytics-service/internal/client/order_client.go` with gRPC client supporting client-streaming and unary RPCs.
  - Refactored `analytics-service/internal/models/analytics.go` to store integer minor units (`gross_sales_minor`, `cost_lost_minor`, `revenue_minor`), added `CostLostMinor` to `WasteRecordedData`, and replaced `models.Order` with `OrderDetails`.
  - Updated `analytics-service/internal/service/analytics_service.go` to use `eventRepo` for deduplication, accumulate minor units, and query Order Service via gRPC upon `OrderCancelled` without saving any local orders.
  - Updated `analytics-service/internal/factory/analytics_factory.go` to convert integer minor units to floating-point currency at the API presentation boundary.
  - Wired `eventRepo` and `orderClient` in `analytics-service/cmd/api/main.go` and added `ORDER_SERVICE_GRPC_ADDR` in `config/config.go` and `.env.example`.
  - Created comprehensive unit tests in `tests/service_test.go` and `tests/handler_test.go`, and created in-memory `bufconn` gRPC integration test in `tests/order_client_integration_test.go`.
  - Updated `analytics-service/README.md`.
- **The hypothesis being tested:**
  - Eliminating shadow tables and using on-demand gRPC queries alongside event-id deduplication ensures strict microservice autonomy, data integrity, and compliance with repository architecture boundaries.
- **The observed result/outcome:**
  - All 12 unit and in-memory bufconn integration tests passed cleanly in 0.27s.
  - Service binary built successfully (`go build ./cmd/api`).
  - `git diff --check` passed with 0 errors.

## [2026-09-24] Analytics Service: Testing Pyramid Alignment & Full Pipeline E2E Integration Test

- **What was attempted:**
  - Upgraded `analytics-service/tests/consumer_integration_test.go` from a hybrid mock test to a true Full Pipeline E2E Integration Test connecting to both real RabbitMQ and real MongoDB.
  - Added event deduplication verification in real MongoDB `processed_events` collection within `consumer_integration_test.go` using duplicate `OrderPlaced` events.
  - Created `analytics-service/tests/event_repo_integration_test.go` as a dedicated Narrow Integration Test for `EventRepository` against MongoDB.
  - Enhanced `analytics-service/tests/analytics_repo_integration_test.go` to assert persistence and retrieval of integer minor units (`gross_sales_minor`, `cost_lost_minor`).
- **The hypothesis being tested:**
  - Aligning testing architecture with industry standards (broad unit test base, narrow adapter integration tests, and a single full pipeline E2E smoke test) provides fast debugging, avoids hybrid test ambiguity, and proves end-to-end event-to-database persistence and deduplication against real containers.
- **The observed result/outcome:**
  - All tests passed cleanly: `TestConsumerIntegration` (0.33s), `TestAnalyticsRepositoryIntegration` (0.13s), `TestEventRepositoryIntegration` (0.17s), and all unit tests in 2.75s total.

## [2026-09-24] Fix PR #17 CI Failures: Buf Lint RPC Naming and Go Code Formatting

- **What was attempted:**
  - Renamed `StreamOrderItemRequest` and `StreamOrderItemResponse` to `StreamOrderItemsRequest` and `StreamOrderItemsResponse` in `contracts/proto/order/v1/order.proto` to satisfy Buf linter RPC standard naming conventions.
  - Updated corresponding Go structs in `analytics-service/internal/client/order_client.go` and `analytics-service/tests/order_client_integration_test.go`.
  - Executed `gofmt -s -w .` across `analytics-service` to satisfy `gofmt -s -l .` formatting checks.
- **The hypothesis being tested:**
  - Standardizing RPC message names to match Buf lint rules and enforcing Go formatting standardizes cross-service contracts and ensures all CI workflow checks pass.
- **The observed result/outcome:**
  - `gofmt -s -l .` reports 0 unformatted files.
  - All unit and bufconn integration tests in `analytics-service` passed cleanly.
  - `git diff --check` passed with 0 errors.

## [2026-10-10] API Gateway: JWT Verification, Identity Header Sanitization, and CORS Idempotency Headers

- **What was attempted:**
  - Created and switched to branch `feature/api-gateway-jwt-verification-and-cors`.
  - Added `github.com/golang-jwt/jwt/v5` dependency and updated `api-gateway/config/config.go` and `.env.example` with `JWT_SECRET` (enforcing >= 32 bytes), `JWT_ISSUER`, and `JWT_AUDIENCE`.
  - Created JWT Verifier in `api-gateway/internal/auth/verifier.go` strictly validating HMAC-SHA256 signature, expiration (`exp`), issued-at (`iat`), issuer (`iss`), audience (`aud`), UUID subject (`sub`), and valid roles (`CUSTOMER`, `STAFF`, `MANAGER`).
  - Created `StripIdentityHeaders` middleware in `api-gateway/internal/middleware/sanitize.go` to strip client-sent spoofed identity headers (`X-User-*`, `X-Role*`, `X-Authenticated-*`) before routing downstream, while strictly preserving the original `Authorization: Bearer <token>` header for downstream services.
  - Implemented `RequireAuth` and `AuthenticateIfPresent` middlewares in `api-gateway/internal/middleware/auth.go`.
  - Updated `api-gateway/cmd/api/main.go` to validate configuration on startup and allow `Idempotency-Key` and `X-Idempotency-Key` in CORS `AllowHeaders`.
  - Wired route protections in `api-gateway/internal/router/router.go` to secure protected downstream routes (`/orders/*`, `/fulfillments/*`, `/analytics/*`, `/payments/*`, `/auth/me`, catalog mutations, inventory batch mutations) while allowing unauthenticated public reads and public auth actions (`signup`, `login`).
  - Committed work in minor milestone commits to ensure traceability.
  - Created comprehensive unit and integration test suites covering config, verifier, middlewares, route security, header forwarding/sanitization, and CORS preflight.
- **The hypothesis being tested:**
  - Verifying JWT claims at the API Gateway prevents unauthorized traffic from penetrating the internal mesh, while stripping identity headers and forwarding the original Bearer token prevents spoofing while allowing downstream services (like Catalog and Inventory) to locally verify user identity. Adding idempotency headers to CORS prevents browser preflight rejections.
- **The observed result/outcome:**
  - All tests in `api-gateway` passed with 100% success (`go test -count=1 ./...`).
  - Redocly lint passed with 0 errors (`npx @redocly/cli lint`).
  - Docker compose configuration verified cleanly (`docker compose ... config --quiet`).
  - Event schema example validation passed cleanly (`python contracts/scripts/validate_event_examples.py`).
  - `git diff --check` passed with 0 errors.

## [2026-10-13] Documentation Audit & Codebase Alignment Plan

- **What was attempted:**
  - Audited all 13 Markdown documentation files under `docs/` against the current codebase implementation and contracts.
  - Identified discrepancies where documentation lagged behind recent implementations (specifically Payment Service Checkout Session gRPC and webhook callback adapter, as well as `order-cancelled` and `fulfillment` event schemas).
  - Identified architectural targets documented in `docs/` that are not yet implemented in the codebase (Order Service gRPC read server, API Gateway public webhook bypass for Stripe, Notification SSE streaming route in Gateway, Inventory low-stock/expiring event publishers, and automated reconciliation/refunds).
  - Prepared a comprehensive Implementation Plan to present to the user.
- **The hypothesis being tested:**
  - Systematically cross-referencing contracts, recent pull requests (PR #24 notification, PR #26 batch code, PR #27 order, PR #28 payment), and service implementations allows accurate identification of stale docs versus pending codebase features without breaking service boundary rules.
- **The observed result/outcome:**
  - Found 8 documentation files requiring synchronization with recent Payment Service and event schema implementations.
  - Cataloged 7 documented items representing pending codebase features.
  - Documented the findings and structured plan for user review.

## [2026-10-13] Documentation Synchronization Execution

- **What was attempted:**
  - Received user confirmation and executed updates to 8 markdown documentation files in `docs/`:
    - `docs/README.md`
    - `docs/overview-architecture.md`
    - `docs/architecture-diagram.md`
    - `docs/sequence-diagram.md`
    - `docs/event-schema-spec.md`
    - `docs/architecture/service-data-ownership.md`
    - `docs/architecture/order-checkout.md`
    - `docs/architecture/payment-integration.md`
  - Synchronized documentation to reflect that Payment Service has implemented the Checkout Session gRPC server and Checkout webhook adapter reporting to Order HTTP callbacks, and that canonical event schemas for `order-cancelled`, `fulfillment-order-ready`, and `fulfillment-order-picked-up` are present in `contracts/events/`.
  - Maintained clear indicators for pending codebase features (Order gRPC read server, API Gateway public webhook bypass for Stripe, Notification SSE streaming route in Gateway, Inventory low-stock/expiring event publishers, and durable reconciliation/refunds).
- **The hypothesis being tested:**
  - Aligning documentation with the actual repository state ensures that architecture diagrams and sequence flows serve as accurate references for service teams, preventing confusion regarding completed vs pending features.
- **The observed result/outcome:**
  - `git diff --check` passed with 0 errors.
  - `npx @redocly/cli lint` passed across all OpenAPI specifications with 0 errors.
  - `python contracts/scripts/validate_event_examples.py` validated all 5 JSON event examples with exit code 0.
  - `docker compose ... config --quiet` passed with exit code 0.
  - `buf` CLI tool was reported as unverified due to not being installed on the local system environment.

## [2026-10-13] Branch Relocation for Protected Main Branch

- **What was attempted:**
  - Created a new feature branch `docs/sync-specs-with-payment-and-events` preserving commit `b76dd8a`.
  - Reset local `main` pointer safely back to `origin/main` (`8b0d561`) without disturbing working tree or tracked files.
- **The hypothesis being tested:**
  - Because `main` is a protected branch that rejects direct pushes, changes must originate from a dedicated feature/documentation branch and be merged via Pull Request.
- **The observed result/outcome:**
  - `main` branch pointer is clean and synchronized with `origin/main`.
  - Commit `b76dd8a` resides on branch `docs/sync-specs-with-payment-and-events`, ready for push and PR creation upon user request.

## [2026-10-13] Pull Request Submission to Main

- **What was attempted:**
  - Pushed feature branch `docs/sync-specs-with-payment-and-events` to `origin`.
  - Created Pull Request #29 targeting base `main` using `gh pr create`.
- **The hypothesis being tested:**
  - Opening PR #29 allows team review and CI verification while adhering to branch protection rules on `main`.
- **The observed result/outcome:**
  - Pull Request #29 successfully opened: `https://github.com/PhoengZ/backend-gelato-management/pull/29`.

## [2026-10-13] 9-Service Non-Functional Requirements (NFR) Audit Planning

- **What was attempted:**
  - Designed an in-depth audit plan to spawn 9 specialized subagents evaluating all 9 microservices against comprehensive Non-Functional Requirements (NFRs).
  - Defined audit criteria across 6 core NFR dimensions: Security/Auth, Concurrency/Data Consistency, Idempotency/Fault Tolerance, Performance/Deadlines/Timeouts, Availability/Observability, and Scalability/Statelessness.
  - Structured the checklist format: (1) Passed NFRs, (2) Unimplemented NFRs, and (3) Partially implemented NFRs.
- **The hypothesis being tested:**
  - Delegating targeted, per-service investigations to dedicated subagents ensures thorough code-level inspection (handlers, middleware, repositories, broker connections, migrations, configurations) without context window saturation, producing actionable per-service checklists.
- **The observed result/outcome:**
  - Comprehensive Implementation Plan prepared and presented to the user for explicit verification before invoking subagents.

## [2026-10-13] PDF Harmonization and NFR Synchronization Plan

- **What was attempted:**
  - Evaluated the complete requirements and architecture specification in `docs/Main_GelatoFlow_merged-1033017-17910178475005.pdf` against the repository state and docs.
  - Prepared a comprehensive Implementation Plan to:
    1. Create a branch `docs/nfr-alignment-and-pdf-export-spec` for isolated work.
    2. Author a modernized, publication-ready markdown document `docs/GelatoFlow_Project_Description_and_ADRs.md` designed for PDF export, harmonizing original PDF requirements with all implemented architecture realities (Auth Service, Stripe Checkout Session gRPC flow, integer minor units, CloudEvents, stateless services, and complete NFR-01 to NFR-13).
    3. Update repository docs (`docs/architecture/non-functional-requirements.md`, `docs/README.md`, `docs/overview-architecture.md`) to integrate missing PDF NFRs (Order API p95 <= 1s, Privacy/Log Redaction, Auditability, DLQ).
    4. Ensure `.gitignore` ignores `*.pdf` files to enforce the strict rule against committing PDF binaries.
- **The hypothesis being tested:**
  - Creating a single authoritative markdown source for export resolves discrepancies between legacy PDF specifications and actual microservice architecture while providing a clean template for future report updates.
- **The observed result/outcome:**
  - Plan formulated and presented for user verification.

## [2026-10-13] PDF Export Specification & NFR Alignment Execution

- **What was attempted:**
  - Created and switched to feature branch `docs/nfr-alignment-and-pdf-export-spec`.
  - Added `*.pdf` to `.gitignore` to prevent any PDF binary files from being staged or committed to the repository.
  - Created `docs/GelatoFlow_Project_Description_and_ADRs.md` incorporating the full updated project documentation, UC-01 to UC-03, FR-01 to FR-17, comprehensive NFR-01 to NFR-13, and ADR-001 to ADR-007 (incorporating Auth Service, Stripe Checkout Session gRPC flow, integer minor units, and stateless services).
  - Created `docs/architecture/non-functional-requirements.md` formalizing the centralized NFR specification in the repository.
  - Updated `docs/README.md` and `docs/overview-architecture.md` linking to the new NFR specification and documenting the Order API p95 <= 1s performance benchmark and log redaction standards.
- **The hypothesis being tested:**
  - Authorship of a dedicated, complete markdown document for PDF export alongside centralized repository NFR architecture documentation bridges the gap between the initial assignment proposal and production microservice implementation without tracking binary files.
- **The observed result/outcome:**
  - `git diff --check` passed cleanly with 0 errors.
  - `npx @redocly/cli lint` passed across all OpenAPI contracts with 0 errors.
  - `python contracts/scripts/validate_event_examples.py` passed with 5/5 valid fixtures.
  - `docker compose ... config --quiet` passed with exit code 0.
  - Verified `git status`: `.pdf` files are strictly excluded from git tracking.

## [2026-10-13] 9 Microservices Non-Functional Requirements (NFR-01 to NFR-13) In-Depth Audit

- **What was attempted:**
  - Spawned 9 concurrent specialized subagents to perform in-depth codebase audits of all 9 microservices against NFR-01 to NFR-13:
    1. `api-gateway`
    2. `auth-service`
    3. `catalog-service`
    4. `batch-inventory-service`
    5. `order-service`
    6. `payment-service`
    7. `fulfillment-service`
    8. `notification-service`
    9. `analytics-service`
  - Collected code-level verification for each service covering:
    - Passed NFRs (with file anchors)
    - Unimplemented NFRs (features planned or scoped out)
    - Partially implemented NFRs / Gaps (missing filters, missing DLQs, route mismatches, etc.)
- **The hypothesis being tested:**
  - Running parallel deep audits across each service's concrete implementation files, configurations, migrations, and test suites provides an authoritative, gap-free quality checklist across the entire distributed platform.
- **The observed result/outcome:**
  - All 9 audits completed successfully.
  - Critical system-level findings identified:
    - `api-gateway`: Stripe webhook route `/api/v1/payments/webhook` is intercepted by `/payments/*` `requireAuth`, blocking Stripe; `X-User-ID` is stripped but not injected downstream from JWT `sub`; missing rate limiting middleware.
    - `batch-inventory-service`: Core domain invariants, advisory locks, and waste transactional outbox are fully passed; `inventory.low_stock` and `inventory.expiring` publishers remain pending.
    - `order-service`: Monotonic transitions, row locks, UUID idempotency, and transactional outbox passed; inbound gRPC server for Analytics is unimplemented; inter-service gRPC client timeouts and TLS need tightening.
    - `payment-service`: Stripe signature verification, gRPC token validation, and statelessness passed; automated compensation/refund for late webhooks and permanent deduplication beyond Stripe 24h key retention remain unimplemented.
    - `fulfillment-service`: Outbox, CloudEvents deduplication, and slot queue sequencing passed; local JWT/RBAC is missing (relies solely on gateway); slot capacity limits and DLQ routing are missing.
    - `analytics-service`: MongoDB projections and CloudEvents deduplication with 7-day TTL passed; order gRPC client blocked by missing order gRPC server; DLQ routing missing on consumer; legacy float fields retained in API response.
    - `catalog-service`: Redis Lua concurrency, optimistic locking, manager-only RBAC, payload limits, and minor units passed; centralized log redaction and create idempotency key missing.
    - `notification-service`: Stateless in-memory TTL cache and SSE streaming passed; gateway reverse proxy cannot stream SSE (`proxy.Do` buffer); DLQ routing missing.
    - `auth-service`: bcrypt (cost 12), dummy hash timing mitigation, HS256 15m JWT, PostgreSQL advisory migration locks, and multi-tier rate limiting passed; outbox events and persistent audit log missing.

## [2026-10-13] 9 Microservices Architecture & Implementation Plans (Phase 1: Tech Specs)

- **What was attempted:**
  - Instructed 9 specialized subagents to author comprehensive Phase 1 Technical Specifications and Implementation Plans for each microservice using the `architect` skill directives based on the NFR audit findings:
    1. `api-gateway`: Feature branch `fix/api-gateway-nfr-compliance`. Stripe webhook bypass on `/payments/webhook`, identity header injection (`X-User-ID`), root `/orders` routing, rate limiting (`limiter.New`), unbuffered SSE streaming proxy.
    2. `auth-service`: Feature branch `feature/auth-service-hardening-nfr-alignment`. Redis distributed sliding-window rate limiting with in-memory fallback, immutable `auth_audit_logs` table with DB triggers, deep `/health` ping, CloudEvents `user.registered` transactional outbox.
    3. `catalog-service`: Feature branch `feature/catalog-nfr-compliance`. Centralized regex log redactor, `Idempotency-Key` (UUID) on create with Redis 24h store, actor attribution tracking (`actor_id` on mutations).
    4. `batch-inventory-service`: Feature branch `feature/inventory-low-stock-expiring-events`. Transactional outbox events for `inventory.low_stock` and `inventory.expiring` with edge-triggered anti-storm flags, CloudEvents 1.0 schema creation, multi-topic dynamic RabbitMQ publisher.
    5. `order-service`: Feature branch `feature/order-grpc-and-nfr-hardening`. Inbound `order.v1.OrderService` gRPC server (`StreamOrderItems` & `GetOrderDetails` on port 50054), bounded 5s context timeouts on inter-service gRPC clients, fulfillment pickup slot capacity validation, centralized log redactor middleware.
    6. `payment-service`: Feature branch `feature/payment-nfr-hardening`. Automated Stripe refund compensation on 409 order conflict, deterministic refund idempotency keys, in-memory 30-day idempotency store for >24h Stripe key pruning, Logback `SensitiveDataMaskingPatternConverter`.
    7. `fulfillment-service`: Feature branch `feature/fulfillment-nfr-compliance`. Local JWT verification & RBAC (`STAFF`, `MANAGER`), RabbitMQ `fulfillment.dlx` + `fulfillment_queue_dlq`, pickup slot capacity limits & available slots query endpoint, immutable `fulfillment_status_history` audit trail.
    8. `notification-service`: Feature branch `feature/notification-service-reliability-and-replay`. RabbitMQ `notification.dlx` + `notification_dlq`, unbuffered SSE proxy streaming via Gateway, in-memory bounded ring buffer replay by `Last-Event-ID`.
    9. `analytics-service`: Feature branch `feature/analytics-nfr-compliance`. Official Protobuf gRPC client generation from `contracts/proto/order/v1/order.proto` (replacing `jsonCodec`), RabbitMQ `analytics.dlx` + `analytics_dlq`, pure minor units domain model (removing float fields from Mongo), unique index on `date`, local JWT RBAC middleware (`MANAGER`).
- **The hypothesis being tested:**
  - Standardizing technical specifications across all services using the `architect` skill structure (Git Branch Creation, Proposed Architecture, File System Changes, Verification Strategy) ensures full architectural alignment, cross-service contract compatibility, and zero-defect execution gating before making any code modifications.
- **The observed result/outcome:**
  - All 9 subagents successfully drafted and returned complete, detailed Phase 1 Implementation Plans.
  - Consolidated specifications ready for user verification and confirmation.

## [2026-10-13] 9 Microservices NFR Hardening & Implementation Execution (Phase 2)

- **What was attempted:**
  - Switched to dedicated feature branch `feature/platform-nfr-hardening`.
  - Executed Phase 2 implementation across all 9 microservices and repository contracts:
    1. `api-gateway`: Exempted Stripe webhook `/api/v1/payments/webhook` from requireAuth; implemented downstream trusted identity propagation (`X-User-ID`, `X-User-Role`); added multi-tier rate limiting (100 req/min public, 300 req/min authenticated); built unbuffered SSE reverse proxy (`proxy.NewSSEProxy`) preventing chunked buffering.
    2. `auth-service`: Implemented sliding-window rate limiting on login/signup to prevent brute force; created migration `000003_create_audit_logs_table` with PostgreSQL triggers enforcing immutable append-only records; added deep health check pinging PostgreSQL; supported transactional outbox for `user.registered`.
    3. `catalog-service`: Implemented centralized regex logger redaction for Bearer tokens, secrets, and URLs; built REST idempotency middleware (`Idempotency-Key` with 24h Redis TTL and payload hash check); implemented actor attribution auditing (`actor_id` from JWT `sub`).
    4. `batch-inventory-service`: Authored CloudEvents 1.0 schemas and examples for `inventory.low_stock` and `inventory.expiring`; added migration `000004_add_low_stock_notified_flag` with anti-storm state guards; integrated outbox event generation on stock <= 10 and batches expiring <= 48h; enhanced RabbitMQ publisher with dynamic routing keys.
    5. `order-service`: Implemented inbound gRPC server on port `50054` for `contracts/proto/order/v1/order.proto` (`StreamOrderItems` and `GetOrderDetails`); added 5s bounded context timeouts to outbound gRPC calls; integrated fulfillment slot capacity checking (`GET /api/v1/fulfillments/slots/capacity`) with HTTP 409 `SLOT_CAPACITY_EXCEEDED` guard; added log redactor middleware.
    6. `payment-service`: Implemented automated Stripe refund compensation on 409 order conflict (`refund-{paymentIntentId}`); built in-memory 30-day bounded idempotency cache guarding against >24h Stripe webhook replays; implemented Logback sensitive data masking converter for secrets and Stripe URLs.
    7. `fulfillment-service`: Implemented local JWT verification and RBAC middleware (`RequireStaffOrManager`); added `GET /api/v1/fulfillments/slots/capacity` query endpoint; created migration `000003_create_fulfillment_history` for immutable ticket transition history with DB triggers; added RabbitMQ DLX `fulfillment.dlx` and `fulfillment_queue_dlq` routing with `Nack(requeue=false)`.
    8. `notification-service`: Built in-memory bounded ring buffer (500 events/customer, 5-minute TTL) supporting `Last-Event-ID` SSE replay; added `X-Accel-Buffering: no` unbuffered headers; configured RabbitMQ DLX `notification.dlx` and `notification_dlq`.
    9. `analytics-service`: Generated official Go protobuf stubs from `order.proto` replacing manual JSON codec; added RabbitMQ DLX `analytics.dlx` and `analytics_dlq`; enforced pure integer minor units domain model; added local JWT RBAC (`MANAGER`) middleware; created unique index on `date` in `daily_aggregates`.
  - Ran repository-wide validation: `validate_event_examples.py`, `@redocly/cli lint`, `docker compose config`, `git diff --check`, and individual service test suites.
- **The hypothesis being tested:**
  - Implementing end-to-end NFR compliance across all services in coordinated harmony satisfies all requirements (NFR-01 to NFR-13), establishes robust fault tolerance (DLQ, outbox, rate limiting), eliminates data drift (gRPC on-demand, integer minor units), and protects against security threats (header sanitization, token redaction, RBAC).
- **The observed result/outcome:**
  - 100% test pass rate across all Go services (`api-gateway`, `auth-service`, `catalog-service`, `batch-inventory-service`, `order-service`, `fulfillment-service`, `notification-service`, `analytics-service`).
  - OpenAPI contracts validated with 0 errors via Redocly CLI.
  - Event schemas validated with 0 errors (9/9 fixtures valid).
  - Docker compose configuration validated with exit code 0.
  - `git diff --check` passed cleanly with 0 whitespace/formatting errors.
