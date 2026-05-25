DROP TABLE IF EXISTS recipe_version_sensory;

ALTER TABLE recipe_ingredients
    DROP CONSTRAINT IF EXISTS recipe_ingredients_botanical_role_chk,
    DROP COLUMN IF EXISTS botanical_role;

ALTER TABLE recipe_versions
    DROP CONSTRAINT IF EXISTS recipe_versions_distillation_method_chk,
    DROP CONSTRAINT IF EXISTS recipe_versions_maceration_hours_chk,
    DROP CONSTRAINT IF EXISTS recipe_versions_ngs_input_l_chk,
    DROP CONSTRAINT IF EXISTS recipe_versions_ngs_input_abv_chk,
    DROP COLUMN IF EXISTS tasting_notes,
    DROP COLUMN IF EXISTS distillation_method,
    DROP COLUMN IF EXISTS maceration_hours,
    DROP COLUMN IF EXISTS gin_ngs_input_l,
    DROP COLUMN IF EXISTS gin_ngs_input_abv_pct;
