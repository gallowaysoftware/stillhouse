-- name: CreateFermentationRun :one
INSERT INTO fermentation_runs (
    tenant_id, mash_run_id, fermenter_label, yeast_material_id, yeast_lot_id, yeast_notes,
    pitch_at, target_final_gravity, initial_volume_l, status, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
) RETURNING *;

-- name: GetFermentationRun :one
SELECT * FROM fermentation_runs WHERE id = $1;

-- name: ListFermentationRunsByMash :many
SELECT * FROM fermentation_runs
WHERE mash_run_id = $1
ORDER BY pitch_at;

-- name: ListFermentationRuns :many
SELECT fr.*,
       mr.mash_no   AS mash_no,
       mr.mash_date AS mash_date,
       r.name       AS recipe_name
FROM fermentation_runs fr
JOIN mash_runs mr ON mr.id = fr.mash_run_id
JOIN recipe_versions rv ON rv.id = mr.recipe_version_id
JOIN recipes r          ON r.id = rv.recipe_id
WHERE (sqlc.narg('status')::fermentation_status IS NULL OR fr.status = sqlc.narg('status')::fermentation_status)
ORDER BY fr.pitch_at DESC;

-- name: UpdateFermentationStatus :one
UPDATE fermentation_runs SET status = $2 WHERE id = $1 RETURNING *;

-- name: AddFermentationLog :one
INSERT INTO fermentation_logs (
    tenant_id, fermentation_run_id, observed_at, specific_gravity, ph, temperature_c, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
) RETURNING *;

-- name: ListFermentationLogs :many
SELECT * FROM fermentation_logs
WHERE fermentation_run_id = $1
ORDER BY observed_at;
