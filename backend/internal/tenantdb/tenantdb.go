// Package tenantdb provides the canonical way to run tenant-scoped database
// work in Stillhouse: open a pgx transaction, set the per-request
// app.current_tenant_id GUC so row-level security policies activate, then
// hand a tenant-scoped *sqlcgen.Queries to a callback.
//
// Every RPC that touches an RLS-protected table should go through
// WithTenantTx — never reach the pool directly with tenant data.
package tenantdb

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *DB { return &DB{Pool: pool} }

// WithTenantTx opens a transaction, sets app.current_tenant_id to tenantID
// for the lifetime of the transaction (set_config(..., true) == SET LOCAL),
// then runs fn with a *sqlcgen.Queries bound to that transaction. On nil
// error the transaction commits; otherwise it rolls back.
func (d *DB) WithTenantTx(
	ctx context.Context,
	tenantID uuid.UUID,
	fn func(ctx context.Context, q *sqlcgen.Queries) error,
) error {
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.current_tenant_id', $1, true)",
			tenantID.String(),
		); err != nil {
			return fmt.Errorf("set tenant context: %w", err)
		}
		return fn(ctx, sqlcgen.New(tx))
	})
}

// SetTenantContext sets app.current_tenant_id inside an already-open
// transaction opened by WithoutTenantTx.
//
// Signup is the case this exists for: the tenant does not exist when the
// transaction begins, so there is no id to scope by — but by the time the
// audit row is written there is, and audit_events has FORCE ROW LEVEL
// SECURITY. Without this the INSERT is refused, the transaction rolls
// back, and self-service signup fails with a 500 in any deployment where
// RLS is actually enforcing. It appeared to work only where the server
// connected as a superuser, which is precisely where RLS isn't protecting
// anything.
func SetTenantContext(ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID) error {
	if err := q.SetTenantContext(ctx, tenantID.String()); err != nil {
		return fmt.Errorf("set tenant context mid-transaction: %w", err)
	}
	return nil
}

// WithoutTenantTx opens a transaction WITHOUT setting a tenant context.
// Only safe for cross-tenant or pre-tenant operations: signup (creates the
// first row in tenants), invite redemption, password reset lookups by
// email. RLS-protected tables can't be touched from this transaction —
// the policies refuse without app.current_tenant_id — but the bootstrap
// tables (tenants, users, invite_codes, password_reset_tokens) are
// intentionally not RLS-protected and are reachable here.
func (d *DB) WithoutTenantTx(
	ctx context.Context,
	fn func(ctx context.Context, q *sqlcgen.Queries) error,
) error {
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		return fn(ctx, sqlcgen.New(tx))
	})
}
