package tenantdb

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// These two tests are the ones that actually prove tenant isolation, and
// until stage 153 neither of them ran: they wanted a second environment
// variable naming the stillhouse_app DSN, nobody set it, and the suite
// reported a confident green while skipping the only thing that checks
// the boundary holds. They now derive the app-role connection from the
// one admin DSN the rest of the suite already needs — see internal/testdb.

// TestRLSIsolation creates two tenants and a material in each via the
// admin pool, then uses the app pool through WithTenantTx to query
// materials under each tenant's context. Each tenant must see only its
// own row.
func TestRLSIsolation(t *testing.T) {
	ctx := context.Background()
	adminPool := testdb.AdminPool(t)
	appPool := testdb.AppPool(t)
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

// TestSetTenantContextEnablesAuditWriteDuringSignup pins the fix for a
// break that only ever appeared under a correctly configured server.
//
// Signup creates a tenant, so the transaction has to begin WITHOUT a
// tenant context — there is no id to scope by yet. But audit_events
// carries FORCE ROW LEVEL SECURITY, so writing the signup's audit row
// with no app.current_tenant_id set is refused, the transaction rolls
// back, and the tenant, the owner and the invite redemption all vanish
// behind a 500.
//
// It looked fine in dev because dev connects as the superuser, who
// bypasses RLS entirely — so the feature worked exactly where the tenant
// boundary wasn't being enforced, and broke exactly where it was. This
// test runs as stillhouse_app, which is the only configuration that can
// see it.
func TestSetTenantContextEnablesAuditWriteDuringSignup(t *testing.T) {
	ctx := context.Background()
	pool := testdb.AppPool(t)
	db := New(pool)

	var tenantID uuid.UUID
	err := db.WithoutTenantTx(ctx, func(ctx context.Context, q *sqlcgen.Queries) error {
		tenant, e := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
			Name:                    "Signup RLS " + uuid.NewString(),
			CraSpiritsLicenceNumber: "SIGNUP-" + uuid.NewString(),
			DefaultJurisdiction:     "CA-ON",
		})
		if e != nil {
			return e
		}
		tenantID = tenant.ID
		// The line under test. Without it the insert below is refused.
		if e := SetTenantContext(ctx, q, tenant.ID); e != nil {
			return e
		}
		_, e = q.InsertAuditEvent(ctx, sqlcgen.InsertAuditEventParams{
			TenantID:   tenant.ID,
			EntityType: "tenant",
			EntityID:   tenant.ID.String(),
			Action:     sqlcgen.AuditActionCreate,
			Payload:    []byte(`{"signup":true}`),
		})
		return e
	})
	if err != nil {
		t.Fatalf("signup transaction failed as stillhouse_app: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tenants WHERE id = $1", tenantID)
	})
}
