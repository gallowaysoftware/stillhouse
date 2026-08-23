DROP INDEX IF EXISTS packaging_removals_location_idx;
DROP INDEX IF EXISTS packaged_inventory_location_idx;
DROP INDEX IF EXISTS bulk_containers_location_idx;
ALTER TABLE packaging_removals DROP COLUMN IF EXISTS location_id;
ALTER TABLE packaged_inventory DROP COLUMN IF EXISTS location_id;
ALTER TABLE bulk_containers    DROP COLUMN IF EXISTS location_id;
DROP TABLE IF EXISTS locations;
