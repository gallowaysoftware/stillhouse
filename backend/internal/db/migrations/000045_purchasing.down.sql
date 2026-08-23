DROP INDEX IF EXISTS material_lots_grni_idx;
DROP INDEX IF EXISTS material_lots_po_line_idx;
ALTER TABLE material_lots
    DROP COLUMN IF EXISTS landed_unit_cost_cad,
    DROP COLUMN IF EXISTS invoiced_at,
    DROP COLUMN IF EXISTS invoice_reference,
    DROP COLUMN IF EXISTS handling_cad,
    DROP COLUMN IF EXISTS import_duty_cad,
    DROP COLUMN IF EXISTS freight_cad,
    DROP COLUMN IF EXISTS supplier_id,
    DROP COLUMN IF EXISTS purchase_order_line_id;
DROP TABLE IF EXISTS purchase_order_lines;
DROP TABLE IF EXISTS purchase_orders;
DROP TYPE IF EXISTS purchase_order_status;
DROP TABLE IF EXISTS suppliers;
