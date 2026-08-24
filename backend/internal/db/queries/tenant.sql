
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
