DROP INDEX IF EXISTS bulk_containers_owner_idx;
DROP INDEX IF EXISTS bulk_containers_possession_idx;

ALTER TABLE bulk_containers
    DROP CONSTRAINT IF EXISTS bulk_containers_holder_named;

ALTER TABLE bulk_containers
    DROP COLUMN IF EXISTS possession_changed_at,
    DROP COLUMN IF EXISTS held_by_licence_no,
    DROP COLUMN IF EXISTS held_by_name,
    DROP COLUMN IF EXISTS possession,
    DROP COLUMN IF EXISTS owner_customer_id;

DROP TYPE IF EXISTS bulk_possession;
