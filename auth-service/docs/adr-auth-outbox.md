# Auth decision: confirmed outbox delivery without changing team topology

Status: implemented producer capability; shared event/topology adoption pending.

## Context

Signup atomically inserts an Auth user and `user.registered` outbox row, but there
was no runtime publisher. The shared event contracts, exchange definitions and
broker permissions currently define no Auth registration consumer or route.
Writing a pending row alone does not establish NFR-05 delivery.

## Decision

- Start an opt-in publisher in the Auth API process after DB migration/listener
  initialization. Keep its default off and existing HTTP/JSON/role contracts.
- Require an explicit AMQP URL and exchange. Use passive exchange lookup, mandatory
  publishing, persistent delivery mode and publisher confirms. Never create or
  modify shared topology/users. Preserve pending rows when there is no route.
- Reuse the existing table and pending partial index. Atomically lease one due
  row with `FOR UPDATE SKIP LOCKED`, increment its attempt and commit before
  touching RabbitMQ. This queue lock is separate from Inventory's stock locks.
  The lease expires after the publish timeout plus 10 seconds, so process death
  is recoverable without adding a migration or holding a connection during I/O.
- Fence completion with the claimed attempt. Positive confirm without a return
  sets `published_at`; other outcomes persist capped exponential backoff. A DB
  acknowledgement failure leaves a recoverable lease. Never delete failed rows.
- Reopen a bounded connection/channel on each attempt. Context cancellation closes
  even a silent socket. A requested AMQPS connection verifies CA/hostname and TLS
  1.2+, with an optional private CA. No transport downgrade is attempted.
- Emit a deterministic CloudEvents projection from existing rows: original UUID,
  original UTC time, type `com.gelatoflow.auth.user-registered.v1`, source
  `urn:gelatoflow:auth-service`, subject `users/<uuid>`, and data containing only
  `user_id`, `role`, `status`. Do not export old email/name or credential fields.
  The schema/example under this service are a draft for consumer review.
- Log fixed categories only. Keep signup/login and health independent of broker
  availability. Cancel the worker on shutdown before closing its DB pool.

## Consequences and acceptance

Delivery is at least once to the configured broker queue, not exactly once and
not proof of consumer processing. A broker ACK followed by crash/DB failure or
an expired lease can redeliver the same ID/body; consumers must deduplicate it.
Durable queues and publisher permissions remain deployment responsibilities.
Poison/unknown rows remain inspectable and retry with a bounded diagnostic;
consumer rejection/DLQ policies require their owners.

No new DB migration, public HTTP field, shared event file, broker binding or
teammate code changes are made. Activation on a shared broker needs an agreed
contract, durable receiving topology and least-privilege Auth credential. Existing
pending rows are eligible after opt-in activation; review their backlog first.

PostgreSQL integration tests cover concurrency, backoff, lost acknowledgements,
stale claims and cancellation. RabbitMQ integration tests cover mandatory returns,
durable messages, missing topology, failed credentials and verified AMQPS.
