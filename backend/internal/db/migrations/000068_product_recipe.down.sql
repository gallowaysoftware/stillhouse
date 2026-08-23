DROP INDEX IF EXISTS products_recipe_idx;
ALTER TABLE products DROP COLUMN IF EXISTS recipe_version_id;
