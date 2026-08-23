-- name: CreateBulkContainer :one
INSERT INTO bulk_containers (
    tenant_id, name, kind, capacity_l, location, notes
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: UpdateBulkContainer :one
UPDATE bulk_containers
SET name       = $2,
    kind       = $3,
    capacity_l = $4,
    location   = $5,
    notes      = $6
WHERE id = $1
RETURNING *;

-- name: SetBulkContainerArchived :one
UPDATE bulk_containers SET archived = $2 WHERE id = $1 RETURNING *;

-- name: GetBulkContainer :one
SELECT * FROM bulk_containers WHERE id = $1;

-- name: GetBulkContainerForUpdate :one
-- Read a container's balance with the intent to change it. FOR UPDATE is
-- what makes the read-modify-write safe: without it two transactions both
-- read the same volume, both compute an absolute new value, and the second
-- commit silently discards the first one's withdrawal. Eight concurrent
-- barrel fills from one tank moved 800 L while the tank fell by 100 —
-- alcohol conjured out of a lost update. Every path that writes a balance
-- must read it through here.
--
-- Lock ordering: a transaction touching more than one container (a blend,
-- a transfer) must acquire them in a deterministic order — see
-- lockContainers — or two of them can deadlock holding each other's rows.
SELECT * FROM bulk_containers WHERE id = $1 FOR UPDATE;

-- name: ListBulkContainers :many
-- Excludes barrels — they have their own dedicated list/get RPCs that
-- expose the maturation clock + barrel attributes. Including them here
-- would double-count vessels in the dashboard rollup and surface them
-- with no kind label (the proto enum has no BARREL case in the bulk
-- list response).
SELECT bc.*, COALESCE(own.name, '') AS owner_name
FROM bulk_containers bc
LEFT JOIN customers own ON own.id = bc.owner_customer_id
WHERE (sqlc.arg('include_archived')::boolean OR NOT bc.archived)
  AND bc.kind != 'barrel'
ORDER BY bc.archived, bc.name;

-- name: UpdateBulkContainerBalance :one
UPDATE bulk_containers
SET current_volume_l = $2,
    current_abv_pct  = $3,
    current_laa      = $4
WHERE id = $1
RETURNING *;

-- name: InsertExternalBulkMovement :one
-- A movement recorded directly by an operator rather than as a side effect
-- of another action: spirits arriving on or leaving the premises. Carries
-- the counterparty, the document, the determination and the author, none of
-- which a side-effect movement needs because its parent row has them.
INSERT INTO bulk_movements (
    tenant_id, source_container_id, destination_container_id,
    volume_l, abv_pct, laa, reason, reference_type,
    notes, occurred_at,
    counterparty_name, counterparty_licence_no, document_reference,
    temperature_c, observed_volume_l, observed_density_kg_m3,
    volume_factor_c, strength_source,
    volume_instrument_id, strength_instrument_id, temperature_instrument_id,
    recorded_by, packaged_inventory_id, bottles_unpackaged,
    loss_duty_treatment, loss_treatment_authority
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26
) RETURNING *;

-- name: InsertBulkMovement :one
INSERT INTO bulk_movements (
    tenant_id, source_container_id, destination_container_id,
    volume_l, abv_pct, laa, reason, reference_type, reference_id,
    notes, occurred_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
) RETURNING *;

-- name: ListBulkMovementsByContainer :many
SELECT bm.*,
       src.name AS source_name,
       dst.name AS destination_name
FROM bulk_movements bm
LEFT JOIN bulk_containers src ON src.id = bm.source_container_id
LEFT JOIN bulk_containers dst ON dst.id = bm.destination_container_id
WHERE bm.source_container_id = $1
   OR bm.destination_container_id = $1
ORDER BY bm.occurred_at DESC
LIMIT 200;

-- name: ListRecentBulkMovements :many
SELECT bm.*,
       src.name AS source_name,
       dst.name AS destination_name
FROM bulk_movements bm
LEFT JOIN bulk_containers src ON src.id = bm.source_container_id
LEFT JOIN bulk_containers dst ON dst.id = bm.destination_container_id
ORDER BY bm.occurred_at DESC
LIMIT 100;

-- name: BulkContainerLastActivity :many
-- Last movement timestamp per container (either as source or destination).
-- Returns one row per container that's ever moved alcohol; containers that
-- have only existed never accept a row here. Caller falls back to
-- container.created_at for those.
SELECT container_id,
       MAX(occurred_at)::timestamptz AS last_movement_at
FROM (
    SELECT source_container_id AS container_id, occurred_at
        FROM bulk_movements WHERE source_container_id IS NOT NULL
    UNION ALL
    SELECT destination_container_id AS container_id, occurred_at
        FROM bulk_movements WHERE destination_container_id IS NOT NULL
) m
GROUP BY container_id;

-- name: SumBulkLAA :one
-- Bulk LAA excludes barrels for the same reason ListBulkContainers
-- does — barrel LAA is reported separately so summing both would
-- double-count the alcohol on hand.
SELECT COALESCE(SUM(current_laa), 0)::double precision AS total_laa
FROM bulk_containers
WHERE NOT archived
  AND kind != 'barrel';

-- name: SumBulkLAAByOwnership :one
-- The two figures a "how much alcohol is here" question actually has,
-- once they stop being the same number.
--
-- SumBulkLAA above answers neither on its own: it sums every non-archived
-- container, so it counts a customer's tote as our asset and counts a
-- parcel we cannot touch as stock we could bottle tomorrow. Barrels are
-- excluded here for the same reason they are there — they are reported
-- separately and summing both double-counts.
SELECT
    -- Ours, wherever it is. What the balance sheet means.
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NULL), 0)::double precision AS owned_laa,
    -- Here, whoever owns it. What the B266 means.
    COALESCE(SUM(current_laa) FILTER (
        WHERE possession = 'held'), 0)::double precision AS held_laa,
    -- Ours and here: what we could actually blend, reduce or bottle.
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NULL AND possession = 'held'), 0)::double precision
        AS available_laa,
    -- Somebody else's, on our floor.
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NOT NULL AND possession = 'held'), 0)::double precision
        AS held_for_others_laa,
    -- Ours, somewhere else.
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NULL AND possession = 'held_elsewhere'), 0)::double precision
        AS held_elsewhere_laa,
    -- Somebody else's, somewhere else. On neither the return nor the
    -- books, and without this figure the two above read zero above a row
    -- that plainly was not — found by QA on a cask sold in place and then
    -- sent away.
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NOT NULL AND possession = 'held_elsewhere'), 0)::double precision
        AS third_party_elsewhere_laa,
    COUNT(*) FILTER (WHERE possession = 'held')::INTEGER AS held_count,
    COUNT(*) FILTER (WHERE owner_customer_id IS NOT NULL)::INTEGER AS third_party_count
FROM bulk_containers
WHERE NOT archived
  AND kind != 'barrel';

-- name: SumBarrelLAAByOwnership :one
-- The same split for casks, which is where third-party ownership actually
-- turns up: contract maturation and cask-ownership programmes are barrels.
SELECT
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NULL), 0)::double precision AS owned_laa,
    COALESCE(SUM(current_laa) FILTER (
        WHERE possession = 'held'), 0)::double precision AS held_laa,
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NOT NULL AND possession = 'held'), 0)::double precision
        AS held_for_others_laa,
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NULL AND possession = 'held_elsewhere'), 0)::double precision
        AS held_elsewhere_laa,
    COALESCE(SUM(current_laa) FILTER (
        WHERE owner_customer_id IS NOT NULL AND possession = 'held_elsewhere'), 0)::double precision
        AS third_party_elsewhere_laa,
    COUNT(*) FILTER (WHERE owner_customer_id IS NOT NULL)::INTEGER AS third_party_count
FROM bulk_containers
WHERE NOT archived
  AND kind = 'barrel';

-- name: SetBulkContainerOwner :one
UPDATE bulk_containers SET owner_customer_id = $2 WHERE id = $1 RETURNING *;

-- name: SetBulkContainerPossession :one
UPDATE bulk_containers
SET possession            = sqlc.arg(possession)::bulk_possession,
    held_by_name          = sqlc.arg(held_by_name)::TEXT,
    held_by_licence_no    = sqlc.arg(held_by_licence_no)::TEXT,
    possession_changed_at = NOW()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListThirdPartyBulkContainers :many
-- Everything that is not simply ours-and-here, which is the list an
-- operator needs before they sign a return or value their inventory.
SELECT bc.*, COALESCE(c.name, '') AS owner_name
FROM bulk_containers bc
LEFT JOIN customers c ON c.id = bc.owner_customer_id
WHERE NOT bc.archived
  AND (bc.owner_customer_id IS NOT NULL OR bc.possession <> 'held')
ORDER BY bc.possession, c.name NULLS FIRST, bc.name;

-- name: CountThirdPartyBulkContainers :one
-- Whether anything on the premises belongs to somebody else. Cheap enough
-- to ask on every journal build, and the answer decides whether the cost
-- of sales figure needs a caveat attached — see journal.addCOGS.
SELECT COUNT(*)::INTEGER AS n
FROM bulk_containers
WHERE NOT archived
  AND owner_customer_id IS NOT NULL;
