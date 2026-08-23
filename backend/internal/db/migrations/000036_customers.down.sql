DROP INDEX IF EXISTS packaging_removals_customer_idx;
ALTER TABLE packaging_removals DROP COLUMN IF EXISTS customer_id;
ALTER TABLE customers DROP COLUMN IF EXISTS price_list_id;
DROP TABLE IF EXISTS price_list_entries;
DROP TABLE IF EXISTS price_lists;
DROP TABLE IF EXISTS customers;
DROP TYPE IF EXISTS customer_kind;
