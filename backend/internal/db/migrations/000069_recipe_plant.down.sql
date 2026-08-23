DROP INDEX IF EXISTS recipe_versions_plant_idx;
ALTER TABLE recipe_versions DROP COLUMN IF EXISTS mash_equipment_id;
