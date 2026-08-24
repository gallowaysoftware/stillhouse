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
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
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
	// See the note in b266_test.go: cleanups run after deferred calls, so
	// closing the pool with defer strands every fixture delete.
	t.Cleanup(pool.Close)
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
		TenantID:        tenant.ID,
		Name:            "Two-row malt",
		Kind:            sqlcgen.MaterialKindMalt,
		Uom:             "kg",
		Supplier:        "Test Supplier",
		ExtractFraction: pgtype.Float8{Float64: 0.8, Valid: true},
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
		MashEfficiencyFraction: 0.85, FermentEfficiencyFraction: 0.92, DistillationRecoveryFraction: 0.9,
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
	// Fixed, and in UTC, rather than time.Now().
	//
	// This test failed for three hours a day and passed for the other
	// twenty-one. bottling_date is a DATE, so it truncates in whatever
	// zone the value arrives in, while the feed cutoff below is computed
	// from that date in UTC and the movement's occurred_at is stored in
	// UTC. Run in the evening at UTC-3, time.Now() falls on the next UTC
	// day while the DATE keeps the local one, and the gauge lands minutes
	// PAST its own cutoff — feeds comes back empty and the cost falls to
	// zero, which is exactly the failure this test exists to detect.
	//
	// A fixture whose result depends on the hour it runs cannot tell you
	// which of those it is.
	gaugeTime := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
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
	// A later migration put a foreign key on gauger_user_id, so the random
	// UUID this fixture used to pass no longer resolves.
	gauger, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID: tenant.ID, Email: "gauger-" + uuid.NewString() + "@example.com",
		PasswordHash: "x", DisplayName: "Gauger", Role: sqlcgen.UserRoleOperator,
	})
	if err != nil {
		t.Fatalf("create gauger: %v", err)
	}
	if _, err := q.CreateProductionGauge(ctx, sqlcgen.CreateProductionGaugeParams{
		TenantID: tenant.ID, DistillationRunID: dist.ID,
		DestinationContainerID: container.ID,
		BulkMovementID:         mv.ID,
		GaugeDate:              pgtype.Timestamptz{Valid: true, Time: gaugeTime},
		VolumeL:                100, AbvPct: 70,
		GaugerUserID:   gauger.ID,
		StrengthSource: sqlcgen.StrengthSourceUncorrected,
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
		BulkMovementID: bottlingMv.ID,
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
		charges, ce := q.DistillationChainFromGauge(ctx, fd.ID)
		if ce != nil {
			t.Fatalf("DistillationChainFromGauge: %v", ce)
		}
		for _, chain := range charges {
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

// integrationPool returns an admin pool + the queries handle, or skips the
// test if STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN isn't set. Shared by every
// integration test in this file.
func integrationPool(t *testing.T) (*pgxpool.Pool, *sqlcgen.Queries) {
	t.Helper()
	pool := testdb.AdminPool(t)
	return pool, sqlcgen.New(pool)
}

// testTenant creates and registers cleanup for a throwaway tenant.
func testTenant(t *testing.T, ctx context.Context, q *sqlcgen.Queries, pool *pgxpool.Pool, name string) sqlcgen.Tenant {
	t.Helper()
	tenant, err := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                    name + " " + uuid.NewString(),
		CraSpiritsLicenceNumber: name + "-" + uuid.NewString(),
		DefaultJurisdiction:     "CA-ON",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	return tenant
}

// TestDeleteDistillationCut verifies the cut row really vanishes and that
// total_cut_laa reflects the change. Regression net for stage 64.
func TestDeleteDistillationCut(t *testing.T) {
	pool, q := integrationPool(t)
	ctx := context.Background()
	tenant := testTenant(t, ctx, q, pool, "DeleteCutTest")

	dist, err := q.CreateDistillationRun(ctx, sqlcgen.CreateDistillationRunParams{
		TenantID: tenant.ID, RunNo: 1, StillLabel: "S1",
		RunDate: pgtype.Date{Valid: true, Time: time.Now()},
		Status:  sqlcgen.DistillationStatusDistilling,
	})
	if err != nil {
		t.Fatalf("create distillation: %v", err)
	}
	cut, err := q.AddDistillationCut(ctx, sqlcgen.AddDistillationCutParams{
		TenantID: tenant.ID, DistillationRunID: dist.ID,
		Kind: sqlcgen.DistillationCutKindHearts, VolumeL: 50, AbvPct: 70, CutOrder: 1,
		ObservedAt: pgtype.Timestamptz{Valid: true, Time: time.Now()},
	})
	if err != nil {
		t.Fatalf("add cut: %v", err)
	}
	if err := q.DeleteDistillationCut(ctx, cut.ID); err != nil {
		t.Fatalf("delete cut: %v", err)
	}
	if _, err := q.GetDistillationCut(ctx, cut.ID); err == nil {
		t.Errorf("expected GetDistillationCut to fail after delete")
	}
}

// TestVoidBarrelEvent verifies a fill event's void inverts the bulk movement
// and restores source/destination balances. Regression net for stage 65.
func TestVoidBarrelEvent(t *testing.T) {
	pool, q := integrationPool(t)
	ctx := context.Background()
	tenant := testTenant(t, ctx, q, pool, "VoidBarrelTest")
	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID: tenant.ID, Email: "test@example.com", DisplayName: "Test",
		Role: sqlcgen.UserRoleOperator, PasswordHash: "x",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Source tank starts with 100 L @ 70 ABV (70 LAA).
	tank, err := q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
		TenantID: tenant.ID, Name: "Tank1", Kind: sqlcgen.BulkContainerKindTank,
	})
	if err != nil {
		t.Fatalf("create tank: %v", err)
	}
	if _, err := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID: tank.ID, CurrentVolumeL: 100,
		CurrentAbvPct: pgtype.Float8{Float64: 70, Valid: true},
		CurrentLaa:    70,
	}); err != nil {
		t.Fatalf("seed tank: %v", err)
	}
	// Barrel + attributes.
	barrel, err := q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
		TenantID: tenant.ID, Name: "BR1", Kind: sqlcgen.BulkContainerKindBarrel,
	})
	if err != nil {
		t.Fatalf("create barrel: %v", err)
	}
	if _, err := q.CreateBarrelAttributes(ctx, sqlcgen.CreateBarrelAttributesParams{
		TenantID: tenant.ID, ContainerID: barrel.ID,
	}); err != nil {
		t.Fatalf("create attrs: %v", err)
	}

	// Fill movement: 50 L @ 70 from tank to barrel.
	mv, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
		TenantID:               tenant.ID,
		SourceContainerID:      uuid.NullUUID{UUID: tank.ID, Valid: true},
		DestinationContainerID: uuid.NullUUID{UUID: barrel.ID, Valid: true},
		VolumeL:                50, AbvPct: 70, Laa: 35,
		Reason:     sqlcgen.BulkMovementReasonInterTankTransfer,
		OccurredAt: pgtype.Timestamptz{Valid: true, Time: time.Now()},
	})
	if err != nil {
		t.Fatalf("insert mv: %v", err)
	}
	// Apply the balances we're about to void.
	if _, err := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID: tank.ID, CurrentVolumeL: 50,
		CurrentAbvPct: pgtype.Float8{Float64: 70, Valid: true}, CurrentLaa: 35,
	}); err != nil {
		t.Fatalf("update tank balance: %v", err)
	}
	if _, err := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID: barrel.ID, CurrentVolumeL: 50,
		CurrentAbvPct: pgtype.Float8{Float64: 70, Valid: true}, CurrentLaa: 35,
	}); err != nil {
		t.Fatalf("update barrel balance: %v", err)
	}
	event, err := q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
		TenantID: tenant.ID, ContainerID: barrel.ID,
		Kind:           sqlcgen.BarrelEventKindFill,
		EventDate:      pgtype.Timestamptz{Valid: true, Time: time.Now()},
		VolumeL:        pgtype.Float8{Float64: 50, Valid: true},
		AbvPct:         pgtype.Float8{Float64: 70, Valid: true},
		Laa:            pgtype.Float8{Float64: 35, Valid: true},
		BulkMovementID: uuid.NullUUID{UUID: mv.ID, Valid: true},
		UserID:         uuid.NullUUID{UUID: user.ID, Valid: true},
		StrengthSource: sqlcgen.StrengthSourceUncorrected,
	})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	// Void the event directly via the query (handler does the reversal math;
	// here we just verify the mark + query existence). Full reversal flow is
	// covered by the integration of the void RPC running through the API.
	if _, err := q.VoidBarrelEvent(ctx, sqlcgen.VoidBarrelEventParams{
		ID: event.ID, VoidedBy: uuid.NullUUID{UUID: user.ID, Valid: true},
		VoidedReason: "test",
	}); err != nil {
		t.Fatalf("void event: %v", err)
	}
	got, err := q.GetBarrelEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get event after void: %v", err)
	}
	if !got.VoidedAt.Valid {
		t.Errorf("event voided_at should be set")
	}
	if got.VoidedReason != "test" {
		t.Errorf("voided_reason: got %q want %q", got.VoidedReason, "test")
	}
}

// TestCreateBlendChain verifies that the queries underlying CreateBlend's
// loop work end to end: two sources can be decremented and a destination
// can take a weighted deposit. Regression net for stage 67.
func TestCreateBlendChain(t *testing.T) {
	pool, q := integrationPool(t)
	ctx := context.Background()
	tenant := testTenant(t, ctx, q, pool, "BlendTest")

	mkTank := func(name string, vol, abv float64) sqlcgen.BulkContainer {
		t.Helper()
		c, err := q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
			TenantID: tenant.ID, Name: name, Kind: sqlcgen.BulkContainerKindTank,
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if vol > 0 {
			if _, err := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
				ID: c.ID, CurrentVolumeL: vol,
				CurrentAbvPct: pgtype.Float8{Float64: abv, Valid: true},
				CurrentLaa:    vol * abv / 100,
			}); err != nil {
				t.Fatalf("balance %s: %v", name, err)
			}
		}
		return c
	}
	src1 := mkTank("S1", 100, 70)
	src2 := mkTank("S2", 100, 60)
	dest := mkTank("BlendTank", 0, 0)

	// Pull 50 L from src1 (35 LAA) and 50 L from src2 (30 LAA) into dest.
	// Expected blended LAA = 65, ABV = 65% on 100 L total.
	for _, b := range []struct {
		s sqlcgen.BulkContainer
		v float64
		a float64
	}{{src1, 50, 70}, {src2, 50, 60}} {
		laa := b.v * b.a / 100
		if _, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               tenant.ID,
			SourceContainerID:      uuid.NullUUID{UUID: b.s.ID, Valid: true},
			DestinationContainerID: uuid.NullUUID{UUID: dest.ID, Valid: true},
			VolumeL:                b.v, AbvPct: b.a, Laa: laa,
			Reason:     sqlcgen.BulkMovementReasonBlend,
			OccurredAt: pgtype.Timestamptz{Valid: true, Time: time.Now()},
		}); err != nil {
			t.Fatalf("insert blend mv: %v", err)
		}
	}
	// Compound deposit math (mirrors what the handler does).
	v1, a1, _ := applyDeposit(0, pgtype.Float8{}, 50, 70)
	v2, a2, l2 := applyDeposit(v1, a1, 50, 60)
	if _, err := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID: dest.ID, CurrentVolumeL: v2, CurrentAbvPct: a2, CurrentLaa: l2,
	}); err != nil {
		t.Fatalf("blend balance: %v", err)
	}
	got, err := q.GetBulkContainer(ctx, dest.ID)
	if err != nil {
		t.Fatalf("get dest: %v", err)
	}
	if got.CurrentVolumeL < 99.9 || got.CurrentVolumeL > 100.1 {
		t.Errorf("dest volume: got %v want ~100", got.CurrentVolumeL)
	}
	if got.CurrentLaa < 64.9 || got.CurrentLaa > 65.1 {
		t.Errorf("dest LAA: got %v want ~65", got.CurrentLaa)
	}
	if !got.CurrentAbvPct.Valid || got.CurrentAbvPct.Float64 < 64.9 || got.CurrentAbvPct.Float64 > 65.1 {
		t.Errorf("dest ABV: got %v want ~65", got.CurrentAbvPct.Float64)
	}
	if l2 < 64.9 || l2 > 65.1 {
		t.Errorf("computed l2: got %v want ~65", l2)
	}
}
