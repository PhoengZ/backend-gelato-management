# Inventory decision: protect history inside the owned database

Date: 2026-10-25
Status: Implemented within Inventory; deployment migration remains explicit.

## Context and decision

Application writers already append inventory_movements and waste_records, but
that convention alone does not enforce the NFR's append-only statement. Both
tables are owned exclusively by Inventory. No supported stock path updates,
deletes or truncates them.

Migration 006 adds a shared rejection function, one BEFORE UPDATE OR DELETE
row trigger and one BEFORE TRUNCATE statement trigger per table. ENABLE ALWAYS
also enforces them in replica session mode. They return a fixed SQLSTATE 23514
failure and never log arbitrary row data. PostgreSQL requires a statement trigger
for TRUNCATE; see [CREATE TRIGGER](https://www.postgresql.org/docs/16/sql-createtrigger.html)
and [ALTER TABLE trigger enablement](https://www.postgresql.org/docs/16/sql-altertable.html).

Inserts continue normally in the stock transaction. A history correction must
be represented by a new compensating business operation. Existing rows, UUIDs,
balances, timestamps and unknown historical actors are preserved. No actor is
invented for an old row or system worker. No cross-service foreign key is added.

The updated binary checks every embedded migration and the actual enabled
function/event/table bindings at startup and /ready. A marker without the four
ALWAYS guards is insufficient. Run /app/migrate before /app/inventory and
coordinate the DDL window with Inventory writers. No shared database is changed
by this code delivery. An administrator with DDL privileges can still replace the
function, disable/drop triggers or change the schema. Readiness catches missing
or disabled bindings; it is not cryptographic verification of function contents
or protection against a hostile database administrator.

## Alternatives and validation

Application-only discipline cannot reject manual DML. An external append-only
audit store would introduce another operational dependency. These local guards
close the ordinary DML gap while preserving supported API/worker writes and
existing caller contracts. They are additive, not an automatic destructive
rollback; reverting the application does not require deleting history.

Real PostgreSQL tests reject UPDATE/DELETE/TRUNCATE on both tables, compare the
complete original rows, accept later production/waste evidence, upgrade a schema
with legacy NULL actors, apply twice, enforce replica mode and reject readiness
after a dropped/disabled/replica-only trigger. Full suites cover reservations,
expiry and outbox operations against the protected schema.
