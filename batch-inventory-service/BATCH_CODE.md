# Batch code design and upgrade notes

Proposed feature branch: `batch-code`, based on fetched main `eba37e07` on
11 October 2026 (Asia/Bangkok). The feature is separate from the UUID-only owned
demo. Opening this branch for review does not upgrade any existing team/demo database.

## Resource identity and labels

A batch has two distinct values:

| Field | Purpose |
|---|---|
| `id` / `batch_id` | System-generated UUID used by REST paths, Order gRPC allocations and waste events |
| `batch_code` | Persisted production-container label supplied by Manager or generated on omission |

The label cannot replace UUIDs in existing callers. No change is made to Order,
Payment, Catalog, Auth, Analytics, Fulfillment, Notification or frontend code.
The canonical REST contract is `../contracts/openapi/batch-inventory-service.v1.yaml`.

## Create behavior

Manager submits `POST /api/v1/inventory/batches` with the existing fields plus an
optional label, for example:

```json
{
  "batch_code": " gf-20261011-choc-01 ",
  "flavor_id": "9e914b8c-2476-4c28-8a7d-36c88b1f2b9d",
  "production_date": "2026-10-11",
  "expires_at": "2026-10-13T12:00:00Z",
  "initial_portions": 40,
  "unit_cost_minor": 125,
  "currency": "THB"
}
```

The flavor UUID must identify an active flavor in Catalog. A successful HTTP 201
response contains a new UUID `id` and `batch_code: GF-20261011-CHOC-01`.
Without `batch_code`, the label is `BATCH-` followed by the uppercase batch UUID.
CreateBatch remains non-idempotent; reconcile an ambiguous create before retrying.

Surrounding whitespace is trimmed; ASCII letters are uppercased. Normalized codes
must be 1–64 characters matching `[A-Z0-9][A-Z0-9_-]{0,63}`. Blank, null, Unicode
letters, embedded spaces and other characters return HTTP 400. Duplicate codes
across flavors return HTTP 409 `BATCH_CODE_CONFLICT` after normalization, including
concurrent creates. Stock and audit movements commit only for a successful create.
Get/List include the stable persisted label. No code-update endpoint is added.

## Upgrade sequence

1. Back up the Inventory-owned database and stop all old Inventory writers.
2. Run the updated embedded migrator, `go run ./cmd/migrate`, with the Inventory
   DATABASE_URL. It applies 002 under the existing transaction/advisory lock.
3. Existing rows receive `BATCH-<UPPERCASE_ID>`; UUIDs, balances, dates and business
   references stay unchanged. Format, NOT NULL and unique constraints are added.
4. Start the updated Inventory binary. Startup requires migrations 001 and 002.
5. Verify migration receipt, preserved balances and create/read behavior before
   directing traffic to this version.

Repeated migration application is idempotent. This is a coordinated upgrade,
not a mixed-version rolling deployment: the old binary omits the new NOT NULL
column and therefore cannot create batches once migration 002 is installed.
Application rollback alone is insufficient after a database upgrade; preserve the
backup and plan a separate schema rollback while Inventory writers are stopped.
No existing team/demo database is upgraded automatically by this local branch.

## Caller compatibility

- Existing create bodies that omit the label continue to work with the new server.
- Batch UUIDs and gRPC/event payloads are unchanged; Order and Analytics continue
  referencing UUIDs.
- Batch REST response now includes `batch_code`. Old strict response validators
  with `additionalProperties: false` need the updated OpenAPI schema.
- Frontend's mock uses camelCase `batchCode`; its owner still needs a real API
  adapter for snake_case fields and must continue sending UUID `batch_id` for waste.
- No shared broker bindings, Gateway route, JWT policy or service token is changed.

Affected REST consumers should review the response addition and upgrade sequence
before merge or deployment. This document does not mark shared ADR decisions as
Accepted or claim complete UC1–UC3 integration.

## Validation

The feature tests cover normalized HTTP create/read/list, stored labels, duplicate
conflicts, invalid/null inputs without stock side effects, omitted-field fallback,
64-character boundaries, concurrent uniqueness across different flavor locks,
upgrade preservation/reapplication, and startup refusal without migration 002.
The existing full Inventory suite covers reserve/confirm/release/waste, FEFO,
expiry, idempotency, executable restart and RabbitMQ outbox behavior.

Run the full suite using `scripts/test-integration.sh` with isolated TEST_* stores;
run OpenAPI lint, Buf lint/build and event checks from the repository root.
