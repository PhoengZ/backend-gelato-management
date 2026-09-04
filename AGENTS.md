# GelatoFlow Backend Agent Guide

This file applies to the entire repository. More specific `AGENTS.md` files may
override it within their own directories.

## Start here

- Inspect `git status` before editing and preserve unrelated work.
- Read `README.md`, `contracts/README.md`, and the relevant files under
  `docs/architecture/` before changing a service boundary.
- Treat `contracts/` as the source of truth. `docs/API_SPEC.md` is a migration
  reference for the frontend demo, not the canonical backend contract.
- Do not claim that a service is implemented merely because its contract or
  infrastructure exists.

## Service and data ownership

- API Gateway owns routing, external authentication enforcement, and rate
  limiting. It owns no business data or database.
- Auth owns users, credential hashes, roles, JWT issuance, and Auth PostgreSQL.
- Catalog owns flavor metadata, price, images, recipe, allergens, active status,
  and Catalog Redis. It must not own or calculate stock.
- Batch Inventory is the only source of truth for batches, expiry, available
  portions, reservations, sold portions, adjustments, and waste. It owns
  Inventory PostgreSQL.
- Order must call Batch Inventory through the versioned gRPC contract. It must
  not read or write Inventory storage directly.
- Analytics consumes asynchronous events and owns its MongoDB projections. It
  must not participate in operational transactions.
- A Gateway or frontend adapter may compose Catalog metadata with Inventory
  availability, but the composed response does not transfer data ownership.

## Contract conventions

- Public REST endpoints are versioned under `/api/v1`; Catalog flavors use
  `/api/v1/flavors` and Inventory endpoints use `/api/v1/inventory/*`.
- Use UUIDs for resource identifiers, `snake_case` for JSON fields, RFC 3339 UTC
  timestamps, and `YYYY-MM-DD` for date-only values.
- Represent money as integer minor units with an explicit currency. Do not use
  floating-point values for persisted or transported monetary amounts.
- Return structured errors with `code`, `message`, and optional `details`.
- Inventory reservations must be atomic, idempotent, and all-or-nothing. Allocate
  expiring stock using FEFO and never allow negative availability.
- Follow the `ACTIVE` to `CONFIRMED`, `RELEASED`, or `EXPIRED` reservation
  lifecycle defined in `docs/architecture/inventory-reservation.md`.
- Use CloudEvents 1.0 structured envelopes and canonical RabbitMQ routing keys,
  including `order.placed`, `order.cancelled`, and `inventory.waste`.
- Producers use a transactional outbox. Consumers must tolerate at-least-once
  delivery and deduplicate by event `id`.

## Change discipline

- Keep implementation changes within the owning service when possible.
- Changes to shared contracts, Gateway routes, event schemas, or broker bindings
  require review from every affected service owner.
- Update contracts and architecture documentation together when changing a
  boundary. Keep examples and infrastructure configuration synchronized.
- Do not silently preserve a legacy shape when it conflicts with `contracts/`;
  document compatibility and migration work explicitly.
- Do not commit generated coverage files, local environment files, credentials,
  or editor artifacts.

## Validation

Run the checks relevant to the changed files before handing work off:

```bash
npx --yes @redocly/cli@2.49.0 lint
buf lint contracts
buf build contracts
python3 contracts/scripts/validate_event_examples.py
docker compose --env-file infra/.env.example \
  -f infra/docker/compose.yml \
  -f infra/docker/compose.dev.yml config --quiet
git diff --check
```

Also run the owning service's formatter and test suite. If a required tool or
runtime is unavailable, report the unverified check instead of treating it as a
pass.
