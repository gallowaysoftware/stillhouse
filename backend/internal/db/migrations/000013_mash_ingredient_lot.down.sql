DROP INDEX IF EXISTS mash_ingredient_usage_lot_idx;
ALTER TABLE mash_ingredient_usage DROP COLUMN IF EXISTS material_lot_id;
ALTER TABLE mash_ingredient_usage
    ADD CONSTRAINT mash_ingredient_usage_mash_run_id_material_id_key
    UNIQUE (mash_run_id, material_id);
