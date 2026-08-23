DROP INDEX IF EXISTS packaging_removals_shipment_idx;
ALTER TABLE packaging_removals DROP COLUMN IF EXISTS shipment_id;
DROP TABLE IF EXISTS shipment_lines;
DROP TABLE IF EXISTS shipments;
DROP TYPE IF EXISTS shipment_status;
DROP TABLE IF EXISTS sales_order_lines;
DROP TABLE IF EXISTS sales_orders;
DROP TYPE IF EXISTS sales_order_status;
