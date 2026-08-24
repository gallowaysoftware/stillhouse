-- name: SaveProvincialRegistration :one
INSERT INTO provincial_registrations (
    tenant_id, jurisdiction, board_name, registration_no,
    portal_url, contact, registered_on, ended_on, notes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (tenant_id, jurisdiction) DO UPDATE
SET board_name      = EXCLUDED.board_name,
    registration_no = EXCLUDED.registration_no,
    portal_url      = EXCLUDED.portal_url,
    contact         = EXCLUDED.contact,
    registered_on   = EXCLUDED.registered_on,
    ended_on        = EXCLUDED.ended_on,
    notes           = EXCLUDED.notes,
    updated_at      = NOW()
RETURNING *;

-- name: ListProvincialRegistrations :many
SELECT * FROM provincial_registrations ORDER BY jurisdiction;

-- name: GetProvincialRegistration :one
SELECT * FROM provincial_registrations WHERE id = $1;

-- name: DeleteProvincialRegistration :exec
DELETE FROM provincial_registrations WHERE id = $1;

-- name: SaveProvincialReportDefinition :one
INSERT INTO provincial_report_definitions (
    id, tenant_id, registration_id, name, cadence,
    due_days_after_period_end, follows_excise_clock,
    provenance, authority, confirmed_on, notes, archived
) VALUES (
    COALESCE(sqlc.narg(id)::uuid, gen_random_uuid()),
    $1, $2, $3, sqlc.arg(cadence)::reporting_cadence, $4, $5,
    sqlc.arg(provenance)::requirement_provenance, $6, $7, $8, $9
)
ON CONFLICT (id) DO UPDATE
SET registration_id           = EXCLUDED.registration_id,
    name                      = EXCLUDED.name,
    cadence                   = EXCLUDED.cadence,
    due_days_after_period_end = EXCLUDED.due_days_after_period_end,
    follows_excise_clock      = EXCLUDED.follows_excise_clock,
    provenance                = EXCLUDED.provenance,
    authority                 = EXCLUDED.authority,
    confirmed_on              = EXCLUDED.confirmed_on,
    notes                     = EXCLUDED.notes,
    archived                  = EXCLUDED.archived,
    updated_at                = NOW()
RETURNING *;

-- name: ListProvincialReportDefinitions :many
SELECT d.*, r.jurisdiction, r.board_name
FROM provincial_report_definitions d
JOIN provincial_registrations r ON r.id = d.registration_id
WHERE (sqlc.arg(include_archived)::boolean OR NOT d.archived)
ORDER BY r.jurisdiction, d.name;

-- name: GetProvincialReportDefinition :one
SELECT d.*, r.jurisdiction, r.board_name
FROM provincial_report_definitions d
JOIN provincial_registrations r ON r.id = d.registration_id
WHERE d.id = $1;

-- name: UpsertProvincialReportPeriod :one
INSERT INTO provincial_report_periods (
    tenant_id, definition_id, period_start, period_end, due_on
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (definition_id, period_start, period_end) DO UPDATE
SET due_on = COALESCE(provincial_report_periods.due_on, EXCLUDED.due_on)
RETURNING *;

-- name: MarkProvincialReportFiled :one
UPDATE provincial_report_periods
SET filed_at        = NOW(),
    filed_by        = $2,
    acknowledgement = $3,
    notes           = $4
WHERE id = $1 AND filed_at IS NULL
RETURNING *;

-- name: ListProvincialReportPeriods :many
SELECT p.*, d.name AS definition_name, r.jurisdiction, r.board_name
FROM provincial_report_periods p
JOIN provincial_report_definitions d ON d.id = p.definition_id
JOIN provincial_registrations r      ON r.id = d.registration_id
WHERE (NOT sqlc.arg(unfiled_only)::boolean OR p.filed_at IS NULL)
ORDER BY p.due_on NULLS LAST, r.jurisdiction;

-- name: ProvincialPeriodsDueBefore :many
-- Unfiled periods with a due date on or before a day, for the alert
-- evaluator. A definition with no recorded due-days produces periods
-- with a NULL due_on, which cannot be overdue and is not alerted on —
-- an alert derived from a deadline nobody recorded would be inventing
-- the deadline.
SELECT p.*, d.name AS definition_name, r.jurisdiction, r.board_name
FROM provincial_report_periods p
JOIN provincial_report_definitions d ON d.id = p.definition_id
JOIN provincial_registrations r      ON r.id = d.registration_id
WHERE p.filed_at IS NULL
  AND p.due_on IS NOT NULL
  AND p.due_on <= sqlc.arg(before)::date
ORDER BY p.due_on;

-- name: ProvincialSalesInPeriod :many
-- What actually went into a jurisdiction: removals to a customer in it,
-- by product.
--
-- The jurisdiction is the customer's, not the lot's. A lot's jurisdiction
-- is whose excise stamps are on the bottle, which is a federal fact about
-- the stamp; where the sale happened is a fact about the buyer, and they
-- diverge the first time a case stamped for one province is sold into
-- another. Reporting Ontario sales by stamp would report a shipment to
-- Alberta as Ontario's.
--
-- Removals with no customer are excluded, and counted separately by
-- ProvincialSalesUnattributed, because a removal with a free-text
-- destination cannot be attributed to a board without guessing.
SELECT c.jurisdiction,
       p.id   AS product_id,
       p.name AS product_name,
       p.gtin,
       p.bottle_size_ml,
       p.target_abv_pct,
       COALESCE(SUM(pr.bottles_removed), 0)::int              AS bottles,
       COALESCE(SUM(pr.total_litres), 0)::double precision    AS litres,
       COALESCE(SUM(pr.total_laa), 0)::double precision       AS laa,
       COALESCE(SUM(pr.duty_amount_cad), 0)::double precision AS duty_cad,
       COUNT(*)::int                                          AS removals
FROM packaging_removals pr
JOIN customers c ON c.id = pr.customer_id
JOIN packaged_inventory pi ON pi.id = pr.packaged_inventory_id
JOIN products p ON p.id = pi.product_id
WHERE pr.voided_at IS NULL
  AND pr.removal_date >= sqlc.arg(period_start)::date
  AND pr.removal_date <= sqlc.arg(period_end)::date
  AND (sqlc.narg(jurisdiction)::text IS NULL
       OR c.jurisdiction = sqlc.narg(jurisdiction)::text)
GROUP BY c.jurisdiction, p.id, p.name, p.gtin, p.bottle_size_ml, p.target_abv_pct
ORDER BY c.jurisdiction, p.name;

-- name: ProvincialSalesUnattributed :one
-- Removals in the period that name no customer, so no board can be
-- credited with them. Reported alongside every provincial figure: a
-- report that silently omits them understates the province it is for,
-- and the operator is the only one who knows where they went.
SELECT COALESCE(SUM(pr.bottles_removed), 0)::int           AS bottles,
       COALESCE(SUM(pr.total_laa), 0)::double precision    AS laa,
       COUNT(*)::int                                       AS removals
FROM packaging_removals pr
WHERE pr.voided_at IS NULL
  AND pr.customer_id IS NULL
  AND pr.removal_date >= sqlc.arg(period_start)::date
  AND pr.removal_date <= sqlc.arg(period_end)::date;

-- name: ContainersRemovedByJurisdiction :many
-- Containers that went into each market during the period, with the size
-- band a deposit programme charges by.
--
-- Counted from removals rather than from bottling: a deposit is owed when
-- a container enters the market, not when it is filled. Voided removals
-- are excluded — the stock did not leave, so no deposit arose.
--
-- Only duty-paid removals to customers. An export leaves the country and
-- a transfer in bond goes to another licensee's premises; neither puts a
-- container in front of a consumer, which is what a deposit programme
-- charges for.
SELECT COALESCE(NULLIF(pi.jurisdiction, ''), 'unstated')::text AS jurisdiction,
       p.bottle_size_ml,
       SUM(r.bottles_removed)::int AS containers,
       -- Returns come back off, because a container that came back is one
       -- the programme is not owed for twice. Stage 198.
       COALESCE((
           SELECT SUM(pr.bottles)
           FROM packaged_returns pr
           WHERE pr.packaged_inventory_id = r.packaged_inventory_id
             AND pr.voided_at IS NULL
             AND pr.returned_on >= @period_start::date
             AND pr.returned_on <= @period_end::date
       ), 0)::int AS returned
FROM packaging_removals r
JOIN packaged_inventory pi ON pi.id = r.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
WHERE r.voided_at IS NULL
  AND r.destination_kind = 'duty_paid_customer'
  AND r.removal_date >= @period_start::date
  AND r.removal_date <= @period_end::date
GROUP BY pi.jurisdiction, p.bottle_size_ml, r.packaged_inventory_id
ORDER BY jurisdiction, p.bottle_size_ml;
