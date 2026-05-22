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
