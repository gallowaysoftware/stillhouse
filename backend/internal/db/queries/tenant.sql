
-- name: SetTenantContext :exec
-- Sets the RLS GUC inside an already-open transaction. Used by signup,
-- where the tenant does not exist when the transaction begins but must be
-- in scope before the audit row is written. Transaction-local (the `true`
-- argument), so it cannot leak across pooled connections.
SELECT set_config('app.current_tenant_id', $1, true);
