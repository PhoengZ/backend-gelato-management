# ADR-007 — Implement Inventory transactions and explicit caller boundaries

Date: 2026-09-28
Status: Proposed for affected service-owner review; implementation on the local
Inventory branch. This is not deployment or team-wide integration acceptance.

## Context

Canonical Inventory REST and gRPC contracts already define batch creation,
availability, waste and a reservation lifecycle. PostgreSQL must prevent
overselling with concurrent replicas. Catalog gRPC is now available, while
Order's caller and Auth-issued machine identity contracts remain separate work.

## Decision

- Add one Go Inventory service on REST 3004 / local gRPC 50051 with PostgreSQL 16.
  Use explicit embedded migrations and one application layer for both transports.
- Verify user JWTs locally for REST. Enforce Manager creation and Staff/Manager
  batch reads and waste; public availability rejects malformed supplied tokens.
- Protect every Inventory RPC with a distinct configured Order service token for
  isolated development. A user JWT is not that token. This provisional credential
  does not change Auth or prove payment; Order remains responsible for verified
  payment before ConfirmReservation. Adopt team-reviewed machine identity/TLS
  before broader deployment. Plaintext is confined to local isolated networking.
- Validate an active Catalog flavor through GetFlavor before creating a batch.
  Do not persist Catalog metadata or hold PostgreSQL locks during that RPC.
  This is a point-in-time validation, not a distributed transaction with Catalog:
  a later Catalog archive does not delete existing stock. Order must separately
  enforce sale eligibility when creating an order.
- Use transaction-scoped flavor advisory locks plus row locks and DB constraints.
  Take order locks before sorted flavor locks, then row locks consistently across
  stock-changing paths. FEFO orders expiry, creation, UUID. No SKIP LOCKED for
  stock allocation, so lock contention is not reported as absent inventory.
- Store idempotency hash and original successful response in the stock transaction.
  Same key/request returns that response; changed payload conflicts. V1 permits
  one reservation per order ID, including terminal reservations, to prevent a
  second key double-reserving an order. A new order represents a new attempt.
- Reservation TTL defaults to 10 minutes and is capped at earliest batch expiry.
  Check database time after acquiring locks. Late confirmation fails even before
  the expiry worker runs. Order must reconcile late payment independently.
- Expire durable reservations and batches with periodic workers. A release to an
  expired batch becomes waste. Repeated/concurrent workers cannot double-count it.
- Write waste, audit movement and CloudEvent outbox together. Outbox uses broker
  confirms, mandatory routing and stable IDs under at-least-once delivery.
- Disable event publication by default; explicitly enable against an isolated
  broker or after consumer compatibility/deduplication has been verified.

## Consequences

Independent Inventory replicas preserve stock correctness using PostgreSQL, with
serialization per affected flavor rather than a global application lock. Very hot
flavors can contend; deadlines bound waiting. Unrelated flavors remain independent.
One reservation per order is deliberately restrictive and must be reviewed with
Order before adding retry/reorder workflows. CreateBatch remains non-idempotent
because its v1 contract has no key; ambiguous create failures need reconciliation.

Migrations and tables belong exclusively to Inventory. Gateway already routes
its prefix and needs no code change. The Inventory Catalog client reads existing
Catalog RPCs without changing Catalog's implementation or contract. No teammate
service implementation, shared Compose, or broker permission is changed here.

## Validation and follow-up

Validate real PostgreSQL constraints, FEFO, all-or-nothing multi-flavor allocation,
20 contenders for one portion across instances, same-key concurrency, terminal
races, TTL, worker restart, real TCP/protobuf, actual executable restart, and
RabbitMQ failures/redelivery. CI must fail rather than skip required integration.

Analytics currently parses canonical waste identifiers and costs, but that alone
is not end-to-end or duplicate-safe processing evidence. Coordinate its projection
and date semantics, then test the published event against the real consumer before
turning on the shared producer. This change does not implement Order, payments,
Auth machine identities, low-stock notifications, a video, or a production rollout.
