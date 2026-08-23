DROP INDEX IF EXISTS products_gtin_idx;
ALTER TABLE products
    DROP COLUMN IF EXISTS gtin,
    DROP COLUMN IF EXISTS cspc_code,
    DROP COLUMN IF EXISTS bottles_per_case,
    DROP COLUMN IF EXISTS cases_per_layer,
    DROP COLUMN IF EXISTS layers_per_pallet,
    DROP COLUMN IF EXISTS case_gross_weight_kg,
    DROP COLUMN IF EXISTS common_name,
    DROP COLUMN IF EXISTS age_statement,
    DROP COLUMN IF EXISTS container_marking,
    DROP COLUMN IF EXISTS allergen_statement,
    DROP COLUMN IF EXISTS country_of_origin,
    DROP COLUMN IF EXISTS marketing_description;
