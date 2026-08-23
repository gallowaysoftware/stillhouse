package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// assertRLSEnforced checks, at boot, that the connection in DATABASE_URL
// is actually subject to row-level security.
//
// Tenant isolation in Stillhouse is entirely row-level security. Every
// tenant-scoped table enables and forces it, every policy keys off the
// app.current_tenant_id GUC that WithTenantTx sets, and a schema test
// keeps the next migration honest. All of that is worth exactly nothing
// if the server connects as a role that bypasses RLS — and the failure
// mode is the bad one: the app works perfectly, every screen renders,
// and the tenant boundary is simply not there.
//
// Two ways a role bypasses: it is a superuser, or it carries BYPASSRLS.
// Both are one copy-paste away, because the documented dev flow and
// cmd/seed both legitimately use the superuser DSN for migrations.
//
// In production this refuses to boot. Under STILLHOUSE_DEV=1 it degrades
// to a loud warning, because a developer pointing everything at the
// superuser DSN for an afternoon is a reasonable thing to do and should
// not be a puzzle.
func assertRLSEnforced(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, dev bool) error {
	var currentUser string
	var isSuperuser bool
	var bypassRLS bool
	err := pool.QueryRow(ctx, `
		SELECT current_user,
		       current_setting('is_superuser') = 'on',
		       COALESCE((SELECT r.rolbypassrls FROM pg_roles r
		                 WHERE r.rolname = current_user), false)`,
	).Scan(&currentUser, &isSuperuser, &bypassRLS)
	if err != nil {
		return fmt.Errorf("rls guard: %w", err)
	}

	if !isSuperuser && !bypassRLS {
		logger.Info("row-level security enforced", "db_role", currentUser)
		return nil
	}

	reason := "the role carries BYPASSRLS"
	if isSuperuser {
		reason = "the role is a superuser"
	}
	if dev {
		logger.Warn("TENANT ISOLATION IS OFF — row-level security is not enforced on this connection",
			"db_role", currentUser,
			"why", reason,
			"fix", "point DATABASE_URL at stillhouse_app; keep the superuser DSN in ADMIN_DATABASE_URL",
			"note", "allowed only because STILLHOUSE_DEV=1")
		return nil
	}
	return fmt.Errorf(
		"refusing to start: DATABASE_URL connects as %q and %s, so row-level "+
			"security does not apply and every tenant can read every other "+
			"tenant's data. Point DATABASE_URL at stillhouse_app and keep the "+
			"superuser DSN in ADMIN_DATABASE_URL. Set STILLHOUSE_DEV=1 to "+
			"downgrade this to a warning",
		currentUser, reason)
}
