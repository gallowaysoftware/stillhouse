-- name: ListLocations :many
SELECT l.*, COALESCE(el.licence_number, '') AS licence_number,
       (SELECT COUNT(*) FROM bulk_containers bc
         WHERE bc.location_id = l.id AND bc.archived = FALSE)::INTEGER AS container_count
FROM locations l
LEFT JOIN excise_licences el ON el.id = l.excise_licence_id
WHERE (sqlc.arg(include_archived)::BOOLEAN OR l.archived_at IS NULL)
ORDER BY l.is_default DESC, l.name;

-- name: GetLocation :one
SELECT * FROM locations WHERE id = $1;

-- name: GetDefaultLocation :one
SELECT * FROM locations WHERE is_default LIMIT 1;

-- name: CreateLocation :one
INSERT INTO locations (
    tenant_id, name, address, excise_licence_id, retail_store, notes
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateLocation :one
UPDATE locations SET
    name = $2, address = $3, excise_licence_id = $4,
    retail_store = $5, notes = $6, archived_at = $7
WHERE id = $1
RETURNING *;

-- name: ClearDefaultLocation :exec
-- Run before setting a new default. The partial unique index would
-- otherwise refuse the second one, and doing it in the same transaction
-- means there is never a moment with no default at all.
UPDATE locations SET is_default = FALSE WHERE is_default;

-- name: SetDefaultLocation :one
UPDATE locations SET is_default = TRUE WHERE id = $1 RETURNING *;

-- name: SetBulkContainerLocation :one
UPDATE bulk_containers SET location_id = $2 WHERE id = $1 RETURNING *;

-- name: RetailSupplyByLocation :many
-- The 30% single-retail-store supply rule (EDM8-1-1 ¶20): a licensee may
-- not supply more than 30% of a retail store's stock from its own
-- production. What Stillhouse can compute is its own side of that —
-- how much it sent to each of its retail locations against how much it
-- removed in total for the period.
--
-- It deliberately does NOT report a percentage against the store's whole
-- stock, because Stillhouse does not know what else the store bought.
-- Reporting a ratio it cannot see the denominator of would be inventing
-- the number the rule turns on.
SELECT
    l.id   AS location_id,
    l.name AS location_name,
    l.retail_store,
    COALESCE(SUM(r.total_laa), 0)::DOUBLE PRECISION      AS laa_removed,
    COALESCE(SUM(r.bottles_removed), 0)::INTEGER         AS bottles_removed
FROM locations l
LEFT JOIN packaging_removals r
       ON r.location_id = l.id
      AND r.voided_at IS NULL
      AND r.removal_date >= sqlc.arg(period_start)::DATE
      AND r.removal_date <= sqlc.arg(period_end)::DATE
WHERE l.archived_at IS NULL
GROUP BY l.id, l.name, l.retail_store
ORDER BY l.name;

-- name: CreateDefaultLocation :one
-- The first location for a new tenant, named from the tenant itself.
-- Called wherever a tenant is created, after the tenant context is set —
-- see the note in migration 000047 about why this is not a trigger.
INSERT INTO locations (tenant_id, name, is_default, notes)
VALUES ($1, $2, TRUE,
        'Created with the tenant. Rename it to whatever you call the site, '
        || 'and add others if you have them.')
RETURNING *;
