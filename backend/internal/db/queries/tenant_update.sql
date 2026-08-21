-- name: SetDutyPointEffectiveFrom :one
-- Moves the cutover. Not exposed in the UI: the date is set once, when the
-- tenant is created or when this migration ran, and moving it re-attributes
-- duty across events that may already have been filed. Here so a support
-- path exists that is deliberate rather than improvised.
UPDATE tenants SET duty_point_effective_from = $2 WHERE id = $1 RETURNING *;

-- name: UpdateFilingCalendar :one
-- The reporting calendar: how often the licensee files, and how their
-- fiscal months are defined. Separate from UpdateTenant because these two
-- are CRA elections with paperwork behind them, not distillery details an
-- owner edits in passing.
UPDATE tenants
SET filing_frequency                   = $2,
    fiscal_month_basis                 = $3,
    fiscal_month_end_day               = $4,
    fiscal_month_notification_ref      = $5,
    filing_frequency_authorization_ref = $6
WHERE id = $1
RETURNING *;

-- name: UpdateTenant :one
UPDATE tenants
SET name                            = $2,
    cra_spirits_licence_number      = $3,
    excise_warehouse_licence_number = $4,
    default_jurisdiction            = $5
WHERE id = $1
RETURNING *;
