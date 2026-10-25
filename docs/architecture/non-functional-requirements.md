# Non-Functional Requirements Specification

This document defines the authoritative Non-Functional Requirements (NFRs) and Quality Attributes for the GelatoFlow platform. It harmonizes the original project charter and progress deliverable (Project Progress 2 PDF, Section 8, page 11: **NFR-01 through NFR-09**) with the repository architecture extensions (**NFR-10 through NFR-13**) and concrete microservice implementations across the codebase.

---

## 1. NFR Master Summary Matrix

| ID | Quality Attribute | Target Metric / Measurable Criterion (PDF & Repo Specification) | Implementation Mechanisms & Microservices | Verification & Enforcement Suite |
|:---:|:---|---|---|---|
| **NFR-01** | **Consistency** *(Data Invariant)* | ทุก batch ต้องรักษา Invariant:<br>$$\text{Available} = \text{Produced} - \text{Reserved} - \text{Sold} - \text{Waste} \ge 0$$<br>ไม่มียอดคงเหลือติดลบเด็ดขาด | • **`batch-inventory-service`**: PostgreSQL `CHECK` constraints on portions, atomic transactions, row-level locks, no `SKIP LOCKED`. | • `batch-inventory-service/tests/`<br>• `pytest tests/test_uc03_batch_inventory.py` |
| **NFR-02** | **Concurrency Reliability** | เมื่อมีสินค้าคงเหลือ $Available = 1$ portion และมี **20 concurrent order requests** แย่งจองพร้อมกัน ระบบต้องตัดจองสำเร็จเพียง **1 รายการ** และ Available ต้องเหลือ 0 | • **`batch-inventory-service`**: Deterministic flavor advisory locks (`pg_advisory_xact_lock`) + batch row locks (`SELECT ... FOR UPDATE`).<br>• **`order-service`**: Synchronous gRPC reservation before checkout. | • `pytest tests/test_nfr02_concurrency.py`<br>• `go test -race ./...` in `batch-inventory-service` |
| **NFR-03** | **Performance & Latency** | Order creation API ต้องมีค่า Latency **$p95 \le 1.0\text{ s}$** ภายใต้ภาระงาน MVP load ที่ตกลงกัน | • **`order-service`**: Bounded 5s gRPC client deadlines.<br>• **`catalog-service`**: In-memory Redis active catalog caching.<br>• **`batch-inventory-service`**: Indexed queries; no remote network calls while DB locks held. | • Automated latency benchmarks in `tests/`<br>• Profiling and APM latency histograms |
| **NFR-04** | **Availability & Fault Isolation** | ความล้มเหลวของ Downstream services (เช่น Notification Service หรือ RabbitMQ ล่ม) ต้อง**ไม่ทำให้ confirmed order สูญหายหรือล้มเหลว** | • **`order-service`**: Transactional Outbox pattern; order state commits to PostgreSQL `PAID` locally before async publication.<br>• **`notification-service`**: Decoupled asynchronous consumer. | • `pytest tests/test_uc01_preorder_checkout.py` with stopped notification container |
| **NFR-05** | **Reliability & Messaging** | การส่งข้อมูลแบบ Asynchronous ผ่าน RabbitMQ ต้องใช้ **Transactional Outbox**; Consumer ต้องรองรับ **At-least-once delivery**, ทำ **Idempotent Deduplication** ผ่าน CloudEvents `id`, และมี **Dead Letter Queue (DLQ)** | • **Producers** (`order`, `inventory`, `fulfillment`, `auth`): Transactional outbox with publisher confirms and reconnect loops.<br>• **Consumers** (`fulfillment`, `notification`, `analytics`): UUID `id` deduplication, RabbitMQ DLX `*.dlx` + `*_dlq`, and `Nack(requeue=false)`. | • `python contracts/scripts/validate_event_examples.py`<br>• `pytest tests/test_fulfillment_nfr.py`<br>• RabbitMQ DLQ queue verification |
| **NFR-06** | **Security & Access Control** | ใช้ **TLS 1.2+**, Role-based authorization (**RBAC**: `CUSTOMER`, `STAFF`, `MANAGER`), Adaptive password hashing (**bcrypt cost 12**); **ไม่เก็บ plaintext password หรือข้อมูลบัตรเครดิต** | • **`auth-service`**: bcrypt password hashing, timing-attack mitigation on non-existent accounts, short-lived signed JWTs (15m, HS256).<br>• **`api-gateway`**: Identity header sanitization, public route protection.<br>• **Transports**: Opt-in TLS 1.2+ across Auth, Catalog, and Inventory. | • `auth-service/internal/transportsecurity/tls_test.go`<br>• `pytest tests/test_uc02_kitchen_pickup.py` (RBAC boundary checks) |
| **NFR-07** | **Privacy & Log Redaction** | Application Log ต้อง **Redact ข้อมูลสำคัญ** เช่น Bearer Access Tokens, Machine Secrets, Stripe Webhook Signatures, raw DSNs, และข้อมูลส่วนบุคคล (PII) | • **`api-gateway`**: Header/cookie sanitizing access logger.<br>• **`payment-service`**: Logback `SensitiveDataMaskingPatternConverter` masking Stripe keys, URLs, and signatures.<br>• **Go services**: Bounded diagnostic errors excluding DSNs and panic text. | • `payment-service` unit tests: `SensitiveDataMaskingPatternConverterTest`<br>• `tests/process_privacy_test.go` in `auth-service` |
| **NFR-08** | **Auditability** | ทุกการปรับปรุง Batch, การบันทึก Waste, การแก้ไขสารก่อภูมิแพ้ (Allergen), และ Privileged Actions ต้องมี **Audit Record ตรวจย้อนหลังได้พร้อมระบุตัวตน (Actor)** | • **`batch-inventory-service`**: Immutable `inventory_movements` (Migration 005) with verified `actor_id` and `actor_role`, plus `waste_records`.<br>• **`catalog-service`**: Atomic Redis mutation audit trail.<br>• **`auth-service`**: Immutable `auth_audit_logs` with triggers.<br>• **`fulfillment-service`**: Immutable `fulfillment_status_history`. | • `batch-inventory-service/tests/production_audit_test.go`<br>• `pytest tests/test_fulfillment_nfr.py` |
| **NFR-09** | **Modifiability & Boundary** | Service อื่นต้องเข้าถึงข้อมูลผ่าน **API/gRPC Contract ของ Service เจ้าของข้อมูลเท่านั้น** ห้ามอ่าน/เขียน Private Database ของ Service อื่น และ**ห้ามทำ Duplicate/Shadow Tables** | • **Architecture**: Database-per-service isolation (PostgreSQL Auth, Redis Catalog, PostgreSQL Order, PostgreSQL Inventory, PostgreSQL Fulfillment, MongoDB Analytics, Stateless Payment & Notification).<br>• **Cross-service queries**: Synchronous gRPC on demand. | • `buf lint contracts` & `buf build contracts`<br>• `docker compose -f infra/docker/compose.yml config` boundary inspection |
| **NFR-10** | **Monetary Precision** *(Repo Extension)* | ข้อมูลจำนวนเงินทั้งหมดต้องเก็บและคำนวณในรูป **Integer Minor Units** (Satang ใน THB) ร่วมกับระบุ ISO 4217 Currency อย่างชัดเจน **ห้ามใช้ Floating-point decimals** | • **All services**: `int64` minor unit data models (`price_minor`, `total_amount_minor`, `cost_lost_minor`, `revenue_minor`), explicit string currency (`THB`). | • `npx @redocly/cli lint contracts/openapi/*.yaml`<br>• Model test assertions |
| **NFR-11** | **Idempotency Standard** *(Repo Extension)* | ทุก Mutation REST Endpoint ต้องรองรับ Header **`Idempotency-Key` (UUID)**; การส่งคำขอเดิมซ้ำต้อง Replay ผลลัพธ์เดิมที่สำเร็จ; ส่งคำขอ payload ขัดแย้งกันด้วย key เดิมต้องตอบกลับ **HTTP 409 Conflict** | • **`order-service`**: Order creation idempotency.<br>• **`batch-inventory-service`**: Reservation & waste idempotency.<br>• **`catalog-service`**: Flavor creation idempotency with Redis 24h key store.<br>• **`payment-service`**: Deterministic refund/session idempotency keys. | • `pytest tests/test_uc01_preorder_checkout.py`<br>• `pytest tests/test_uc03_batch_inventory.py` |
| **NFR-12** | **Stateless Architecture** *(Repo Extension)* | **Payment Service** และ **Notification Service** ต้องเป็น **Stateless Service** โดยไม่ถือครอง Local Database ของตนเอง เพื่อรองรับการ Restart และ Horizontal Scaling ได้อิสระ | • **`payment-service`**: Stateless adapter translating Stripe webhooks to Order callbacks.<br>• **`notification-service`**: Stateless SSE adapter with bounded in-memory ring buffer (500 events/customer, 5-min TTL) replayable by `Last-Event-ID`. | • Container restart tests (`docker restart payment-service notification-service`)<br>• SSE reconnect replay checks |
| **NFR-13** | **Network Isolation** *(Repo Extension)* | พอร์ตของฐานข้อมูลระดับ Operational (PostgreSQL, Redis, MongoDB) และ RabbitMQ Broker ต้องถูก**จำกัดสิทธิ์ให้อยู่ภายใน Docker Internal Network (`gelato_network`)** เท่านั้นใน Production | • **`infra/docker/compose.yml`**: Database and broker services omit `ports:` host mappings; communication is restricted to the internal bridge network. Dedicated `compose.dev.yml` provides opt-in host port exposure for local testing only. | • `docker compose --env-file infra/.env.example -f infra/docker/compose.yml config`<br>• Port scanning audits |

---

## 2. Microservice Implementation Controls & Evidence

### 2.1 Auth Service
- **Authentication & Roles (NFR-06):** Issues HMAC-SHA256 JWTs (15m expiry, `iss: gelatoflow-auth`, `aud: gelatoflow-api`) containing UUID `sub` and validated roles (`CUSTOMER`, `STAFF`, `MANAGER`). Password verification uses bcrypt (cost 12) with dummy hash comparison to eliminate username enumeration timing leaks.
- **Audit Logging (NFR-08):** Appends to `auth_audit_logs` table via PostgreSQL triggers enforcing append-only immutability (prohibiting `UPDATE` and `DELETE`). Captures client IP, User-Agent (bounded to 255 chars), and event types (`LOGIN_SUCCESS`, `LOGIN_FAILURE`, `USER_REGISTERED`).
- **Transport Security (NFR-06):** Supports opt-in TLS via `HTTP_TLS_CERT_FILE` and `HTTP_TLS_KEY_FILE` with TLS 1.2 minimum floor.
- **Privacy & Diagnostics (NFR-07):** Startup and migration error diagnostics emit sanitized fixed text and error categories, completely omitting raw database connection strings (DSNs) and credentials.

### 2.2 Catalog Service
- **Metadata Ownership & Caching (NFR-03, NFR-09):** Serves active flavor profiles, portion prices, and allergen flags from Redis. Public endpoints hide archived flavors and internal recipes.
- **Atomic Mutation Auditing (NFR-08):** Flavor updates and creations atomically append verified actor attribution (`actor_id`, `actor_role`), timestamp, and before/after allergen/active states in a Redis transaction.
- **Idempotent Registration (NFR-11):** Mutation requests support `Idempotency-Key` (UUID); the payload hash, response, and audit log commit atomically with a 24-hour Redis TTL.
- **Transport Security (NFR-06):** Independent opt-in TLS 1.2+ configuration for HTTPS (port 3002) and gRPC (port 50052).
- **Log Masking (NFR-07):** Request and transport diagnostic loggers redact Bearer tokens and sensitive query parameters.

### 2.3 Batch Inventory Service
- **Stock Invariant Invariant (NFR-01):** Enforces $Available = Produced - Reserved - Sold - Waste \ge 0$ at all times using database `CHECK (available_portions >= 0)` constraints.
- **Advisory Lock Concurrency (NFR-02):** Serializes concurrent reservation requests per flavor using PostgreSQL transaction-scoped advisory locks (`pg_advisory_xact_lock(hashtext('flavor:' || flavor_id))`) and row-level locks (`SELECT ... FOR UPDATE`). Bypasses `SKIP LOCKED` to prevent artificial stock-out reports.
- **Actor Attribution in Audit Trails (NFR-08):** Migration 005 (`005_movement_actor.sql`) binds verified `actor_id` and `actor_role` to every stock movement (`PRODUCED`, `WASTED`).
- **Transactional Outbox (NFR-04, NFR-05):** Outbox table commits within the stock mutation transaction; outbox dispatcher publishes `inventory.waste` with publisher confirms and mandatory routing.
- **Transport Security (NFR-06):** Opt-in TLS 1.2+ for HTTPS, inbound gRPC, and outbound Catalog gRPC client (`CATALOG_GRPC_TLS_ENABLED=true`).

### 2.4 Order Service
- **Synchronous gRPC Orchestration (NFR-03, NFR-09):** Calls Catalog (`BatchGetFlavors`) and Batch Inventory (`ReservePortions`) via gRPC bounded by strict 5-second deadlines.
- **Outbox Commitment (NFR-04, NFR-05):** Upon verified payment callback, commits order status `PAID` and `order.placed` CloudEvent atomically to PostgreSQL Order DB.
- **Idempotency Protection (NFR-11):** Verifies incoming `Idempotency-Key` and compares SHA-256 request payload hash. Matching requests return existing order details; mutated requests with identical keys are rejected with HTTP 409 Conflict.
- **Monetary Precision (NFR-10):** Computes all subtotals and totals in Satang (`int64` minor units) in `THB`.

### 2.5 Payment Service
- **Stateless Integration (NFR-12):** Maintains zero local database state; translates Stripe Checkout Sessions and signed webhooks (`Stripe-Signature`).
- **Automated Compensation / Refund (NFR-04):** Automatically issues idempotent Stripe refunds (`refund-{payment_intent_id}`) when Order rejects late payment callbacks due to expired reservations (HTTP 409 Conflict).
- **Log Redaction (NFR-07):** Configures Logback `SensitiveDataMaskingPatternConverter` to automatically mask Stripe secret keys (`sk_test_*`, `rk_test_*`), webhook secrets (`whsec_*`), and Stripe checkout session URLs.

### 2.6 Fulfillment Service
- **Slot Capacity Enforcement (NFR-01, NFR-03):** Allocates kitchen tickets within 15-minute pickup slots, strictly enforcing a maximum capacity limit (10 orders per slot) with HTTP 409 `SLOT_CAPACITY_EXCEEDED` guard.
- **Audit History (NFR-08):** Records all ticket transitions (`QUEUED` -> `PREPARING` -> `READY_FOR_PICKUP` -> `COMPLETED`) in immutable `fulfillment_status_history` table.
- **Messaging Resilience & DLQ (NFR-05):** RabbitMQ consumer verifies CloudEvents `id` against processed event log; poison messages route to `fulfillment.dlx` and `fulfillment_queue_dlq`.

### 2.7 Notification Service
- **Stateless In-Memory Replay (NFR-12):** Maintains a bounded in-memory ring buffer (500 events per customer, 5-minute TTL) allowing web clients to reconnect and resume dropped SSE streams via `Last-Event-ID`.
- **SSE Streaming Optimization (NFR-03):** Sends `X-Accel-Buffering: no` and explicit keepalive heartbeats preventing reverse proxy chunk buffering.
- **Fault Isolation & DLQ (NFR-04, NFR-05):** Consumes `order.*` and `fulfillment.*` events; failed deliveries route to `notification.dlx` and `notification_dlq` without impacting order placement.

### 2.8 Analytics Service
- **Eventual Consistency & Deduplication (NFR-05, NFR-09):** Consumes domain events from RabbitMQ and performs idempotent deduplication against `processed_events` collection with a 7-day TTL index.
- **Monetary Precision (NFR-10):** Ingests and aggregates metrics exclusively in integer minor units (`gross_sales_minor`, `cost_lost_minor`, `revenue_minor`), eliminating floating-point rounding drifts.
- **Dead Letter Routing (NFR-05):** Unprocessable messages route to `analytics.dlx` and `analytics_dlq`.

### 2.9 API Gateway
- **Single Entry Point & Coarse RBAC (NFR-06):** Centralizes ingress routing on `/api/v1/*`, verifies HS256 JWT signatures, and enforces role boundaries.
- **Header Sanitization (NFR-06):** Strips untrusted client headers (`X-User-*`, `X-Role*`) and securely injects verified identity headers (`X-User-ID`, `X-User-Role`) extracted from the verified token.
- **Rate Limiting (NFR-03):** Implements multi-tier in-memory rate limiting (100 req/min for public unauthenticated endpoints, 300 req/min for authenticated clients).
- **Privacy & Logging (NFR-07):** Sanitizes access logs to redact authorization headers and cookie tokens.
