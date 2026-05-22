-- Link mash ingredients to the specific material lot they came from. The
-- existing schema enforced one row per (mash_run, material), which made
-- the common "split between two lots" case unrepresentable. Drop that and
-- let multiple rows per material coexist.
ALTER TABLE mash_ingredient_usage
    DROP CONSTRAINT IF EXISTS mash_ingredient_usage_mash_run_id_material_id_key;

ALTER TABLE mash_ingredient_usage
    ADD COLUMN material_lot_id UUID REFERENCES material_lots(id) ON DELETE RESTRICT;

CREATE INDEX mash_ingredient_usage_lot_idx ON mash_ingredient_usage (material_lot_id)
    WHERE material_lot_id IS NOT NULL;
