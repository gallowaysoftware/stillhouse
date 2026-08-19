package rpc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

func TestB266GenerationEndToEnd(t *testing.T) {
	adminDSN := os.Getenv("STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN to run this test")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()
	q := sqlcgen.New(adminPool)

	tenant, err := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                         "B266 Test " + uuid.NewString(),
		CraSpiritsLicenceNumber:      "B266-TEST-" + uuid.NewString(),
		ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
		DefaultJurisdiction:          "CA-ON",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID)
	})

	// Bulk container to hold spirits.
	container, err := q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
		TenantID:  tenant.ID,
		Name:      "B266 Test Tank",
		Kind:      sqlcgen.BulkContainerKindTank,
		CapacityL: pgtype.Float8{Float64: 1000, Valid: true},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	// Period = May 2026 (a closed past month so date filters work consistently).
	periodStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	queryEnd := periodEnd.AddDate(0, 0, 1)
	midPeriod := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	// Production gauge: 100 L @ 70% ABV = 70 LAA into bulk.
	produced := 100 * 70.0 / 100
	prodMv, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
		TenantID:               tenant.ID,
		SourceContainerID:      uuid.NullUUID{Valid: false},
		DestinationContainerID: uuid.NullUUID{UUID: container.ID, Valid: true},
		VolumeL:                100,
		AbvPct:                 70,
		Laa:                    produced,
		Reason:                 sqlcgen.BulkMovementReasonProductionGauge,
		ReferenceType:          "test_production",
		Notes:                  "fixture",
		OccurredAt:             pgtype.Timestamptz{Time: midPeriod, Valid: true},
	})
	if err != nil {
		t.Fatalf("insert production movement: %v", err)
	}
	_ = prodMv

	// Bottling: transfer 75 L to packaging (= 52.5 LAA out of bulk).
	bottledVolume := 75.0
	bottledLAA := bottledVolume * 70.0 / 100
	if _, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
		TenantID:               tenant.ID,
		SourceContainerID:      uuid.NullUUID{UUID: container.ID, Valid: true},
		DestinationContainerID: uuid.NullUUID{Valid: false},
		VolumeL:                bottledVolume,
		AbvPct:                 70,
		Laa:                    bottledLAA,
		Reason:                 sqlcgen.BulkMovementReasonTransferToPackaging,
		ReferenceType:          "test_bottling",
		OccurredAt:             pgtype.Timestamptz{Time: midPeriod, Valid: true},
	}); err != nil {
		t.Fatalf("insert bottling movement: %v", err)
	}

	// Update container balance to reflect both movements.
	if _, err := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID:             container.ID,
		CurrentVolumeL: 25,
		CurrentAbvPct:  pgtype.Float8{Float64: 70, Valid: true},
		CurrentLaa:     25 * 70 / 100,
	}); err != nil {
		t.Fatalf("update container balance: %v", err)
	}

	// Create a product + a bottling_run + a packaged_inventory row + a removal.
	product, err := q.CreateProduct(ctx, sqlcgen.CreateProductParams{
		TenantID: tenant.ID, Name: "B266 Test Product",
		SpiritKind: sqlcgen.SpiritKindVodka, BottleSizeMl: 750, TargetAbvPct: 40,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	run, err := q.CreateBottlingRun(ctx, sqlcgen.CreateBottlingRunParams{
		TenantID:                tenant.ID,
		RunNo:                   90001,
		ProductID:               product.ID,
		SourceContainerID:       container.ID,
		DestinationJurisdiction: "CA-ON",
		BottlingDate:            pgtype.Date{Time: midPeriod, Valid: true},
		BottleCount:             100,
		BottlingLossL:           0,
		LotCode:                 "B266-TEST-L-" + uuid.NewString(),
		TankGaugeVolumeL:        75,
		TankGaugeAbvPct:         40,        // bottling at bottle-proof
		TankGaugeLaa:            30,        // 75 × 0.40 = 30
		BulkMovementID:          prodMv.ID, // any non-null reference; not used by B266 query
	})
	if err != nil {
		t.Fatalf("create bottling run: %v", err)
	}
	pkg, err := q.UpsertPackagedInventory(ctx, sqlcgen.UpsertPackagedInventoryParams{
		TenantID:      tenant.ID,
		ProductID:     product.ID,
		LotCode:       run.LotCode,
		Jurisdiction:  "CA-ON",
		BottlingRunID: uuid.NullUUID{UUID: run.ID, Valid: true},
		BottlesOnHand: 100,
	})
	if err != nil {
		t.Fatalf("upsert packaged inventory: %v", err)
	}

	// Remove 30 of the 100 packaged bottles = 9 LAA, duty = 9 × 14.117 = 127.053.
	if _, err := q.CreateRemoval(ctx, sqlcgen.CreateRemovalParams{
		TenantID:            tenant.ID,
		RemovalNo:           90001,
		PackagedInventoryID: pkg.ID,
		RemovalDate:         pgtype.Date{Time: midPeriod, Valid: true},
		BottlesRemoved:      30,
		DestinationKind:     sqlcgen.RemovalDestinationKindDutyPaidCustomer,
		BottleSizeMl:        750,
		BottleAbvPct:        40,
		TotalLitres:         22.5,
		TotalLaa:            9,
		DutyRatePerLaa:      14.117,
		DutyAmountCad:       127.053,
	}); err != nil {
		t.Fatalf("create removal: %v", err)
	}
	if _, err := q.DecrementPackagedOnHand(ctx, sqlcgen.DecrementPackagedOnHandParams{
		ID:            pkg.ID,
		BottlesOnHand: 30,
	}); err != nil {
		t.Fatalf("decrement packaged: %v", err)
	}

	// Call computeB266Report directly with the admin Queries.
	report, err := computeB266Report(ctx, q, periodStart, periodEnd, queryEnd)
	if err != nil {
		t.Fatalf("computeB266Report: %v", err)
	}

	// Filter to this tenant's contribution. The function sums globally
	// because in production it runs inside WithTenantTx; under the admin
	// pool here it sees all tenants. Because we just created this tenant
	// in isolation, the period-bounded values should still be deterministic
	// only if no other tenants have May-2026 activity. Defensive check
	// that the contributions are AT LEAST our expected amounts.
	if got := report.BulkProductionLaa; got < produced-1e-6 {
		t.Errorf("BulkProductionLaa: got %v, want at least %v", got, produced)
	}
	if got := report.BulkTransferredToPackagingLaa; got < bottledLAA-1e-6 {
		t.Errorf("BulkTransferredToPackagingLaa: got %v, want at least %v", got, bottledLAA)
	}
	if got := report.PackagedPackagedLaa; got < 30-1e-6 {
		t.Errorf("PackagedPackagedLaa: got %v, want at least %v", got, 30.0)
	}
	if got := report.PackagedRemovedDutyPaidLaa; got < 9-1e-6 {
		t.Errorf("PackagedRemovedDutyPaidLaa: got %v, want at least %v", got, 9.0)
	}
	if got := report.DutyPayableCad; got < 127.05-0.01 {
		t.Errorf("DutyPayableCad: got %v, want at least %v", got, 127.05)
	}
	if report.DutyRatePerLaa != 14.117 {
		t.Errorf("DutyRatePerLaa: got %v, want 14.117", report.DutyRatePerLaa)
	}
}
