DROP INDEX IF EXISTS bottling_runs_owner_idx;
DROP INDEX IF EXISTS packaged_inventory_owner_idx;
ALTER TABLE marked_special_containers DROP COLUMN IF EXISTS owner_customer_id;
ALTER TABLE bottling_runs DROP COLUMN IF EXISTS owner_customer_id;
ALTER TABLE packaged_inventory DROP COLUMN IF EXISTS owner_customer_id;
