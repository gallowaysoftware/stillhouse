-- Knowing before the glass runs out.
--
-- Materials have quantities on hand and no sense of whether that is a
-- lot. The stamp panel already answers the question for excise stamps —
-- bottles a day over the last thirty, divided into what is left — and
-- what it computes is not special to stamps: it is cover, and every
-- material has it.
--
-- Two figures, both the licensee's and both optional:
--
--   reorder_point     the level at which to order more, in the
--                     material's own unit.
--   lead_time_days    how long the supplier actually takes.
--
-- Neither has a default. A reorder point Stillhouse guessed would fire
-- at a level nobody chose, and an alert people did not choose is an alert
-- they learn to dismiss. A material with neither set is never alerted on
-- and the screen says its cover is unknown rather than fine.

ALTER TABLE materials
    ADD COLUMN reorder_point DOUBLE PRECISION
        CHECK (reorder_point IS NULL OR reorder_point >= 0),
    ADD COLUMN reorder_quantity DOUBLE PRECISION
        CHECK (reorder_quantity IS NULL OR reorder_quantity > 0),
    ADD COLUMN lead_time_days INTEGER
        CHECK (lead_time_days IS NULL OR lead_time_days >= 0),
    -- Who to order it from, when there is a usual one. The purchasing
    -- track already has suppliers; this is the default, not a contract.
    ADD COLUMN preferred_supplier_id UUID REFERENCES suppliers(id) ON DELETE SET NULL;

CREATE INDEX materials_reorder_idx ON materials (reorder_point)
    WHERE reorder_point IS NOT NULL AND NOT archived;

ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'material_low';
