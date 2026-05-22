package tenantdb

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// integrationDSN returns the DSN to use for the integration test or skips
// the test if not set. Set STILLHOUSE_INTEGRATION_TEST_DSN to the
// stillhouse_app (non-super) role DSN to run.
func integrationDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("STILLHOUSE_INTEGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_DSN to run RLS integration tests")
	}
	return dsn
}

// adminDSN is the superuser DSN used to set up test fixtures (it bypasses
// RLS so we can create rows across tenants).
func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN to run RLS integration tests")
	}
	return dsn
}

// TestRLSIsolation creates two tenants and a material in each via the
// admin pool, then uses the app pool through WithTenantTx to query
// materials under each tenant's context. Each tenant must see only its
// own row.
func TestRLSIsolation(t *testing.T) {
	appDSN := integrationDSN(t)
	adminDSN := adminDSN(t)
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()

	appPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	defer appPool.Close()

	adminQ := sqlcgen.New(adminPool)
	app := New(appPool)

	// --- create two test tenants + a material each via the admin pool ---
	tenantA, err := adminQ.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                         "RLS Test Tenant A " + uuid.NewString(),
		CraSpiritsLicenceNumber:      "RLS-TEST-A-" + uuid.NewString(),
		ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
		DefaultJurisdiction:          "CA-ON",
	})
	if err != nil {
		t.Fatalf("create tenant A: %v", err)
	}
	tenantB, err := adminQ.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                         "RLS Test Tenant B " + uuid.NewString(),
		CraSpiritsLicenceNumber:      "RLS-TEST-B-" + uuid.NewString(),
		ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
		DefaultJurisdiction:          "CA-BC",
	})
	if err != nil {
		t.Fatalf("create tenant B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(ctx, "DELETE FROM tenants WHERE id IN ($1, $2)", tenantA.ID, tenantB.ID)
	})

	matA, err := adminQ.CreateMaterial(ctx, sqlcgen.CreateMaterialParams{
		TenantID: tenantA.ID,
		Name:     "Material-A-" + uuid.NewString(),
		Kind:     sqlcgen.MaterialKindGrain,
		Uom:      "kg",
	})
	if err != nil {
		t.Fatalf("create material A: %v", err)
	}
	matB, err := adminQ.CreateMaterial(ctx, sqlcgen.CreateMaterialParams{
		TenantID: tenantB.ID,
		Name:     "Material-B-" + uuid.NewString(),
		Kind:     sqlcgen.MaterialKindMalt,
		Uom:      "kg",
	})
	if err != nil {
		t.Fatalf("create material B: %v", err)
	}

	// --- queries via WithTenantTx must isolate ---
	mustList := func(name string, tenantID uuid.UUID) []sqlcgen.Material {
		var rows []sqlcgen.Material
		if err := app.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
			var e error
			rows, e = q.ListMaterials(ctx, sqlcgen.ListMaterialsParams{IncludeArchived: true})
			return e
		}); err != nil {
			t.Fatalf("%s: list: %v", name, err)
		}
		return rows
	}

	rowsA := mustList("tenantA", tenantA.ID)
	containsA, containsB := false, false
	for _, r := range rowsA {
		if r.ID == matA.ID {
			containsA = true
		}
		if r.ID == matB.ID {
			containsB = true
		}
	}
	if !containsA {
		t.Errorf("tenant A query did not return tenant A's material")
	}
	if containsB {
		t.Errorf("tenant A query LEAKED tenant B's material — RLS broken")
	}

	rowsB := mustList("tenantB", tenantB.ID)
	containsA, containsB = false, false
	for _, r := range rowsB {
		if r.ID == matA.ID {
			containsA = true
		}
		if r.ID == matB.ID {
			containsB = true
		}
	}
	if !containsB {
		t.Errorf("tenant B query did not return tenant B's material")
	}
	if containsA {
		t.Errorf("tenant B query LEAKED tenant A's material — RLS broken")
	}

	// --- bogus tenant sees nothing ---
	bogus := uuid.New()
	rowsBogus := mustList("bogus", bogus)
	for _, r := range rowsBogus {
		if r.ID == matA.ID || r.ID == matB.ID {
			t.Errorf("bogus tenant query LEAKED material %s — RLS broken", r.Name)
		}
	}

	// --- cross-tenant write is blocked: under tenant A context, try to
	//     insert a material claiming tenant B's id. RLS WITH CHECK must
	//     reject it.
	err = app.WithTenantTx(ctx, tenantA.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
		_, e := q.CreateMaterial(ctx, sqlcgen.CreateMaterialParams{
			TenantID: tenantB.ID, // intentionally wrong
			Name:     "ShouldFail-" + uuid.NewString(),
			Kind:     sqlcgen.MaterialKindOther,
			Uom:      "kg",
		})
		return e
	})
	if err == nil {
		t.Errorf("cross-tenant insert succeeded — RLS WITH CHECK is broken")
	}
}
