-- name: ListExciseLicences :many
-- Ceased licences are included and flagged rather than hidden: a return
-- filed under a licence that has since been surrendered still has to be
-- explicable years later.
SELECT * FROM excise_licences
ORDER BY kind, effective_from DESC;

-- name: GetExciseLicence :one
SELECT * FROM excise_licences WHERE id = $1;

-- name: CreateExciseLicence :one
INSERT INTO excise_licences (
    tenant_id, kind, licence_number, effective_from, expires_on,
    premises, security_amount_cad, security_expires_on, notes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateExciseLicence :one
UPDATE excise_licences SET
    kind = $2, licence_number = $3, effective_from = $4, expires_on = $5,
    premises = $6, security_amount_cad = $7, security_expires_on = $8,
    notes = $9, ceased_on = $10
WHERE id = $1
RETURNING *;

-- name: ListLicencesForRenewalAlert :many
-- Live licences with a recorded expiry. The rule that reads this decides
-- what counts as "soon"; the query's job is to exclude the ones that
-- cannot expire on us — ceased, or with no expiry recorded at all.
--
-- A licence with no expiry date is deliberately NOT alerted on. Every CRA
-- licence expires, so a missing date means nobody has entered it, and
-- inventing a two-year window from an effective date we may also be
-- guessing at would produce a reminder for the wrong day — which is
-- worse than none, because it would be believed.
SELECT * FROM excise_licences
WHERE ceased_on IS NULL
  AND (expires_on IS NOT NULL OR security_expires_on IS NOT NULL);

-- name: CountLicencesMissingExpiry :one
-- How many live licences have no expiry recorded, so the register screen
-- can say so rather than looking complete.
SELECT COUNT(*)::INTEGER FROM excise_licences
WHERE ceased_on IS NULL AND expires_on IS NULL;
