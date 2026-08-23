// Package db_test holds schema-level assertions: things that must be true
// of the database Stillhouse migrates into, independent of any handler.
package db_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// notTenantScoped lists the tables that carry a tenant_id column and are
// deliberately NOT under row-level security. Each entry is a decision
// with a reason, not an oversight — and adding one should require the
// same argument.
//
// The carve-out is stated in migration 000001: a login lookup by email
// has to work before a tenant context exists, so the auth tables cannot
// be behind a policy keyed off app.current_tenant_id.
var notTenantScoped = map[string]string{
	"users": "login is by email, before any tenant context exists (000001)",
}

// TestEveryTenantScopedTableEnforcesRLS enumerates every table in the
// public schema carrying a tenant_id column and asserts three things
// about each: row-level security enabled, FORCEd (so it applies to the
// table owner too), and at least one policy attached. Enabled without a
// policy denies everything; enabled without FORCE silently exempts the
// owner; a policy without enable does nothing at all.
//
// This is the assertion H11 was missing. The tenant boundary was correct
// by convention and by review; a new migration that creates a
// tenant-scoped table and forgets one of the three lines now fails here
// instead of shipping.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestEveryTenantScopedTableEnforcesRLS(t *testing.T) {
	pool := openSchemaTestPool(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx, `
		SELECT c.relname,
		       c.relrowsecurity,
		       c.relforcerowsecurity,
		       (SELECT count(*) FROM pg_policy p WHERE p.polrelid = c.oid)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND c.relkind = 'r'
		  AND EXISTS (
		      SELECT 1 FROM information_schema.columns col
		      WHERE col.table_schema = 'public'
		        AND col.table_name = c.relname
		        AND col.column_name = 'tenant_id')
		ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("enumerate tenant-scoped tables: %v", err)
	}
	defer rows.Close()

	type tableRLS struct {
		name     string
		enabled  bool
		forced   bool
		policies int
	}
	var tables []tableRLS
	for rows.Next() {
		var tr tableRLS
		if err := rows.Scan(&tr.name, &tr.enabled, &tr.forced, &tr.policies); err != nil {
			t.Fatalf("scan: %v", err)
		}
		tables = append(tables, tr)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// A schema with no tenant_id columns at all means the DSN points at a
	// database that was never migrated — fail rather than vacuously pass.
	if len(tables) < 20 {
		t.Fatalf("only %d tenant-scoped tables found; is this database migrated?", len(tables))
	}

	seenExempt := map[string]bool{}
	for _, tr := range tables {
		if why, exempt := notTenantScoped[tr.name]; exempt {
			seenExempt[tr.name] = true
			if tr.enabled {
				t.Errorf("%s is on the RLS exemption list (%s) but has RLS enabled — "+
					"remove it from notTenantScoped", tr.name, why)
			}
			continue
		}
		var missing []string
		if !tr.enabled {
			missing = append(missing, "ENABLE ROW LEVEL SECURITY")
		}
		if !tr.forced {
			missing = append(missing, "FORCE ROW LEVEL SECURITY")
		}
		if tr.policies == 0 {
			missing = append(missing, "a policy")
		}
		if len(missing) > 0 {
			t.Errorf("table %q carries tenant_id but is missing %s.\n"+
				"Every tenant-scoped table enables *and* forces RLS and attaches a "+
				"policy keyed off app.current_tenant_id, in the migration that "+
				"creates it. If this table is genuinely an exception, add it to "+
				"notTenantScoped with the reason.",
				tr.name, strings.Join(missing, " + "))
		}
	}

	// Keep the exemption list from outliving its subject.
	var stale []string
	for name := range notTenantScoped {
		if !seenExempt[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("notTenantScoped lists %q, which no longer has a tenant_id column "+
			"(or no longer exists) — drop the entry", name)
	}
}

// TestAPITokenKeyholeIsNarrow asserts the shape of the one deliberate
// hole in the api_tokens policy: the bearer-auth path resolves a token
// hash before a tenant is known, through SECURITY DEFINER functions
// owned by a NOLOGIN BYPASSRLS role.
//
// What is being defended is that the hole stays exactly two functions
// wide. If a later migration hands stillhouse_auth a third function, or
// lets that role log in, this fails.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestAPITokenKeyholeIsNarrow(t *testing.T) {
	pool := openSchemaTestPool(t)
	ctx := context.Background()

	var canLogin, bypassRLS bool
	if err := pool.QueryRow(ctx,
		`SELECT rolcanlogin, rolbypassrls FROM pg_roles WHERE rolname = 'stillhouse_auth'`,
	).Scan(&canLogin, &bypassRLS); err != nil {
		t.Fatalf("stillhouse_auth role: %v", err)
	}
	if canLogin {
		t.Error("stillhouse_auth can log in; it holds BYPASSRLS and must stay NOLOGIN")
	}
	if !bypassRLS {
		t.Error("stillhouse_auth lacks BYPASSRLS, so the bearer-auth keyhole cannot work")
	}

	rows, err := pool.Query(ctx, `
		SELECT p.proname
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		JOIN pg_roles r     ON r.oid = p.proowner
		WHERE n.nspname = 'public' AND r.rolname = 'stillhouse_auth'
		ORDER BY p.proname`)
	if err != nil {
		t.Fatalf("keyhole functions: %v", err)
	}
	defer rows.Close()
	var owned []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		owned = append(owned, name)
	}
	want := []string{"auth_api_token", "auth_touch_api_token"}
	if strings.Join(owned, ",") != strings.Join(want, ",") {
		t.Errorf("stillhouse_auth owns %v; want exactly %v. Every function it owns "+
			"runs with RLS bypassed — adding one widens the tenant boundary.",
			owned, want)
	}
}

func openSchemaTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN to run this test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestEveryTenantHasExactlyOneDefaultLocation is what a trigger would
// have enforced, and why there is no trigger.
//
// The obvious implementation of "every tenant has a default location" is
// an AFTER INSERT trigger on tenants. It does not work: signup creates a
// tenant in a transaction with no tenant context — there is no id to
// scope by until the INSERT returns — and locations FORCEs row-level
// security, so the trigger's write is refused. Making it SECURITY
// DEFINER would fix it by punching a hole in the tenant boundary to save
// typing in three call sites, which is the trade stage 152 exists to
// refuse.
//
// So the insert lives in the callers and this asserts the invariant
// instead. A new path that creates a tenant and forgets fails here
// rather than producing a tenant whose stock has nowhere to be.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestEveryTenantHasExactlyOneDefaultLocation(t *testing.T) {
	pool := openSchemaTestPool(t)
	ctx := context.Background()

	rows, err := pool.Query(ctx, `
		SELECT t.id, t.name,
		       COUNT(l.id) FILTER (WHERE l.is_default)::int AS defaults,
		       COUNT(l.id)::int                             AS total
		FROM tenants t
		LEFT JOIN locations l ON l.tenant_id = t.id
		GROUP BY t.id, t.name
		HAVING COUNT(l.id) FILTER (WHERE l.is_default) <> 1`)
	if err != nil {
		t.Fatalf("check default locations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var defaults, total int
		if err := rows.Scan(&id, &name, &defaults, &total); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Errorf("tenant %q (%s) has %d default location(s) out of %d. Every tenant "+
			"needs exactly one — whatever created this tenant should call "+
			"CreateDefaultLocation, the way signup and cmd/seed do.",
			name, id, defaults, total)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
}
