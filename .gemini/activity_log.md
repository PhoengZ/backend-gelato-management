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