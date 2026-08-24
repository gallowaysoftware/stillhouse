
-- name: SetTenantContext :exec
-- Sets the RLS GUC inside an already-open transaction. Used by signup,
-- where the tenant does not exist when the transaction begins but must be
-- in scope before the audit row is written. Transaction-local (the `true`
-- argument), so it cannot leak across pooled connections.
SELECT set_config('app.current_tenant_id', $1, true);

-- name: GroupPackagedAndCasks :one
-- The two counts a group view shows beside bulk LAA. Scoped by RLS like
-- everything else, so it can only ever answer for the tenant whose
-- context is set.
SELECT COALESCE((SELECT SUM(bottles_on_hand) FROM packaged_inventory), 0)::int AS bottles,
       COALESCE((SELECT COUNT(*) FROM bulk_containers
                 WHERE kind = 'barrel' AND NOT archived AND current_laa > 0), 0)::int AS casks;

-- name: ListMaterialsForCopy :many
-- Definitions worth carrying to another of the licensee's own
-- distilleries. Archived rows are left behind: copying something the
-- source has retired starts the destination with a mistake.
SELECT name, kind, uom, extract_fraction, moisture_fraction, cereal, notes
FROM materials
WHERE NOT archived
ORDER BY name;

-- name: MaterialNamesInUse :many
SELECT name FROM materials;

-- name: InsertCopiedMaterial :exec
INSERT INTO materials (tenant_id, name, kind, uom, extract_fraction,
                       moisture_fraction, cereal, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: ListSuppliersForCopy :many
SELECT name, account_reference, contact_name, email, phone, address,
       payment_terms_days, country, notes
FROM suppliers
WHERE archived_at IS NULL
ORDER BY name;

-- name: SupplierNamesInUse :many
SELECT name FROM suppliers;

-- name: InsertCopiedSupplier :exec
INSERT INTO suppliers (tenant_id, name, account_reference, contact_name,
                       email, phone, address, payment_terms_days, country, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);
