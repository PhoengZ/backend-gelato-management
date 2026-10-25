# Owned-service decision: optional REST replay and resilient audit

Status: implemented within Auth, Catalog and Batch Inventory; shared deployment and caller adoption remain coordinated separately.

## Context

The original progress PDF defines NFR-01 through NFR-09. Repository documentation adds an NFR-11 statement about mutation replay. Mandatory headers or changes to credential-token responses would change existing consumers. The canonical Waste body key and Inventory RPCs already have durable replay semantics, while Catalog previously covered only POST and Inventory batch creation had no key.

## Decision

- Add optional header support to Catalog PUT/PATCH/DELETE and Inventory batch creation/Waste. Keep successful no-header behavior, response shapes, routes and versioned RPCs intact. Do not edit shared contracts or teammate code.
- Catalog retains the POST compatibility namespace. New mutation keys bind verified Manager identity, method, target UUID and body hash. A Redis script checks replay before stale-state checks and preflights all key types/write permissions before committing flavor, history and response. An early read is only an optimization; the script repeats the authoritative decision atomically. No-op PUT/archive caches the response without adding mutation history.
- Inventory uses its existing `(operation,key)` table. Creation keys are scoped to the verified Manager. Waste can claim both its existing body key and an optional header alias in a deterministic order. Hash checks, stock/history/outbox changes and all original responses commit or roll back together. Existing committed records are never overwritten. No schema migration is added for these keys.
- Auth audit finishes independently of a disconnected request. Preserve a live request's original storage budget (capped at the 30s request maximum); cancelled, expired or unbounded callers get 500 ms of grace. This avoids losing slow-but-valid writes while bounding detached work. Retry only transient storage/network errors, at most three attempts. A stable event UUID and equality check handle ambiguous acknowledgements without adding duplicate rows or modifying append-only evidence. Authentication responses and credential lockout policy remain unchanged.
- Do not cache/replay Auth access tokens under an invented login contract. Issuance, expiry and credential checks need a separately agreed token lifecycle contract if that behavior is ever required.

## Consequences and limits

Existing callers can continue without a header. Callers opting in must use a fresh UUID for a new operation and retain its original input for retry. Catalog uses body-byte hashes and 24-hour retention; Inventory hashes validated typed input and keeps records according to its existing persistent retention. A cached result describes the original operation, so use GET for current stock or metadata.

Old unknown audit actors cannot be reconstructed. Auth's independent bounded retry cannot guarantee evidence through a continuous outage of its only database. Such a guarantee needs an independent durable store or an explicitly approved fail-closed authentication policy; neither is silently introduced here.

TLS remains opt-in. Gateway can use HTTPS upstream URLs plus process CA trust; current Order clients explicitly use plaintext gRPC and require their owner to add TLS support before shared Catalog/Inventory inbound TLS is enabled. Inventory migration 005 must precede the new binary. The [verification/handoff guide](owned-nfr-handoff.md) records the real caller boundaries and test procedure.
