-- What a cask held, and since when, before each event. Voiding an event has
-- to put that back: a dump keyed as 20 L instead of 200 otherwise leaves
-- 180 L of evaporation on the return that never happened, and a regauge
-- could not be undone at all.
ALTER TABLE barrel_events
    ADD COLUMN prior_volume_l  DOUBLE PRECISION,
    ADD COLUMN prior_laa       DOUBLE PRECISION,
    ADD COLUMN prior_fill_date DATE;

COMMENT ON COLUMN barrel_events.prior_volume_l IS
    'Litres in the cask before this event. NULL on events recorded before migration 75, which cannot be put back exactly.';

-- A correction booked because of an event goes when the event does.
ALTER TABLE inventory_adjustments ADD COLUMN voided_at TIMESTAMPTZ;

-- The residual of a dump named the cask; it names the dump.
UPDATE bulk_movements bm
SET reference_id = be.id
FROM barrel_events be
WHERE bm.reference_type = 'barrel_dump_residual'
  AND be.kind = 'dump'
  AND be.container_id = bm.reference_id
  AND be.event_date = bm.occurred_at;

-- The movements that happened: not a voided gauge, bottling run or cask
-- event, nor anything they caused, nor the rows that reverse them. Every
-- line of a return is summed over this. The balances are not: they carry
-- both a movement and its reversal, which cancel.
CREATE VIEW live_bulk_movements WITH (security_invoker = true) AS
SELECT bm.id
FROM bulk_movements bm
WHERE bm.reference_type NOT IN ('distillation_run_void', 'bottling_run_void', 'barrel_event_void')
  AND NOT EXISTS (
      SELECT 1 FROM distillation_runs dr
      WHERE bm.reference_type = 'distillation_run' AND dr.id = bm.reference_id AND dr.voided_at IS NOT NULL)
  AND NOT EXISTS (
      SELECT 1 FROM bottling_runs br
      WHERE br.bulk_movement_id = bm.id AND br.voided_at IS NOT NULL)
  AND NOT EXISTS (
      SELECT 1 FROM barrel_events be
      WHERE be.voided_at IS NOT NULL
        AND (be.bulk_movement_id = bm.id
             OR (be.id = bm.reference_id
                 AND bm.reference_type IN ('barrel_dump_residual', 'barrel_regauge', 'inventory_adjustment'))));

GRANT SELECT ON live_bulk_movements TO stillhouse_app;
