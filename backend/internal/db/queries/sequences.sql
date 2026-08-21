-- Document-number allocation.
--
-- Every `next_*_no` query below is `SELECT MAX(n) + 1` against a column
-- carrying a UNIQUE constraint. That is a read-modify-write, and with two
-- operators working at once both transactions read the same maximum, both
-- claim the same number, and the second dies on the constraint — a 500
-- for the operator and an unrecorded run or shipment. Six concurrent
-- removals against six different lots lost four of them.
--
-- LockDocumentSequence serialises the allocation. The lock is advisory and
-- transaction-scoped: taken inside the same tx that does the INSERT, it is
-- released at commit or rollback with nothing to clean up, and it blocks
-- only other allocators of the same counter in the same tenant. Callers
-- must take it BEFORE reading the next number, in the same transaction.
--
-- Keyed on tenant + counter name so two tenants, and two different
-- counters within a tenant, never wait on each other.
--
-- Lock ordering: a transaction that takes both row locks and a sequence
-- lock must take the ROW locks first — see GetBulkContainerForUpdate and
-- GetPackagedInventoryForUpdate. Every caller does today (bottling and
-- removal lock their container or lot, then allocate; distillation, mash
-- and recipe versions take no row lock at all). Reversing that in one
-- caller and not another is how two of these deadlock.

-- name: LockDocumentSequence :exec
SELECT pg_advisory_xact_lock(
    hashtextextended(
        COALESCE(current_setting('app.current_tenant_id', true), '') || ':' || sqlc.arg(counter)::text,
        0));
