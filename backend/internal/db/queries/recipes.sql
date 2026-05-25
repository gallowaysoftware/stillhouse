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
-- LEFT JOIN sensory so callers can iterate the version history and see
-- each version's tasting scores without N+1 queries. Sensory columns
-- are NULL when a version hasn't been tasted yet. The web Compare view
-- and the MCP list_recipe_versions tool both rely on these columns
-- being populated.
SELECT
    v.*,
    s.juniper       AS sensory_juniper,
    s.citrus        AS sensory_citrus,
    s.herbal        AS sensory_herbal,
    s.spice         AS sensory_spice,
    s.floral        AS sensory_floral,
    s.earth         AS sensory_earth,
    s.body          AS sensory_body,
    s.heat          AS sensory_heat,
    s.balance       AS sensory_balance,
    s.overall       AS sensory_overall,
    s.tasting_panel AS sensory_tasting_panel,
    s.tasted_at     AS sensory_tasted_at
FROM recipe_versions v
LEFT JOIN recipe_version_sensory s ON s.recipe_version_id = v.id
WHERE v.recipe_id = $1
ORDER BY v.version_no DESC;

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
-- One row per recipe_version. Partial-update: an axis that's NULL in
-- the request preserves the existing DB value via COALESCE; an axis
-- with a value overwrites. This lets an MCP / phone caller send
-- `{balance: 8}` to tweak one axis without re-typing the other 9.
-- Tasting panel + tasted_at follow the same rule (empty string is
-- treated as "no change" for panel).
INSERT INTO recipe_version_sensory (
    recipe_version_id, tenant_id,
    juniper, citrus, herbal, spice, floral, earth,
    body, heat, balance, overall,
    tasting_panel, tasted_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (recipe_version_id) DO UPDATE SET
    juniper       = COALESCE(EXCLUDED.juniper,       recipe_version_sensory.juniper),
    citrus        = COALESCE(EXCLUDED.citrus,        recipe_version_sensory.citrus),
    herbal        = COALESCE(EXCLUDED.herbal,        recipe_version_sensory.herbal),
    spice         = COALESCE(EXCLUDED.spice,         recipe_version_sensory.spice),
    floral        = COALESCE(EXCLUDED.floral,        recipe_version_sensory.floral),
    earth         = COALESCE(EXCLUDED.earth,         recipe_version_sensory.earth),
    body          = COALESCE(EXCLUDED.body,          recipe_version_sensory.body),
    heat          = COALESCE(EXCLUDED.heat,          recipe_version_sensory.heat),
    balance       = COALESCE(EXCLUDED.balance,       recipe_version_sensory.balance),
    overall       = COALESCE(EXCLUDED.overall,       recipe_version_sensory.overall),
    tasting_panel = CASE WHEN EXCLUDED.tasting_panel = '' THEN recipe_version_sensory.tasting_panel ELSE EXCLUDED.tasting_panel END,
    tasted_at     = EXCLUDED.tasted_at
RETURNING *;

-- name: GetRecipeVersionSensory :one
SELECT * FROM recipe_version_sensory WHERE recipe_version_id = $1;
