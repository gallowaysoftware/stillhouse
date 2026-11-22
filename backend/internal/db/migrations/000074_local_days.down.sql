-- Where a void reversal was dated is not recorded anywhere, so only the
-- date-only rows go back.
UPDATE bulk_movements
SET occurred_at = occurred_at - INTERVAL '16 hours'
WHERE reference_type IN ('bottling_run', 'redistillation', 'marked_special_container',
                         'marked_special_container_unmark', 'possession_change')
  AND (occurred_at AT TIME ZONE 'UTC')::time = TIME '16:00';

UPDATE barrel_events
SET event_date = event_date - INTERVAL '16 hours'
WHERE notes = 'adopted opening inventory' AND (event_date AT TIME ZONE 'UTC')::time = TIME '16:00';

UPDATE material_lots
SET received_at = received_at - INTERVAL '16 hours'
WHERE (received_at AT TIME ZONE 'UTC')::time = TIME '16:00';
