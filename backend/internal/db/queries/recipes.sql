-- name: CreateRecipe :one
INSERT INTO recipes (
    tenant_id, name, spirit_kind, notes
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: GetRecipe :one
SELECT * FROM recipes WHERE id = $1;

-- name: ListRecipes :many
SELECT * FROM recipes
WHERE (sqlc.arg('include_archived')::boolean OR NOT archived)
ORDER BY archived, name;

-- name: SetRecipeArchived :one
UPDATE recipes SET archived = $2 WHERE id = $1 RETURNING *;

-- name: SetRecipeCurrentVersion :exec
UPDATE recipes SET current_version_id = $2 WHERE id = $1;

-- name: NextRecipeVersionNo :one
SELECT COALESCE(MAX(version_no), 0)::int + 1 AS next
FROM recipe_versions
WHERE recipe_id = $1;

-- name: CreateRecipeVersion :one
INSERT INTO recipe_versions (
    tenant_id, recipe_id, version_no, notes,
    mash_efficiency_pct, ferment_efficiency_pct, distillation_recovery_pct,
    target_water_l,
    tasting_notes, distillation_method, maceration_hours,
    gin_ngs_input_l, gin_ngs_input_abv_pct
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
) RETURNING *;

-- name: GetRecipeVersion :one
SELECT * FROM recipe_versions WHERE id = $1;

-- name: ListRecipeVersions :many
SELECT * FROM recipe_versions
WHERE recipe_id = $1
ORDER BY version_no DESC;

-- name: CreateRecipeIngredient :one
INSERT INTO recipe_ingredients (
    tenant_id, recipe_version_id, material_id, quantity, uom, notes, sort_order,
    botanical_role
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: ListRecipeIngredients :many
SELECT ri.*,
       m.name AS material_name,
       m.kind AS material_kind,
       m.uom  AS material_uom,
       m.extract_pct AS material_extract_pct
FROM recipe_ingredients ri
JOIN materials m ON m.id = ri.material_id
WHERE ri.recipe_version_id = $1
ORDER BY ri.sort_order, m.name;

-- name: UpsertRecipeVersionSensory :one
-- One row per recipe_version. Upsert because edits during recipe
-- development are the whole point — taste, score, save, retaste.
INSERT INTO recipe_version_sensory (
    recipe_version_id, tenant_id,
    juniper, citrus, herbal, spice, floral, earth,
    body, heat, balance, overall,
    tasting_panel, tasted_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (recipe_version_id) DO UPDATE SET
    juniper       = EXCLUDED.juniper,
    citrus        = EXCLUDED.citrus,
    herbal        = EXCLUDED.herbal,
    spice         = EXCLUDED.spice,
    floral        = EXCLUDED.floral,
    earth         = EXCLUDED.earth,
    body          = EXCLUDED.body,
    heat          = EXCLUDED.heat,
    balance       = EXCLUDED.balance,
    overall       = EXCLUDED.overall,
    tasting_panel = EXCLUDED.tasting_panel,
    tasted_at     = EXCLUDED.tasted_at
RETURNING *;

-- name: GetRecipeVersionSensory :one
SELECT * FROM recipe_version_sensory WHERE recipe_version_id = $1;
