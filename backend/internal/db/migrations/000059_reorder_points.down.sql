-- alert_kind keeps 'material_low'; Postgres cannot remove an enum value
-- without rewriting the type, and a kind nothing raises costs nothing.
DROP INDEX IF EXISTS materials_reorder_idx;
ALTER TABLE materials
    DROP COLUMN IF EXISTS preferred_supplier_id,
    DROP COLUMN IF EXISTS lead_time_days,
    DROP COLUMN IF EXISTS reorder_quantity,
    DROP COLUMN IF EXISTS reorder_point;
