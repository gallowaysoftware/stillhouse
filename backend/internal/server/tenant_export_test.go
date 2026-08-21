package server

import (
	"strings"
	"testing"
)

// TestExportTablesAreAllRlsScoped is a guard on the one assumption
// /export/tenant.zip rests on: every table it dumps with a bare SELECT *
// is row-level-security scoped, so the tenant context alone confines the
// result.
//
// tenants and users are NOT. Both are created without RLS in migration
// 000001 because login must find a user before any tenant context exists.
// They were in the export list anyway, so an owner clicking "export my
// data" received every tenant's CRA spirits licence number and every
// user's Argon2id password hash — offline-crackable credentials for other
// distilleries' accounts. They are exported through
// exportOwnTenantIdentity now, scoped by an explicit WHERE.
//
// This test is deliberately a list-comparison rather than a live database
// check: the failure mode is somebody adding a table name to a slice, and
// that is exactly what it catches.
func TestExportTablesAreAllRlsScoped(t *testing.T) {
	// Tables created without ENABLE ROW LEVEL SECURITY. Keep in step with
	// the migrations; adding a table here should be a deliberate act.
	nonRLS := map[string]string{
		"tenants":               "no RLS — login resolves a tenant before any tenant context exists",
		"users":                 "no RLS, and holds password_hash",
		"sessions":              "session store, keyed by token not tenant",
		"invite_codes":          "redeemed before the invitee has a tenant",
		"password_reset_tokens": "looked up by token hash, pre-authentication",
		"schema_migrations":     "migration bookkeeping",
	}
	for _, table := range exportTables {
		if why, bad := nonRLS[table]; bad {
			t.Errorf("exportTables includes %q, which is dumped with a bare SELECT * "+
				"but is %s — every tenant's rows would land in the zip", table, why)
		}
	}
}

// TestIdentityExportOmitsCredentials: the scoped users query must not
// select password_hash. A data export is for the operator's records; it is
// not a place to put credential material, and a leaked export should not
// be a leaked credential store.
func TestIdentityExportOmitsCredentials(t *testing.T) {
	src := readSource(t, "tenant_export.go")
	fn := between(src, "func exportOwnTenantIdentity", "\nfunc ")
	if fn == "" {
		t.Fatal("exportOwnTenantIdentity not found")
	}
	for _, forbidden := range []string{"password_hash", "SELECT *"} {
		if strings.Contains(fn, forbidden) {
			t.Errorf("exportOwnTenantIdentity contains %q — it must select named, "+
				"non-credential columns only", forbidden)
		}
	}
	// And it must be scoped by parameter, since RLS won't do it here.
	if !strings.Contains(fn, "WHERE id = $1") || !strings.Contains(fn, "WHERE tenant_id = $1") {
		t.Error("exportOwnTenantIdentity must scope both queries with an explicit WHERE")
	}
}
