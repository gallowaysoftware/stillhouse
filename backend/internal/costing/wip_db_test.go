package costing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// The apportionment is the whole of PLAN E7, and it is arithmetic over a
// four-table walk. A unit test over hand-built structs would be testing
// the struct-building; these seed the chain and check the money.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

type wipFixture struct {
	ctx           context.Context
	q             *sqlcgen.Queries
	tenant        uuid.UUID
	user          uuid.UUID
	tank          uuid.UUID
	mat           uuid.UUID
	lot           uuid.UUID
	recipeVersion uuid.UUID
}

func newWIPFixture(t *testing.T) *wipFixture {
	t.Helper()
	ctx := context.Background()
	pool := testdb.AdminPool(t)
	f := &wipFixture{ctx: ctx, q: sqlcgen.New(pool)}

	err := pool.QueryRow(ctx, `
		INSERT INTO tenants (name, cra_spirits_licence_number, default_jurisdiction)
		VALUES ($1, $1, 'CA-NS') RETURNING id`, "WIP "+uuid.NewString()).Scan(&f.tenant)
	if err != nil {
		t.Fatalf("tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id=$1", f.tenant) })

	must := func(what string, e error) {
		t.Helper()
		if e != nil {
			t.Fatalf("%s: %v", what, e)
		}
	}
	// Every tenant has exactly one, the way signup and cmd/seed make one —
	// and internal/db asserts it across the whole database, so a fixture
	// that skips it fails a test in another package.
	must("default location", exec(pool, ctx, `
		INSERT INTO locations (tenant_id, name, is_default) VALUES ($1,$2,TRUE)`,
		f.tenant, "WIP default"))
	must("user", pool.QueryRow(ctx, `
		INSERT INTO users (tenant_id, email, password_hash, display_name, role)
		VALUES ($1,$2,'x','WIP','owner') RETURNING id`,
		f.tenant, "wip-"+uuid.NewString()+"@example.com").Scan(&f.user))
	must("tank", pool.QueryRow(ctx, `
		INSERT INTO bulk_containers (tenant_id, name, kind) VALUES ($1,'WIP tank','spirit_receiver')
		RETURNING id`, f.tenant).Scan(&f.tank))
	must("material", pool.QueryRow(ctx, `
		INSERT INTO materials (tenant_id, name, kind, uom) VALUES ($1,'Rye','grain','kg')
		RETURNING id`, f.tenant).Scan(&f.mat))
	// 100 kg at $2.00/kg = $200.00 of grain.
	must("lot", pool.QueryRow(ctx, `
		INSERT INTO material_lots (tenant_id, material_id, quantity_received, quantity_on_hand, unit_cost_cad, received_at)
		VALUES ($1,$2,1000,1000,2.00,NOW()) RETURNING id`, f.tenant, f.mat).Scan(&f.lot))
	var recipe uuid.UUID
	must("recipe", pool.QueryRow(ctx, `
		INSERT INTO recipes (tenant_id, name, spirit_kind) VALUES ($1,'WIP rye','whisky')
		RETURNING id`, f.tenant).Scan(&recipe))
	must("recipe version", pool.QueryRow(ctx, `
		INSERT INTO recipe_versions (tenant_id, recipe_id, version_no) VALUES ($1,$2,1)
		RETURNING id`, f.tenant, recipe).Scan(&f.recipeVersion))
	return f
}

func TestProductionGaugeWIP_ApportionsAcrossTwoStills(t *testing.T) {
	f := newWIPFixture(t)
	pool := testdb.AdminPool(t)
	ctx := f.ctx
	must := func(what string, e error) {
		t.Helper()
		if e != nil {
			t.Fatalf("%s: %v", what, e)
		}
	}

	// One mash: 100 kg of rye at $2.00 = $200.00.
	var mash uuid.UUID
	must("mash", pool.QueryRow(ctx, `
		INSERT INTO mash_runs (tenant_id, recipe_version_id, mash_no, mash_date, status)
		VALUES ($1,$2,1,CURRENT_DATE,'distilled') RETURNING id`, f.tenant, f.recipeVersion).Scan(&mash))
	must("usage", exec(pool, ctx, `
		INSERT INTO mash_ingredient_usage (tenant_id, mash_run_id, material_id, material_lot_id, quantity_used, uom)
		VALUES ($1,$2,$3,$4,100,'kg')`, f.tenant, mash, f.mat, f.lot))

	// One fermentation off it — so no mash-level split is needed.
	var ferm uuid.UUID
	must("ferment", pool.QueryRow(ctx, `
		INSERT INTO fermentation_runs (tenant_id, mash_run_id, fermenter_label, pitch_at, initial_volume_l, status)
		VALUES ($1,$2,'F-A',NOW(),1000,'distilled') RETURNING id`, f.tenant, mash).Scan(&ferm))

	// Charged to two distillation runs: 750 L and 250 L. On the volume
	// basis that is a 75/25 split of the $200, so $150 and $50.
	gauge := func(runNo int, chargedL, abv, gaugeL, gaugeABV float64) uuid.UUID {
		t.Helper()
		var run, mv, g uuid.UUID
		must("run", pool.QueryRow(ctx, `
			INSERT INTO distillation_runs (tenant_id, run_no, still_label, run_date, status)
			VALUES ($1,$2,'Still 1',CURRENT_DATE,'gauged') RETURNING id`, f.tenant, runNo).Scan(&run))
		must("charge", exec(pool, ctx, `
			INSERT INTO distillation_charges (tenant_id, distillation_run_id, fermentation_run_id, volume_charged_l, abv_pct)
			VALUES ($1,$2,$3,$4,$5)`, f.tenant, run, ferm, chargedL, abv))
		must("movement", pool.QueryRow(ctx, `
			INSERT INTO bulk_movements (tenant_id, destination_container_id, volume_l, abv_pct, laa, reason, occurred_at)
			VALUES ($1,$2,$3,$4,$5,'production_gauge',NOW()) RETURNING id`,
			f.tenant, f.tank, gaugeL, gaugeABV, gaugeL*gaugeABV/100).Scan(&mv))
		must("gauge", pool.QueryRow(ctx, `
			INSERT INTO production_gauges (tenant_id, distillation_run_id, destination_container_id,
			                               bulk_movement_id, volume_l, abv_pct, gauger_user_id, gauge_date)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NOW()) RETURNING id`,
			f.tenant, run, f.tank, mv, gaugeL, gaugeABV, f.user).Scan(&g))
		return g
	}
	big := gauge(1, 750, 8, 100, 70)
	small := gauge(2, 250, 8, 30, 70)

	got := wipByID(t, f, "charged_volume")
	if len(got) != 2 {
		t.Fatalf("gauges: got %d, want 2", len(got))
	}
	if v := got[big.String()]; !v.Value.Available || !near(v.Value.AmountCAD, 150.00) {
		t.Errorf("75%% share: got %v (available=%v, missing=%q), want 150.00",
			v.Value.AmountCAD, v.Value.Available, v.Value.Missing)
	}
	if v := got[small.String()]; !v.Value.Available || !near(v.Value.AmountCAD, 50.00) {
		t.Errorf("25%% share: got %v (available=%v, missing=%q), want 50.00",
			v.Value.AmountCAD, v.Value.Available, v.Value.Missing)
	}

	// The mash cost must be conserved across the gauges it fed. This is
	// the property that makes the figure worth posting: apportionment that
	// does not sum back to what was spent is not apportionment.
	total := got[big.String()].Value.AmountCAD + got[small.String()].Value.AmountCAD
	if !near(total, 200.00) {
		t.Errorf("apportioned total: got %v, want the mash's 200.00", total)
	}
}

// The same charges on the LAA basis must split differently, or the basis
// is not doing anything. Here both charges are the same strength, so the
// split is identical — which is exactly why the test uses different
// strengths.
func TestProductionGaugeWIP_BasisChangesTheSplit(t *testing.T) {
	f := newWIPFixture(t)
	pool := testdb.AdminPool(t)
	ctx := f.ctx
	must := func(what string, e error) {
		t.Helper()
		if e != nil {
			t.Fatalf("%s: %v", what, e)
		}
	}

	var mash, ferm uuid.UUID
	must("mash", pool.QueryRow(ctx, `
		INSERT INTO mash_runs (tenant_id, recipe_version_id, mash_no, mash_date, status)
		VALUES ($1,$2,2,CURRENT_DATE,'distilled') RETURNING id`, f.tenant, f.recipeVersion).Scan(&mash))
	must("usage", exec(pool, ctx, `
		INSERT INTO mash_ingredient_usage (tenant_id, mash_run_id, material_id, material_lot_id, quantity_used, uom)
		VALUES ($1,$2,$3,$4,100,'kg')`, f.tenant, mash, f.mat, f.lot))
	must("ferment", pool.QueryRow(ctx, `
		INSERT INTO fermentation_runs (tenant_id, mash_run_id, fermenter_label, pitch_at, initial_volume_l, status)
		VALUES ($1,$2,'F-B',NOW(),1000,'distilled') RETURNING id`, f.tenant, mash).Scan(&ferm))

	// Equal volumes, different strengths: 500 L at 10% (50 LAA) and 500 L
	// at 5% (25 LAA). Volume basis splits 50/50; LAA basis splits 2:1.
	mk := func(runNo int, chargedL, abv float64) uuid.UUID {
		t.Helper()
		var run, mv, g uuid.UUID
		must("run", pool.QueryRow(ctx, `
			INSERT INTO distillation_runs (tenant_id, run_no, still_label, run_date, status)
			VALUES ($1,$2,'Still 1',CURRENT_DATE,'gauged') RETURNING id`, f.tenant, runNo).Scan(&run))
		must("charge", exec(pool, ctx, `
			INSERT INTO distillation_charges (tenant_id, distillation_run_id, fermentation_run_id, volume_charged_l, abv_pct)
			VALUES ($1,$2,$3,$4,$5)`, f.tenant, run, ferm, chargedL, abv))
		must("movement", pool.QueryRow(ctx, `
			INSERT INTO bulk_movements (tenant_id, destination_container_id, volume_l, abv_pct, laa, reason, occurred_at)
			VALUES ($1,$2,50,70,35,'production_gauge',NOW()) RETURNING id`, f.tenant, f.tank).Scan(&mv))
		must("gauge", pool.QueryRow(ctx, `
			INSERT INTO production_gauges (tenant_id, distillation_run_id, destination_container_id,
			                               bulk_movement_id, volume_l, abv_pct, gauger_user_id, gauge_date)
			VALUES ($1,$2,$3,$4,50,70,$5,NOW()) RETURNING id`, f.tenant, run, f.tank, mv, f.user).Scan(&g))
		return g
	}
	strong := mk(3, 500, 10)
	weak := mk(4, 500, 5)

	byVol := wipByID(t, f, "charged_volume")
	if v := byVol[strong.String()]; !near(v.Value.AmountCAD, 100.00) {
		t.Errorf("volume basis, strong: got %v, want 100.00", v.Value.AmountCAD)
	}
	if v := byVol[weak.String()]; !near(v.Value.AmountCAD, 100.00) {
		t.Errorf("volume basis, weak: got %v, want 100.00", v.Value.AmountCAD)
	}

	byLAA := wipByID(t, f, "charged_laa")
	if v := byLAA[strong.String()]; !near(v.Value.AmountCAD, 133.33) {
		t.Errorf("LAA basis, strong: got %v, want 133.33", v.Value.AmountCAD)
	}
	if v := byLAA[weak.String()]; !near(v.Value.AmountCAD, 66.67) {
		t.Errorf("LAA basis, weak: got %v, want 66.67", v.Value.AmountCAD)
	}
}

// An unset basis is a refusal, not a default. This is the whole reason
// E7 sat open, and the reason it can now close: Stillhouse states that it
// will not choose rather than choosing quietly.
func TestProductionGaugeWIP_UnsetBasisRefuses(t *testing.T) {
	f := newWIPFixture(t)
	got, err := ProductionGaugeWIP(f.ctx, f.q, "", time.Now().AddDate(0, -1, 0), time.Now().AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("ProductionGaugeWIP: %v", err)
	}
	if got.Refused == "" {
		t.Fatal("an unset basis produced a figure instead of a refusal")
	}
	if len(got.Gauges) != 0 || got.TotalCAD != 0 {
		t.Errorf("refused but still reported figures: %+v", got)
	}
}

// A mash with a material line carrying no lot is unvalued, and the gauge
// it fed must say so rather than reporting the priced remainder as if it
// were the cost.
func TestProductionGaugeWIP_UnpricedMashIsRefusedNotDiscounted(t *testing.T) {
	f := newWIPFixture(t)
	pool := testdb.AdminPool(t)
	ctx := f.ctx
	must := func(what string, e error) {
		t.Helper()
		if e != nil {
			t.Fatalf("%s: %v", what, e)
		}
	}

	var mash, ferm, run, mv, g uuid.UUID
	must("mash", pool.QueryRow(ctx, `
		INSERT INTO mash_runs (tenant_id, recipe_version_id, mash_no, mash_date, status)
		VALUES ($1,$2,3,CURRENT_DATE,'distilled') RETURNING id`, f.tenant, f.recipeVersion).Scan(&mash))
	// One priced line and one with no lot at all.
	must("priced", exec(pool, ctx, `
		INSERT INTO mash_ingredient_usage (tenant_id, mash_run_id, material_id, material_lot_id, quantity_used, uom)
		VALUES ($1,$2,$3,$4,50,'kg')`, f.tenant, mash, f.mat, f.lot))
	var mat2 uuid.UUID
	must("material2", pool.QueryRow(ctx, `
		INSERT INTO materials (tenant_id, name, kind, uom) VALUES ($1,'Barley','grain','kg')
		RETURNING id`, f.tenant).Scan(&mat2))
	must("unpriced", exec(pool, ctx, `
		INSERT INTO mash_ingredient_usage (tenant_id, mash_run_id, material_id, quantity_used, uom)
		VALUES ($1,$2,$3,50,'kg')`, f.tenant, mash, mat2))

	must("ferment", pool.QueryRow(ctx, `
		INSERT INTO fermentation_runs (tenant_id, mash_run_id, fermenter_label, pitch_at, initial_volume_l, status)
		VALUES ($1,$2,'F-C',NOW(),1000,'distilled') RETURNING id`, f.tenant, mash).Scan(&ferm))
	must("run", pool.QueryRow(ctx, `
		INSERT INTO distillation_runs (tenant_id, run_no, still_label, run_date, status)
		VALUES ($1,5,'Still 1',CURRENT_DATE,'gauged') RETURNING id`, f.tenant).Scan(&run))
	must("charge", exec(pool, ctx, `
		INSERT INTO distillation_charges (tenant_id, distillation_run_id, fermentation_run_id, volume_charged_l, abv_pct)
		VALUES ($1,$2,$3,1000,8)`, f.tenant, run, ferm))
	must("movement", pool.QueryRow(ctx, `
		INSERT INTO bulk_movements (tenant_id, destination_container_id, volume_l, abv_pct, laa, reason, occurred_at)
		VALUES ($1,$2,100,70,70,'production_gauge',NOW()) RETURNING id`, f.tenant, f.tank).Scan(&mv))
	must("gauge", pool.QueryRow(ctx, `
		INSERT INTO production_gauges (tenant_id, distillation_run_id, destination_container_id,
		                               bulk_movement_id, volume_l, abv_pct, gauger_user_id, gauge_date)
		VALUES ($1,$2,$3,$4,100,70,$5,NOW()) RETURNING id`, f.tenant, run, f.tank, mv, f.user).Scan(&g))

	got := wipByID(t, f, "charged_volume")
	v := got[g.String()]
	if v.Value.Available {
		t.Fatalf("an unpriced mash produced a cost of %v — the priced half is not the cost", v.Value.AmountCAD)
	}
	if v.Value.Missing == "" {
		t.Error("unavailable with no reason")
	}
}

// exec is a one-liner so the seeding above reads as a list of facts
// rather than a list of error checks.
func exec(pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) error {
	_, err := pool.Exec(ctx, sql, args...)
	return err
}

// wipByID runs the walk over a window wide enough to catch the fixture's
// rows and keys the result by gauge id.
func wipByID(t *testing.T, f *wipFixture, basis string) map[string]WIPGauge {
	t.Helper()
	got, err := ProductionGaugeWIP(f.ctx, f.q, basis,
		time.Now().AddDate(0, -1, 0), time.Now().AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("ProductionGaugeWIP(%s): %v", basis, err)
	}
	out := map[string]WIPGauge{}
	for _, g := range got.Gauges {
		out[g.ID] = g
	}
	return out
}

// near compares money to the cent.
func near(a, b float64) bool {
	d := a - b
	return d < 0.005 && d > -0.005
}

// A voided distillation run's spirit went back out of the ledger. Valuing
// its gauge would put a cost against alcohol that is not there, and
// voiding is how Stillhouse reverses a distillation — so this is the
// ordinary path, not an edge case.
func TestProductionGaugeWIP_VoidedRunsAreExcluded(t *testing.T) {
	f := newWIPFixture(t)
	pool := testdb.AdminPool(t)
	ctx := f.ctx
	must := func(what string, e error) {
		t.Helper()
		if e != nil {
			t.Fatalf("%s: %v", what, e)
		}
	}

	var mash, ferm, run, mv, g uuid.UUID
	must("mash", pool.QueryRow(ctx, `
		INSERT INTO mash_runs (tenant_id, recipe_version_id, mash_no, mash_date, status)
		VALUES ($1,$2,9,CURRENT_DATE,'distilled') RETURNING id`, f.tenant, f.recipeVersion).Scan(&mash))
	must("usage", exec(pool, ctx, `
		INSERT INTO mash_ingredient_usage (tenant_id, mash_run_id, material_id, material_lot_id, quantity_used, uom)
		VALUES ($1,$2,$3,$4,100,'kg')`, f.tenant, mash, f.mat, f.lot))
	must("ferment", pool.QueryRow(ctx, `
		INSERT INTO fermentation_runs (tenant_id, mash_run_id, fermenter_label, pitch_at, initial_volume_l, status)
		VALUES ($1,$2,'F-V',NOW(),1000,'distilled') RETURNING id`, f.tenant, mash).Scan(&ferm))
	must("run", pool.QueryRow(ctx, `
		INSERT INTO distillation_runs (tenant_id, run_no, still_label, run_date, status)
		VALUES ($1,9,'Still 1',CURRENT_DATE,'gauged') RETURNING id`, f.tenant).Scan(&run))
	must("charge", exec(pool, ctx, `
		INSERT INTO distillation_charges (tenant_id, distillation_run_id, fermentation_run_id, volume_charged_l, abv_pct)
		VALUES ($1,$2,$3,1000,8)`, f.tenant, run, ferm))
	must("movement", pool.QueryRow(ctx, `
		INSERT INTO bulk_movements (tenant_id, destination_container_id, volume_l, abv_pct, laa, reason, occurred_at)
		VALUES ($1,$2,100,70,70,'production_gauge',NOW()) RETURNING id`, f.tenant, f.tank).Scan(&mv))
	must("gauge", pool.QueryRow(ctx, `
		INSERT INTO production_gauges (tenant_id, distillation_run_id, destination_container_id,
		                               bulk_movement_id, volume_l, abv_pct, gauger_user_id, gauge_date)
		VALUES ($1,$2,$3,$4,100,70,$5,NOW()) RETURNING id`, f.tenant, run, f.tank, mv, f.user).Scan(&g))

	// Present while the run stands.
	if _, ok := wipByID(t, f, "charged_volume")[g.String()]; !ok {
		t.Fatal("the gauge is missing before the run is voided — the fixture is wrong")
	}

	must("void", exec(pool, ctx, `UPDATE distillation_runs SET voided_at = NOW() WHERE id = $1`, run))

	if v, ok := wipByID(t, f, "charged_volume")[g.String()]; ok {
		t.Errorf("a voided run still carried %v into WIP", v.Value.AmountCAD)
	}
}
