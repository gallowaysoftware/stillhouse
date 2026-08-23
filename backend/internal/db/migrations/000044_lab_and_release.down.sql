ALTER TABLE tenants DROP COLUMN IF EXISTS require_batch_release;
ALTER TABLE packaged_inventory
    DROP COLUMN IF EXISTS released_at,
    DROP COLUMN IF EXISTS released_by,
    DROP COLUMN IF EXISTS release_notes,
    DROP COLUMN IF EXISTS held_at,
    DROP COLUMN IF EXISTS held_by,
    DROP COLUMN IF EXISTS hold_reason;
DROP TABLE IF EXISTS lab_results;
DROP TYPE IF EXISTS lab_result_status;
