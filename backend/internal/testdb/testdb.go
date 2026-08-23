// Package testdb builds the database handles the DB-backed tests use.
//
// The distinction it exists to enforce: fixtures are seeded through a
// superuser pool, because setting up two tenants' worth of rows needs to
// cross the tenant boundary on purpose — but the code under test runs
// through a pool that row-level security actually applies to.
//
// Before this split every DB-backed test connected as the superuser and
// drove the handlers through that same connection, which had two
// consequences. The tests were not isolated from each other: a query
// whose only tenant scoping is RLS saw every other test's rows, so a
// period one test left behind blocked writes for every other tenant in
// the database, and the suite failed differently depending on which
// packages happened to run in parallel. And the tests proved nothing
// about tenant isolation, because the policies never acted on them.
//
// Running the handlers as stillhouse_app fixes both at once: the suite
// stops interfering with itself, and every DB-backed test now exercises
// the tenant boundary as a side effect of doing whatever else it does.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminDSNEnv is the superuser DSN every DB-backed test needs. Absent, the
// test skips.
const AdminDSNEnv = "STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN"

// AppDSNEnv optionally names a DSN that already connects as the
// application role. When it isn't set — the normal case — the app pool is
// derived from the admin DSN with SET ROLE, so the suite needs exactly one
// environment variable and no second password to keep in step.
const AppDSNEnv = "STILLHOUSE_INTEGRATION_TEST_DSN"

// AppRole is the non-superuser role the server connects as in production,
// and therefore the role the handlers must be tested under.
const AppRole = "stillhouse_app"

// DSN returns the admin DSN, or skips the test.
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(AdminDSNEnv)
	if dsn == "" {
		t.Skip("set " + AdminDSNEnv + " to run this test")
	}
	return dsn
}

// AdminPool returns a superuser pool for seeding fixtures. RLS does not
// apply to it, which is the point: a fixture legitimately needs to create
// rows for a tenant before any tenant context exists.
//
// Nothing under test should be driven through this pool.
func AdminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), DSN(t))
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// AppPool returns a pool whose sessions run as the application role, so
// row-level security applies exactly as it does in production. This is
// what tenantdb.New should be given in a test.
//
// Derived from the admin DSN with SET ROLE rather than a second
// connection string: a superuser may assume any role, the role's password
// never enters the picture, and there is no way for the two DSNs to drift
// out of step with each other.
func AppPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(AppDSNEnv)
	setRole := false
	if dsn == "" {
		dsn = DSN(t)
		setRole = true
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("app pool config: %v", err)
	}
	if setRole {
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+AppRole)
			return err
		}
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	t.Cleanup(pool.Close)

	assertRLSApplies(t, pool)
	return pool
}

// assertRLSApplies is the load-bearing half of this package. A pool that
// silently still bypasses RLS would make every test using it pass while
// proving nothing — the exact failure this package was written to end —
// so the condition is checked rather than assumed.
func assertRLSApplies(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var role string
	var superuser, bypassRLS bool
	err := pool.QueryRow(context.Background(), `
		SELECT current_user,
		       current_setting('is_superuser') = 'on',
		       COALESCE((SELECT r.rolbypassrls FROM pg_roles r
		                 WHERE r.rolname = current_user), false)`,
	).Scan(&role, &superuser, &bypassRLS)
	if err != nil {
		t.Fatalf("app pool: check role: %v", err)
	}
	if superuser || bypassRLS {
		t.Fatalf("app pool connected as %q, which bypasses row-level security; "+
			"the tests would prove nothing about tenant isolation. Point %s at a "+
			"superuser DSN (the role is assumed with SET ROLE), or %s at the %s role",
			role, AdminDSNEnv, AppDSNEnv, AppRole)
	}
}
