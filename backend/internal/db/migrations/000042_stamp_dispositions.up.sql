-- 000042_stamp_dispositions: where every stamp went.
--
-- Excise stamps are Crown-controlled. The licensee is accountable for
-- each one issued to them, and "stamps going missing" is a question CRA
-- asks. Stillhouse tracked three counters per order — received, applied,
-- voided — which answers "how many are left" and cannot answer the
-- question actually asked, which is "where did stamp ABC00457 go".
--
-- Two things were missing and this adds both.
--
-- First, a reason. quantity_voided lumped together a stamp that jammed in
-- the applicator, a roll that went missing off a bench, and a batch
-- returned to CRA. Those are the same arithmetic and completely
-- different events: one is spoilage, one is a loss that has to be
-- reported, one is a return. A single counter cannot tell an auditor
-- which happened, and the free-text reason on the RPC went into the
-- audit log rather than into the stamp record.
--
-- Second, serials. A disposition names the range it took out, where the
-- range is known, so the reconciliation can walk an order's issued range
-- end to end and say of every serial: applied to this run, disposed of
-- for this reason, or still on hand. A serial that is none of those is
-- unaccounted for, and that is the number CRA is asking about.

CREATE TYPE stamp_disposition_kind AS ENUM (
    'spoiled',    -- damaged in application; the ordinary case
    'damaged',    -- damaged before application, in storage or transit
    'lost',       -- cannot be located
    'stolen',     -- known to have been taken
    'destroyed',  -- deliberately destroyed, e.g. obsolete provincial stock
    'returned'    -- returned to CRA
);

CREATE TABLE excise_stamp_dispositions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    stamp_order_id UUID NOT NULL REFERENCES excise_stamp_orders(id) ON DELETE RESTRICT,
    kind           stamp_disposition_kind NOT NULL,
    quantity       INTEGER NOT NULL CHECK (quantity > 0),

    -- Where known. A roll that vanished may have a range; a stamp that
    -- jammed in the filler usually does not, and demanding one would get
    -- a made-up range typed in. Empty means "not identified", which the
    -- reconciliation reports rather than glosses.
    serial_start   TEXT NOT NULL DEFAULT '',
    serial_end     TEXT NOT NULL DEFAULT '',

    occurred_on    DATE NOT NULL DEFAULT CURRENT_DATE,
    -- Always required. A reason code alone does not answer an auditor
    -- asking what happened, the same argument as the reason-coded
    -- inventory adjustment in 000028.
    explanation    TEXT NOT NULL CHECK (length(trim(explanation)) > 0),
    -- Losses and thefts get reported to CRA; recording the reference is
    -- how the trail closes.
    reported_ref   TEXT NOT NULL DEFAULT '',

    -- Who. A disposition is an attributable act.
    recorded_by    UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX excise_stamp_dispositions_order_idx
    ON excise_stamp_dispositions (stamp_order_id, occurred_on DESC);
CREATE INDEX excise_stamp_dispositions_tenant_kind_idx
    ON excise_stamp_dispositions (tenant_id, kind, occurred_on DESC);

ALTER TABLE excise_stamp_dispositions ENABLE ROW LEVEL SECURITY;
ALTER TABLE excise_stamp_dispositions FORCE  ROW LEVEL SECURITY;
CREATE POLICY excise_stamp_dispositions_tenant ON excise_stamp_dispositions FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- Every void recorded before today becomes a 'spoiled' disposition, which
-- is what the existing RPC's own comment said it was for ("damaged in
-- application", "misprint"). The explanation says where it came from
-- rather than inventing one, so nobody reads a backfilled row as a
-- contemporaneous account.
INSERT INTO excise_stamp_dispositions
    (tenant_id, stamp_order_id, kind, quantity, occurred_on, explanation, recorded_by)
SELECT o.tenant_id, o.id, 'spoiled', o.quantity_voided,
       COALESCE(o.received_at, o.ordered_at)::DATE,
       'Backfilled from the order''s void counter, which recorded no reason. '
       || 'Treated as spoilage because that is what the void path was for.',
       (SELECT u.id FROM users u WHERE u.tenant_id = o.tenant_id ORDER BY u.created_at LIMIT 1)
FROM excise_stamp_orders o
WHERE o.quantity_voided > 0
  AND EXISTS (SELECT 1 FROM users u WHERE u.tenant_id = o.tenant_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON excise_stamp_dispositions TO stillhouse_app;
