-- A movement known only by its date was stored at midnight UTC, which west
-- of Greenwich is the evening before. Periods are now cut at the licensee's
-- midnight, so those rows move into the working part of their own day:
-- 16:00 UTC is the same calendar day in every Canadian zone.
--
-- Only rows that are exactly midnight UTC and of a kind that is written from
-- a date are touched. A void reversal carried the moment of the void, and
-- takes the date of what it reverses so the two cancel in any walk.
UPDATE bulk_movements
SET occurred_at = occurred_at + INTERVAL '16 hours'
WHERE reference_type IN ('bottling_run', 'redistillation', 'marked_special_container',
                         'marked_special_container_unmark', 'possession_change')
  AND (occurred_at AT TIME ZONE 'UTC')::time = TIME '00:00';

UPDATE bulk_movements v
SET occurred_at = pg.gauge_date
FROM production_gauges pg
WHERE v.reference_type = 'distillation_run_void' AND pg.distillation_run_id = v.reference_id;

UPDATE bulk_movements v
SET occurred_at = orig.occurred_at
FROM bottling_runs br
JOIN bulk_movements orig ON orig.id = br.bulk_movement_id
WHERE v.reference_type = 'bottling_run_void' AND br.id = v.reference_id;

UPDATE barrel_events
SET event_date = event_date + INTERVAL '16 hours'
WHERE notes = 'adopted opening inventory' AND (event_date AT TIME ZONE 'UTC')::time = TIME '00:00';

UPDATE material_lots
SET received_at = received_at + INTERVAL '16 hours'
WHERE (received_at AT TIME ZONE 'UTC')::time = TIME '00:00';
