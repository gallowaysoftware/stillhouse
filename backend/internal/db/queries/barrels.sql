-- name: CreateBarrelAttributes :one
INSERT INTO barrel_attributes (
    container_id, tenant_id, cooperage_supplier, char_level, wood_species,
    prior_use, serial_burnin, rickhouse, row_position, level_position, column_position
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
) RETURNING *;

-- name: GetBarrelAttributes :one
SELECT * FROM barrel_attributes WHERE container_id = $1;

-- name: ListBarrels :many
SELECT
    bc.id, bc.name, bc.kind, bc.capacity_l, bc.location, bc.notes, bc.archived,
    bc.current_volume_l, bc.current_abv_pct, bc.current_laa,
    bc.created_at, bc.updated_at,
    -- Casks are where third-party ownership actually turns up: contract
    -- maturation and cask-ownership programmes are barrels, not tanks.
    -- Without these three columns the rackhouse list shows a customer's
    -- cask and the distillery's own identically.
    bc.owner_customer_id, bc.possession, bc.held_by_name, bc.held_by_licence_no,
    COALESCE(own.name, '') AS owner_name,
    ba.cooperage_supplier, ba.char_level, ba.wood_species, ba.prior_use,
    ba.serial_burnin, ba.rickhouse, ba.row_position, ba.level_position, ba.column_position,
    ba.fill_date, ba.days_aged_at_dump,
    fill.volume_l AS fill_volume_l,
    fill.abv_pct  AS fill_abv_pct,
    fill.laa      AS fill_laa
FROM bulk_containers bc
JOIN barrel_attributes ba ON ba.container_id = bc.id
LEFT JOIN customers own ON own.id = bc.owner_customer_id
-- The fill this cask is currently living off, so the angel's share can be
-- measured against it without a round trip per barrel.
LEFT JOIN LATERAL (
    SELECT be.volume_l, be.abv_pct, be.laa
    FROM barrel_events be
    WHERE be.container_id = bc.id
      AND be.kind = 'fill'
      AND be.voided_at IS NULL
    ORDER BY be.event_date DESC, be.created_at DESC
    LIMIT 1
) fill ON TRUE
WHERE bc.kind = 'barrel'
  AND (sqlc.arg('include_archived')::boolean OR NOT bc.archived)
ORDER BY ba.fill_date DESC NULLS LAST, bc.name;

-- name: SetBarrelFillDate :exec
UPDATE barrel_attributes SET fill_date = $2 WHERE container_id = $1;

-- name: SetBarrelDumpedClock :exec
UPDATE barrel_attributes
SET fill_date = NULL,
    days_aged_at_dump = $2
WHERE container_id = $1;

-- name: InsertBarrelEvent :one
-- volume_l / abv_pct / laa are the values AT 20 °C; observed_* preserve what
-- the operator read off the instrument. See migration 000023.
INSERT INTO barrel_events (
    tenant_id, container_id, kind, event_date,
    volume_l, abv_pct, laa, bulk_movement_id, location_after, notes, user_id,
    temperature_c, observed_volume_l, observed_density_kg_m3, volume_factor_c, strength_source,
    -- The instruments the determination was made with. NULL means none was
    -- named, which is what every row predating the register says.
    volume_instrument_id, strength_instrument_id, temperature_instrument_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
) RETURNING *;

-- name: ListBarrelEvents :many
SELECT * FROM barrel_events
WHERE container_id = $1
ORDER BY event_date DESC, created_at DESC;

-- name: GetBarrelEvent :one
SELECT * FROM barrel_events WHERE id = $1;

-- name: GetBulkMovementForBarrelEvent :one
SELECT * FROM bulk_movements WHERE id = $1;

-- name: VoidBarrelEvent :one
UPDATE barrel_events
SET voided_at = NOW(),
    voided_by = $2,
    voided_reason = $3
WHERE id = $1 AND voided_at IS NULL
RETURNING *;

-- name: AgeEvidenceForBottlingRun :many
-- The casks behind a bottling run, and how long each was in small wood.
--
-- EDM3-1-1 ¶43–46: age runs from original warehousing in small wood to
-- removal for export sale, and resets on redistillation. Stillhouse holds
-- the maturation clock already; this is the walk that reads it — the run's
-- source vessel, back through the movements that filled it, to the dumps
-- that came out of casks.
--
-- days_aged_at_dump is what the cask recorded when it was emptied, which
-- is the figure that matters: the age at removal from wood, not the age
-- today.
SELECT bc.id AS container_id,
       bc.name AS cask_name,
       bc.capacity_l,
       ba.serial_burnin,
       ba.days_aged_at_dump,
       ba.wood_species,
       ba.prior_use,
       ba.char_level,
       be.event_date AS dumped_on,
       be.laa AS dumped_laa
FROM bulk_movements fed
JOIN bulk_containers bc ON bc.id = fed.source_container_id
JOIN barrel_attributes ba ON ba.container_id = bc.id
LEFT JOIN LATERAL (
    SELECT e.event_date, e.laa
    FROM barrel_events e
    WHERE e.container_id = bc.id AND e.kind = 'dump' AND e.voided_at IS NULL
    ORDER BY e.event_date DESC LIMIT 1
) be ON TRUE
WHERE fed.destination_container_id = sqlc.arg(source_container_id)::uuid
  AND fed.occurred_at <= sqlc.arg(before)::timestamptz
  AND bc.kind = 'barrel'
ORDER BY ba.days_aged_at_dump NULLS LAST;

-- name: RedistillationsTouchingContainer :many
-- Anything put back through the still from this vessel. Age resets on
-- redistillation, so a certificate has to say whether one happened.
SELECT id, taken_on, source_container_id, laa_taken, reason
FROM redistillations
WHERE source_container_id = $1
ORDER BY taken_on DESC;
