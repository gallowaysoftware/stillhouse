-- Who owns the spirits, and whether we hold them.
--
-- EDM10-1-7 page 3: report all bulk spirits in your possession regardless
-- of who owns them, and do not report spirits you own but do not hold.
-- Neither half was expressible. A bulk container was implicitly ours and
-- implicitly here, so a contract distiller's return was wrong in both
-- directions — and the reason it was wrong is worse than the arithmetic:
-- an operator storing a customer's casks has no way to record them, so
-- they don't, and the return under-reports spirits the licensee is
-- answerable for.
--
-- Two separate facts, deliberately not one:
--
--   owner_customer_id  NULL means the licensee owns it. A customer means
--                      they do. This decides whether the alcohol is an
--                      asset — the dashboard total, the inventory value,
--                      the cost of sales — and it has nothing to do with
--                      the B266.
--
--   possession         Whether the spirits are on our premises. This
--                      decides the B266 and nothing else.
--
-- The two are independent: a customer's cask maturing in our rackhouse is
-- theirs and ours to report; a parcel of our own whisky at a partner's
-- bonded warehouse is ours and theirs to report.

CREATE TYPE bulk_possession AS ENUM ('held', 'held_elsewhere');

ALTER TABLE bulk_containers
    ADD COLUMN owner_customer_id UUID REFERENCES customers(id) ON DELETE RESTRICT,
    ADD COLUMN possession bulk_possession NOT NULL DEFAULT 'held',
    -- Who has it, when it is not us. Free text plus a licence number
    -- rather than a foreign key: the holder is a licensee, not a
    -- customer, and the two are different relationships.
    ADD COLUMN held_by_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN held_by_licence_no TEXT NOT NULL DEFAULT '',
    ADD COLUMN possession_changed_at TIMESTAMPTZ;

-- Naming the holder is what makes "held elsewhere" a fact rather than a
-- shrug. A return that omits stock has to be able to say where it went.
ALTER TABLE bulk_containers
    ADD CONSTRAINT bulk_containers_holder_named
    CHECK (possession = 'held' OR held_by_name <> '');

-- The B266 closing balance sums containers in our possession and then
-- walks backwards through movements dated after the period end. That walk
-- stays correct across a possession change without any change to the
-- movement side, but only because of two rules the service layer enforces
-- (see rpc.SetBulkPossession):
--
--   1. A possession change writes a bulk_movement for the container's
--      whole balance — out on leaving, in on returning — under one of the
--      in-bond transfer reasons, which is what the movement actually is
--      and which the B266 already has a line for.
--
--   2. No other movement may be recorded against a container that is held
--      elsewhere. You cannot gauge spirits you do not hold; whatever
--      happens to them is the holder's record, reconciled by a regauge on
--      return.
--
-- Given those, the arithmetic works out: a container that left after the
-- period end is excluded from the running total but its exit movement is
-- subtracted, adding its balance back — which is exactly what was on hand
-- at the period end. A container that returned after the period end is
-- included in the running total and its return movement subtracts it.
--
-- Containers are always created and adopted in our possession, and reach
-- 'held_elsewhere' only by the transition above. There is deliberately no
-- way to conjure one straight into somebody else's warehouse: that would
-- be stock we never received, and the walk would have no movement to
-- reconcile it against.
CREATE INDEX bulk_containers_possession_idx ON bulk_containers (possession)
    WHERE NOT archived;
CREATE INDEX bulk_containers_owner_idx ON bulk_containers (owner_customer_id)
    WHERE owner_customer_id IS NOT NULL;
