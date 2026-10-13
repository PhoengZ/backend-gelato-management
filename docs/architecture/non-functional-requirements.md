# Non-Functional Requirements Specification

This document defines the authoritative Non-Functional Requirements (NFRs) and Quality Attributes for the GelatoFlow platform. It aligns business and architectural goals established in the project charter with the running microservices codebase and versioned contracts.

---

## 1. NFR Summary Matrix

| ID | Quality Attribute | Target Metric / Criterion | Implementation Mechanism | Verification & Enforcement |
|:---:|:---:|---|---|---|
| **NFR-01** | **Data Consistency** | $Available = Produced - Reserved - Sold - Waste \ge 0$ | PostgreSQL check constraints and atomic transactions in `batch-inventory-service`. | Unit & integration tests in `batch-inventory-service/tests/`. |
| **NFR-02** | **Concurrency Reliability** | Exactly 1 success out of 20 concurrent requests on 1 portion. | Flavor-scoped PostgreSQL advisory locks and row-level locks (no `SKIP LOCKED` on inventory). | Concurrency race tests; Go `-race` detector suite. |
| **NFR-03** | **Performance & Latency** | Order creation API $p95 \le 1.0\text{ s}$ under agreed MVP peak load. | In-memory Redis catalog cache, lightweight gRPC inter-service calls, indexed PostgreSQL queries. | Load testing suite; APM latency measurements. |
| **NFR-04** | **Availability & Fault Isolation** | Zero order losses or payment failures caused by downstream notification outages. | Asynchronous messaging via RabbitMQ; Order status commits locally before async publication. | Integration tests simulating broker/notification downtime. |
| **NFR-05** | **Reliability & Messaging** | At-least-once message delivery with idempotent consumer deduplication and DLQ routing. | Transactional outbox on producers; consumer event deduplication by CloudEvents `id`; RabbitMQ publisher confirms. | Python event schema validation script; test consumer replay fixtures. |
| **NFR-06** | **Security & Access Control** | Transport Layer Security (TLS); short-lived JWT access tokens (15m); strict RBAC (`CUSTOMER`, `STAFF`, `MANAGER`). | API Gateway identity header sanitization; local JWT claim verification; bcrypt password hashing. | `catalog-service/SECURITY.md`, `api-gateway/tests/`, and `auth-service` test suites. |
| **NFR-07** | **Privacy & Log Redaction** | Zero exposure of bearer tokens, machine secrets, Stripe signatures, or customer PII in logs. | Centralized logging sanitizers; header and token masking across HTTP/gRPC handlers. | Security code reviews; automated log payload inspections. |
| **NFR-08** | **Auditability** | Full historical traceability of stock movements, waste records, allergen edits, and manager actions. | Immutable audit tables (`inventory_movements`, `waste_records`); explicit actor attribution. | Database migration integrity tests; audit log assertions. |
| **NFR-09** | **Modifiability & Isolation** | Database-per-service isolation; zero direct cross-database queries or shadow/duplicate tables. | Microservice encapsulation; on-demand synchronous gRPC queries for cross-service facts. | Architecture boundary reviews; repository dependency linter. |
| **NFR-10** | **Monetary Precision** | Zero floating-point representation; exact integer minor units (Satang) with ISO 4217 currency. | `int64` minor unit data models (`total_amount_minor`, `cost_lost_minor`); string currency (`THB`). | Schema validation via `contracts/openapi/` and unit test assertions. |
| **NFR-11** | **Idempotency Standard** | Replay of identical request returns original response; mutated payload with identical key returns HTTP 409. | UUID `Idempotency-Key` header; hash verification; database-persisted operation idempotency records. | Idempotency handler test suites in Order and Inventory services. |
| **NFR-12** | **Stateless Architecture** | Stateless operational services allowing immediate restart and horizontal scaling without state loss. | In-memory bounded TTL routing for Notification SSE; zero database dependencies in Payment adapter. | Service container restarts without persistent volume dependencies. |
| **NFR-13** | **Network Isolation** | Zero public internet exposure of internal data stores or message brokers in production. | Dedicated Docker bridge networks (`gelato_network`); isolated production compose files. | `docker compose ... config` validation; network port audits. |

---

## 2. Detailed Quality Requirements

### 2.1 Concurrency & Invariant Consistency (NFR-01, NFR-02)
- **Invariant:** The portion balances across all production batches must strictly adhere to:
  $$\text{initial\_portions} = \text{available} + \text{reserved} + \text{sold} + \text{wasted}$$
  where no balance may fall below zero at any point in time.
- **Contention Protection:** Under extreme contention (e.g., flash sales of high-demand flavors), reservation requests are serialized using PostgreSQL transaction-level advisory locks ordered deterministically by Order ID and Flavor ID. The system strictly avoids `SKIP LOCKED` during stock allocation to prevent reporting artificial stock depletion when the inventory is merely locked for concurrent settlement.

### 2.2 Performance and Latency Targets (NFR-03)
- **Response Budget:** The critical path of checkout (`POST /api/v1/orders`) encompasses synchronous gRPC calls to Catalog (price/metadata validation), Inventory (atomic FEFO reservation), and Payment (Checkout Session initialization). Under standard MVP load, the 95th percentile response latency ($p95$) must not exceed 1.0 second.
- **Deadlines & Timeouts:** Every inter-service RPC is bounded by explicit deadlines:
  - Order-to-Payment gRPC: Default 5 seconds, maximum 30 seconds.
  - Payment-to-Order HTTP Callback: Default 5,000 ms (100–30,000 ms allowable range).
  - Order-to-Inventory gRPC: Default 5 seconds.

### 2.3 Reliability, Asynchronous Messaging & DLQ (NFR-04, NFR-05)
- **Transactional Outbox:** All asynchronous domain events (`order.placed`, `order.cancelled`, `fulfillment.order_ready`, `inventory.waste`) are written atomically to the service's database outbox within the same database transaction that commits the domain entity state.
- **At-Least-Once Delivery & Deduplication:** Message delivery through RabbitMQ guarantees at-least-once transmission. Every downstream consumer must record the CloudEvents UUID `id` and perform atomic deduplication. Redelivered events with previously processed IDs must be handled as idempotent no-ops.
- **Poison Message & DLQ Policy:** Messages failing deserialization or invalid payload constraints must be rejected with `Nack(requeue=false)` and routed to a Dead Letter Queue (DLQ) to prevent infinite poison message requeuing loops.

### 2.4 Security, Privacy & Log Redaction (NFR-06, NFR-07)
- **Token Lifecycle:** User tokens issued by Auth Service are short-lived HMAC-SHA256 JWTs with a 15-minute expiration (`exp`), containing strict claims (`sub`, `role`, `iss`, `aud`, `iat`).
- **Identity Sanitization:** The API Gateway strips all client-injected identity headers (`X-User-*`, `X-Role*`, `X-Authenticated-*`) to prevent spoofing and forwards only the verified Bearer token downstream.
- **Log Redaction:** All application loggers must employ masking filters to redact sensitive values:
  - `Authorization: Bearer [REDACTED]`
  - `X-Payment-Service-Token: [REDACTED]`
  - `Stripe-Signature: [REDACTED]`
  - Stripe Checkout HTTPS URLs must never be emitted to public or diagnostic logs.

### 2.5 Monetary Precision and Data Ownership (NFR-09, NFR-10)
- **Integer Minor Units:** All monetary figures persisted in storage or transmitted via API payloads must be expressed as non-negative integer minor units (e.g., Satang in THB). The use of floating-point representations (`float`, `double`, decimal JSON numbers) is strictly prohibited across backend services to eliminate rounding errors.
- **Single Source of Truth:** Direct access to private storage belonging to another service is prohibited. Analytics receives asynchronous event projections, and services query live facts on-demand through versioned gRPC APIs.
