-- Voidable barrel events. Fill and dump have clean inverses (the linked
-- bulk_movement can be flipped). Regauge replaces a snapshot — voiding
-- it cleanly requires the prior state, which we don't store, so the void
-- handler rejects regauge events and asks the operator to record a new
-- corrective regauge instead.
ALTER TABLE barrel_events
    ADD COLUMN voided_at     TIMESTAMPTZ,
    ADD COLUMN voided_by     UUID REFERENCES users(id),
    ADD COLUMN voided_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX barrel_events_active_idx
    ON barrel_events (container_id, event_date DESC)
    WHERE voided_at IS NULL;
