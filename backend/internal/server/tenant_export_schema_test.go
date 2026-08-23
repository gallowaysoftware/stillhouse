package server

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// notExported lists tenant-owned tables deliberately kept out of
// /export/tenant.zip. Each entry is an argument, not an oversight.
var notExported = map[string]string{
	"api_tokens": "bearer token hashes are credential material — the same " +
		"argument that keeps password_hash out of users.csv",
	"users": "exported through exportOwnTenantIdentity instead, scoped by " +
		"an explicit WHERE and without the credential column",
	"webhook_endpoints": "holds secret_sealed, which signs deliveries as " +
		"this distillery — same argument as api_tokens. The configuration " +
		"is small, visible in Settings, and re-enterable; a signing key in " +
		"a zip that gets emailed around is not worth saving the typing",
	"webhook_deliveries": "an operational log of what was sent, and every " +
		"payload in it is derived from a table that IS exported. Retention " +
		"under s.206 wants the records, not the notifications about them",
}

// TestExportCoversEveryTenantTable derives the expected export from the
// schema rather than from a list somebody remembered to update, in both
// directions: every table carrying a tenant_id is either exported or
// exempted with a reason, and every name in exportTables is a table that
// actually exists.
//
// The second half is the one that mattered. dumpTableToZip logs and
// continues when a table is missing, so three names that were never
// tables — barrels, mash_ingredients, material_receipts — sat in the list
// looking correct while the zip silently went out with no barrel
// maturation record, no mash bill, and no record of any material ever
// received. An export offered for s.206 retention was missing what came
// in the door, and nothing said so: the operator got a zip, the log got a
// warning nobody reads, and the omission would only surface when somebody
// went looking for a record that wasn't there.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestExportCoversEveryTenantTable(t *testing.T) {
	dsn := os.Getenv("STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN to run this test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	real := map[string]bool{}
	rows, err := pool.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatalf("enumerate tables: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		real[name] = true
	}
	rows.Close()
	if len(real) == 0 {
		t.Fatal("no tables found — is the test database migrated?")
	}

	exported := map[string]bool{}
	for _, table := range exportTables {
		if exported[table] {
			t.Errorf("exportTables lists %q twice", table)
		}
		exported[table] = true
		if !real[table] {
			t.Errorf("exportTables includes %q, which is not a table in this schema. "+
				"dumpTableToZip logs and continues, so this ships as a zip that is "+
				"quietly missing a record set nobody notices is gone.", table)
		}
	}

	tenantOwned, err := pool.Query(ctx,
		`SELECT DISTINCT table_name FROM information_schema.columns
		  WHERE table_schema = 'public' AND column_name = 'tenant_id'
		  ORDER BY table_name`)
	if err != nil {
		t.Fatalf("enumerate tenant-owned tables: %v", err)
	}
	defer tenantOwned.Close()

	var missing []string
	for tenantOwned.Next() {
		var name string
		if err := tenantOwned.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if exported[name] {
			if why, exempt := notExported[name]; exempt {
				t.Errorf("%q is both exported and listed as not exported (%s) — "+
					"decide which", name, why)
			}
			continue
		}
		if _, exempt := notExported[name]; exempt {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d tenant-owned table(s) are in neither exportTables nor "+
			"notExported, so an operator's own data export silently omits them:\n  %v",
			len(missing), missing)
	}
}
