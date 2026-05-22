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

// TestBottlingRunCostFullChain wires the chain of queries that
// BottlingRunCost has to walk and asserts the rolled-up cost is non-zero.
// This is the regression net for stages 49-51: if any of the chain queries
// (DistillationChainFromGauge, BottlingRunChainFeeds, ListMashIngredients,
// GetMaterialLot) drift, the cost falls to zero and this test fails.
//
// Skips without STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN, same pattern as the
// existing B266 test. Uses the admin pool directly so RLS doesn't get in
// the way of seeding fixtures.
func TestBottlingRunCostFullChain(t *testing.T) {
	adminDSN := os.Getenv("STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN to run this test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer pool.Close()
	q := sqlcgen.New(pool)

	tenant, err := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                         "Full-path Cost " + uuid.NewString(),
		CraSpiritsLicenceNumber:      "FPCOST-" + uuid.NewString(),
		ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
		DefaultJurisdiction:          "CA-ON",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	// Material + lot at known price: 50 kg @ $1.50/kg = $75 of raw input.
	mat, err := q.CreateMaterial(ctx, sqlcgen.CreateMaterialParams{
		TenantID:    tenant.ID,
		Name:        "Two-row malt",
		Kind:        sqlcgen.MaterialKindMalt,
		Uom:         "kg",
		Supplier:    "Test Supplier",
		ExtractPct:  pgtype.Float8{Float64: 0.8, Valid: true},
	})
	if err != nil {
		t.Fatalf("create material: %v", err)
	}
	lot, err := q.CreateMaterialLot(ctx, sqlcgen.CreateMaterialLotParams{
		TenantID:         tenant.ID,
		MaterialID:       mat.ID,
		SupplierLot:      "TEST-LOT-001",
		QuantityReceived: 50,
		ReceivedAt:       pgtype.Timestamptz{Valid: true, Time: time.Now()},
		UnitCostCad:      pgtype.Float8{Float64: 1.50, Valid: true},
	})
	if err != nil {
		t.Fatalf("create lot: %v", err)
	}

	// Recipe + version. We only need one to satisfy the FK from mash_runs.
	recipe, err := q.CreateRecipe(ctx, sqlcgen.CreateRecipeParams{
		TenantID: tenant.ID, Name: "Test recipe",
		SpiritKind: sqlcgen.SpiritKindCanadianWhisky,
	})
	if err != nil {
		t.Fatalf("create recipe: %v", err)
	}
	rv, err := q.CreateRecipeVersion(ctx, sqlcgen.CreateRecipeVersionParams{
		TenantID: tenant.ID, RecipeID: recipe.ID, VersionNo: 1,
		MashEfficiencyPct: 0.85, FermentEfficiencyPct: 0.92, DistillationRecoveryPct: 0.9,
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	// Mash with lot-linked ingredient.
	mash, err := q.CreateMashRun(ctx, sqlcgen.CreateMashRunParams{
		TenantID: tenant.ID, RecipeVersionID: rv.ID, MashNo: 9001,
		MashDate: pgtype.Date{Valid: true, Time: time.Now()},
		Status:   sqlcgen.MashStatusDistilled,
	})
	if err != nil {
		t.Fatalf("create mash: %v", err)
	}
	if _, err := q.AddMashIngredient(ctx, sqlcgen.AddMashIngredientParams{
		TenantID:      tenant.ID,
		MashRunID:     mash.ID,
		MaterialID:    mat.ID,
		MaterialLotID: uuid.NullUUID{UUID: lot.ID, Valid: true},
		QuantityUsed:  50,
		Uom:           "kg",
	}); err != nil {
		t.Fatalf("add mash ingredient: %v", err)
	}

	// Fermentation -> distillation -> production gauge.
	ferment, err := q.CreateFermentationRun(ctx, sqlcgen.CreateFermentationRunParams{
		TenantID: tenant.ID, MashRunID: mash.ID, FermenterLabel: "FV1",
		YeastMaterialID: uuid.NullUUID{Valid: false},
		PitchAt:         pgtype.Timestamptz{Valid: true, Time: time.Now()},
		Status:          sqlcgen.FermentationStatusDistilled,
	})
	if err != nil {
		t.Fatalf("create fermentation: %v", err)
	}
	dist, err := q.CreateDistillationRun(ctx, sqlcgen.CreateDistillationRunParams{
		TenantID: tenant.ID, RunNo: 9001, StillLabel: "PotStill1",
		RunDate: pgtype.Date{Valid: true, Time: time.Now()},
		Status:  sqlcgen.DistillationStatusGauged,
	})
	if err != nil {
		t.Fatalf("create distillation: %v", err)
	}
	if _, err := q.AddDistillationCharge(ctx, sqlcgen.AddDistillationChargeParams{
		TenantID: tenant.ID, DistillationRunID: dist.ID, FermentationRunID: ferment.ID,
		VolumeChargedL: 100, AbvPct: 8, ChargeOrder: 1,
	}); err != nil {
		t.Fatalf("add distillation charge: %v", err)
	}
	// Bulk container the production gauge will deposit into.
	container, err := q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
		TenantID: tenant.ID, Name: "Test Tank",
		Kind:      sqlcgen.BulkContainerKindTank,
		CapacityL: pgtype.Float8{Float64: 1000, Valid: true},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	gaugeTime := time.Now()
	mv, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
		TenantID:               tenant.ID,
		SourceContainerID:      uuid.NullUUID{Valid: false},
		DestinationContainerID: uuid.NullUUID{UUID: container.ID, Valid: true},
		VolumeL:                100, AbvPct: 70, Laa: 70,
		Reason:        sqlcgen.BulkMovementReasonProductionGauge,
		ReferenceType: "distillation_run",
		ReferenceID:   uuid.NullUUID{UUID: dist.ID, Valid: true},
		OccurredAt:    pgtype.Timestamptz{Valid: true, Time: gaugeTime},
	})
	if err != nil {
		t.Fatalf("insert production movement: %v", err)
	}
	if _, err := q.CreateProductionGauge(ctx, sqlcgen.CreateProductionGaugeParams{
		TenantID: tenant.ID, DistillationRunID: dist.ID,
		DestinationContainerID: container.ID,
		BulkMovementID:         mv.ID,
		GaugeDate:              pgtype.Timestamptz{Valid: true, Time: gaugeTime},
		VolumeL:                100, AbvPct: 70,
		GaugerUserID:           uuid.New(),
	}); err != nil {
		t.Fatalf("create production gauge: %v", err)
	}

	// Product + bottling run drawing from the container.
	product, err := q.CreateProduct(ctx, sqlcgen.CreateProductParams{
		TenantID: tenant.ID, Name: "FP Cost Test Product",
		SpiritKind: sqlcgen.SpiritKindVodka, BottleSizeMl: 750, TargetAbvPct: 40,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	bottlingMv, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
		TenantID:               tenant.ID,
		SourceContainerID:      uuid.NullUUID{UUID: container.ID, Valid: true},
		DestinationContainerID: uuid.NullUUID{Valid: false},
		VolumeL:                75, AbvPct: 40, Laa: 30,
		Reason:     sqlcgen.BulkMovementReasonTransferToPackaging,
		OccurredAt: pgtype.Timestamptz{Valid: true, Time: gaugeTime.Add(1 * time.Hour)},
	})
	if err != nil {
		t.Fatalf("insert bottling movement: %v", err)
	}
	run, err := q.CreateBottlingRun(ctx, sqlcgen.CreateBottlingRunParams{
		TenantID: tenant.ID, RunNo: 9002, ProductID: product.ID,
		SourceContainerID: container.ID, DestinationJurisdiction: "CA-ON",
		BottlingDate: pgtype.Date{Valid: true, Time: gaugeTime.Add(2 * time.Hour)},
		BottleCount:  100, BottlingLossL: 0, LotCode: "FP-COST-" + uuid.NewString(),
		TankGaugeVolumeL: 75, TankGaugeAbvPct: 40, TankGaugeLaa: 30,
		BulkMovementID:   bottlingMv.ID,
	})
	if err != nil {
		t.Fatalf("create bottling run: %v", err)
	}

	// Now exercise the cost chain: this is what the test is really for.
	// Build a stub MaterialService and call BottlingRunCost. Because the
	// test bypasses RLS via the admin pool, we can't go through the real
	// WithTenantTx flow; instead we re-run the cost computation manually
	// against the same queries.
	feedCutoff := run.BottlingDate.Time.Add(24 * time.Hour)
	feeds, err := q.BottlingRunChainFeeds(ctx, sqlcgen.BottlingRunChainFeedsParams{
		DestinationContainerID: uuid.NullUUID{UUID: run.SourceContainerID, Valid: true},
		OccurredAt:             pgtype.Timestamptz{Time: feedCutoff, Valid: true},
	})
	if err != nil {
		t.Fatalf("BottlingRunChainFeeds: %v", err)
	}
	var totalCost float64
	for _, fd := range feeds {
		if fd.Reason != sqlcgen.BulkMovementReasonProductionGauge {
			continue
		}
		chain, ce := q.DistillationChainFromGauge(ctx, fd.ID)
		if ce != nil {
			t.Fatalf("DistillationChainFromGauge: %v", ce)
		}
		if !chain.MashRunID.Valid {
			continue
		}
		ings, ie := q.ListMashIngredients(ctx, chain.MashRunID.UUID)
		if ie != nil {
			t.Fatalf("ListMashIngredients: %v", ie)
		}
		for _, ing := range ings {
			if !ing.MaterialLotID.Valid {
				continue
			}
			ml, le := q.GetMaterialLot(ctx, ing.MaterialLotID.UUID)
			if le != nil {
				t.Fatalf("GetMaterialLot: %v", le)
			}
			if !ml.UnitCostCad.Valid {
				continue
			}
			totalCost += ing.QuantityUsed * ml.UnitCostCad.Float64
		}
	}
	// Expect $75 total (50 kg × $1.50/kg).
	if totalCost < 74.99 || totalCost > 75.01 {
		t.Errorf("totalCost: got %v, want ~75", totalCost)
	}
	perBottle := totalCost / float64(run.BottleCount)
	if perBottle < 0.74 || perBottle > 0.76 {
		t.Errorf("perBottle: got %v, want ~0.75", perBottle)
	}
}
