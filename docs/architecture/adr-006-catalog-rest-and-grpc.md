# ADR-006 — Expose Catalog through REST and gRPC using shared application logic

Date: 2026-09-27

Status: Proposed for affected service-owner review. Catalog implementation is
available on the local development branch; this is not a team acceptance,
deployment, or Order/Inventory integration claim.

## Context

Catalog owns flavor metadata, prices, recipes, allergens and active status in
Redis. Its REST CRUD is already implemented. Internal callers need an
authoritative metadata interface without reading Catalog storage directly.
Order could validate flavor availability for sale and price before reserving
stock; Inventory could validate a flavor before creating a production batch.
The progress milestone also requires a demonstrable gRPC CRUD service.

The original proposal ADR-003 selected gRPC for Order-to-Inventory reservation;
ADR-005 retained REST through Gateway for clients. Neither mandated Catalog gRPC
CRUD. This decision extends Catalog's interface while preserving those decisions
and Inventory's exclusive ownership of stock.

## Decision

- Keep public Catalog REST through Gateway and add a protobuf gRPC listener in
  the same Catalog process. Both transports call one CatalogService and one
  RedisFlavorRepository; there is no second Catalog database or business-rule copy.
- Define catalog.v1 in contracts/proto/catalog/v1/catalog.proto. Generate Go
  interfaces reproducibly into the Catalog module, leaving other services' code
  generation and implementations unchanged.
- Expose unary CRUD plus BatchGetFlavors. UpdateFlavor fully replaces editable
  fields, matching REST PUT. DeleteFlavor archives, matching REST DELETE. There
  is no partial-update RPC in version 1.
- Verify the original JWT in each transport. Writes and explicit archived-list
  filters require Manager. Public read projections hide inactive records and
  recipes. Invalid supplied tokens are rejected, not treated as anonymous.
- Use deadlines, bounded payloads, standard status codes and ErrorInfo. Batch
  reads preserve order and return no partial success; they are not an atomic
  price snapshot. Do not automatically retry ambiguous mutations.
- Keep local development plaintext bound to loopback by default. Exposing this
  listener beyond isolated development requires agreed TLS and network controls.

## Consequences and alternatives

Shared application logic preserves REST/gRPC validation, price, archive and
concurrency behavior. Protobuf gives consumers generated interfaces. Maintenance
now includes two transport contracts, interceptors, mappings and regression tests.
Synchronous Catalog calls would add a checkout dependency and need a failure
policy. REST-only internal calls would avoid a second transport, but would not
follow the repository's current internal-gRPC direction or satisfy the intended
gRPC CRUD progress demo. A separate Catalog microservice for gRPC would duplicate
ownership without an independent business capability.

## Follow-up boundaries

- Order/Inventory owners review and implement their consumers separately. A
  standalone gRPC client demo must not be presented as completed inter-service
  integration. Manager-only write RPCs are administrative capabilities, not a
  reason to give Order write access to Catalog.
- Checkout needs a team decision about storing the price actually agreed at
  purchase. Historical transaction facts must be distinguished from a replicated
  current Catalog entity. The identifier-only wording in service-data-ownership
  is not amended implicitly by this change.
- Inventory must decide whether an archived flavor can receive a new batch.
- Auth machine identities, privileged background callers, TLS deployment,
  pagination and deployment-wide rate limits remain separate integration work.
- This does not implement the Order–Inventory reservation work promised by
  ADR-003, or modify event schemas, broker bindings or teammate service code.

## Verification

Acceptance requires generated client calls over TCP, real Redis CRUD,
cross-transport reads/writes, role/archive/error tests, bounded shutdown and an
actual Catalog executable restart against the same Redis. See Catalog's gRPC
guide and tests. Remote CI and deployment must be verified separately.
