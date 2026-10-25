# Auth decision: optional signup receipts and credential audit policy

Date: 2026-10-25
Status: Implemented within Auth; best-effort audit accepted by the project owner.
The shared NFR-11 credential-endpoint clarification remains for owner review.

## Context and decision

The updated NFR document asks for UUID header replay on REST mutations. Existing
signup callers do not send that header. Login validates a current credential and
account state; retaining and replaying an old token could bypass that validation.

Signup therefore opts into replay when one non-nil UUID Idempotency-Key is
supplied. Auth migration 004 adds a private receipt table. A unique key claim,
user insert, user.registered outbox insert and original user response commit in
one PostgreSQL transaction. The unique constraint serializes racing claims.
No broker call or bcrypt work occurs while this transaction holds a DB lock.
A failed receipt write rolls back all facts. A lost commit acknowledgement can
return 503; retrying the same key recovers the committed receipt.

The key is scoped to signup and expires 24 hours after creation. A SHA-256 hash
binds normalized non-secret fields. The original salted bcrypt hash separately
verifies the password without retaining plaintext or a fast password digest.
It is private credential material with the same DB access controls as users;
the bounded cleanup removes expired proofs, and an outage can delay deletion.
Expiry on lookup does not depend on that worker. Admission limits still apply.

A matching request returns the original 201 user snapshot and replay header;
changed valid input returns 409. Replay creates no extra user, outbox or
registration audit and does not reset a current lockout. Missing/corrupt receipt
storage fails keyed requests with 503. Existing no-header signup behavior stays
available. A stored signup response grants no authenticated access.

Login continues to verify current credentials and account status and issue a
token afresh on every call. No generic token-response cache is added. Document
this as a credential-endpoint policy when the team revises the shared NFR; do not
claim the unchanged universal wording is fully satisfied by this local decision.

The owner accepts best-effort PostgreSQL audit for this iteration. Existing
bounded retries, cancellation-independent writes, stable event IDs and bounded
failure diagnostics remain. A terminal audit failure does not change the
committed signup/login result. No new durable store, mandatory broker dependency
or fail-closed authentication policy is imposed. This acceptance does not prove
complete audit retention during a continuous outage.

## Alternatives and verification

Mandatory headers would break old callers. In-memory receipts would disappear
on restart; separate DB commits would leave duplicate/ambiguous effects. Caching
login tokens would change credential security semantics. Independent durable
audit storage remains possible future work if a stronger guarantee is agreed.

Real PostgreSQL/race tests cover simultaneous equal and conflicting requests,
rollback when saving the response fails, ambiguous acknowledgement, expired
keys/cleanup, absent/corrupt storage, unchanged legacy behavior and current
credential checking despite an Idempotency-Key. The isolated Gateway fixture
checks signup replay in both plain and verified HTTPS upstream profiles.
