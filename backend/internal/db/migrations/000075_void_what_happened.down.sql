DROP VIEW live_bulk_movements;

UPDATE bulk_movements bm
SET reference_id = be.container_id
FROM barrel_events be
WHERE bm.reference_type = 'barrel_dump_residual' AND be.id = bm.reference_id;

ALTER TABLE inventory_adjustments DROP COLUMN voided_at;
ALTER TABLE barrel_events
    DROP COLUMN prior_volume_l,
    DROP COLUMN prior_laa,
    DROP COLUMN prior_fill_date;
