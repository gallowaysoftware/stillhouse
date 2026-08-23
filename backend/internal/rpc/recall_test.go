package rpc

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// A recall walks the chain forward: from a material lot known to be bad,
// to everything that might carry it and everyone who received it.
//
// The design turns on one boundary. Up to the production gauge the chain
// is exact. Past it, spirit is blended and vatted, and the answer is
// possible contact rather than certainty. These tests are about that
// boundary holding in both directions — a recall that is too narrow leaves
// bad stock on a shelf, and one that is too wide destroys good stock.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

type recallSeed struct {
	lot       uuid.UUID
	container uuid.UUID
	gaugeDate time.Time
}

// seedRecallChain builds material lot → mash → ferment → run → gauge into
// a container, and returns what the tests need to reason about.
func seedRecallChain(t *testing.T, f *dutyFixture, gaugeAt time.Time) recallSeed {
	t.Helper()
	pool := testdb.AdminPool(t)
	ctx := f.ctx
	must := func(what string, e error) {
		t.Helper()
		if e != nil {
			t.Fatalf("%s: %v", what, e)
		}
	}
	var s recallSeed
	var mat, supplier, recipe, rv, mash, ferm, run, mv uuid.UUID

	must("supplier", pool.QueryRow(ctx, `
		INSERT INTO suppliers (tenant_id, name) VALUES ($1,'Bad Grain Co') RETURNING id`,
		f.tenant.ID).Scan(&supplier))
	must("material", pool.QueryRow(ctx, `
		INSERT INTO materials (tenant_id, name, kind, uom) VALUES ($1,'Rye','grain','kg') RETURNING id`,
		f.tenant.ID).Scan(&mat))
	must("lot", pool.QueryRow(ctx, `
		INSERT INTO material_lots (tenant_id, material_id, supplier_id, supplier_lot,
		                           quantity_received, quantity_on_hand, unit_cost_cad, received_at)
		VALUES ($1,$2,$3,'SL-999',1000,1000,2.00,NOW()) RETURNING id`,
		f.tenant.ID, mat, supplier).Scan(&s.lot))

	must("recipe", pool.QueryRow(ctx, `
		INSERT INTO recipes (tenant_id, name, spirit_kind) VALUES ($1,'Recall rye','whisky') RETURNING id`,
		f.tenant.ID).Scan(&recipe))
	must("version", pool.QueryRow(ctx, `
		INSERT INTO recipe_versions (tenant_id, recipe_id, version_no) VALUES ($1,$2,1) RETURNING id`,
		f.tenant.ID, recipe).Scan(&rv))
	must("mash", pool.QueryRow(ctx, `
		INSERT INTO mash_runs (tenant_id, recipe_version_id, mash_no, mash_date, status)
		VALUES ($1,$2,41,$3,'distilled') RETURNING id`,
		f.tenant.ID, rv, gaugeAt.AddDate(0, 0, -10)).Scan(&mash))
	must("usage", execCtx(pool, ctx, `
		INSERT INTO mash_ingredient_usage (tenant_id, mash_run_id, material_id, material_lot_id, quantity_used, uom)
		VALUES ($1,$2,$3,$4,500,'kg')`, f.tenant.ID, mash, mat, s.lot))
	must("ferment", pool.QueryRow(ctx, `
		INSERT INTO fermentation_runs (tenant_id, mash_run_id, fermenter_label, pitch_at, initial_volume_l, status)
		VALUES ($1,$2,'F-R',$3,1000,'distilled') RETURNING id`,
		f.tenant.ID, mash, gaugeAt.AddDate(0, 0, -8)).Scan(&ferm))
	must("run", pool.QueryRow(ctx, `
		INSERT INTO distillation_runs (tenant_id, run_no, still_label, run_date, status)
		VALUES ($1,41,'Still 1',$2,'gauged') RETURNING id`,
		f.tenant.ID, gaugeAt).Scan(&run))
	must("charge", execCtx(pool, ctx, `
		INSERT INTO distillation_charges (tenant_id, distillation_run_id, fermentation_run_id, volume_charged_l, abv_pct)
		VALUES ($1,$2,$3,1000,8)`, f.tenant.ID, run, ferm))

	tank := f.tank(t, "Recall receiver", 0, 0)
	s.container = tank.ID
	must("movement", pool.QueryRow(ctx, `
		INSERT INTO bulk_movements (tenant_id, destination_container_id, volume_l, abv_pct, laa, reason, occurred_at)
		VALUES ($1,$2,100,70,70,'production_gauge',$3) RETURNING id`,
		f.tenant.ID, tank.ID, gaugeAt).Scan(&mv))
	must("gauge", execCtx(pool, ctx, `
		INSERT INTO production_gauges (tenant_id, distillation_run_id, destination_container_id,
		                               bulk_movement_id, volume_l, abv_pct, gauger_user_id, gauge_date)
		VALUES ($1,$2,$3,$4,100,70,$5,$6)`,
		f.tenant.ID, run, tank.ID, mv, f.user.ID, gaugeAt))
	s.gaugeDate = gaugeAt
	return s
}

func execCtx(pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) error {
	_, err := pool.Exec(ctx, sql, args...)
	return err
}

// bottle seeds a bottling run off a container and its packaged lot,
// returning the packaged_inventory id.
func bottle(t *testing.T, f *dutyFixture, container uuid.UUID, on time.Time, lotCode string, bottles int32) uuid.UUID {
	t.Helper()
	pool := testdb.AdminPool(t)
	var product, run, pi uuid.UUID
	if err := pool.QueryRow(f.ctx, `
		INSERT INTO products (tenant_id, name, spirit_kind, bottle_size_ml, target_abv_pct)
		VALUES ($1,$2,'whisky',750,40) RETURNING id`, f.tenant.ID, "Recall whisky "+lotCode).Scan(&product); err != nil {
		t.Fatalf("product: %v", err)
	}
	// The draw out of the tank that the run is recorded against.
	var draw uuid.UUID
	if err := pool.QueryRow(f.ctx, `
		INSERT INTO bulk_movements (tenant_id, source_container_id, volume_l, abv_pct, laa, reason, occurred_at)
		VALUES ($1,$2,100,40,40,'transfer_to_packaging',$3) RETURNING id`,
		f.tenant.ID, container, on).Scan(&draw); err != nil {
		t.Fatalf("bottling draw: %v", err)
	}
	if err := pool.QueryRow(f.ctx, `
		INSERT INTO bottling_runs (tenant_id, run_no, product_id, source_container_id, destination_jurisdiction,
		                           bottling_date, bottle_count, lot_code, tank_gauge_volume_l,
		                           tank_gauge_abv_pct, tank_gauge_laa, bulk_movement_id)
		VALUES ($1, floor(random()*100000)::int, $2,$3,'CA-NS',$4,$5,$6,100,40,40,$7) RETURNING id`,
		f.tenant.ID, product, container, on, bottles, lotCode, draw).Scan(&run); err != nil {
		t.Fatalf("bottling run: %v", err)
	}
	if err := pool.QueryRow(f.ctx, `
		INSERT INTO packaged_inventory (tenant_id, product_id, lot_code, jurisdiction, bottling_run_id,
		                                bottles_on_hand, bottles_packaged, bottles_removed)
		VALUES ($1,$2,$3,'CA-NS',$4,$5,$5,0) RETURNING id`,
		f.tenant.ID, product, lotCode, run, bottles).Scan(&pi); err != nil {
		t.Fatalf("packaged inventory: %v", err)
	}
	return pi
}

// The exact half must name the lot's supplier, the mash it went into and
// the gauge it reached — one up and the recorded part of one down.
func TestSimulateRecall_ExactChainToTheGauge(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewTraceabilityService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	seed := seedRecallChain(t, f, time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))

	resp, err := svc.SimulateRecall(f.ctx, connect.NewRequest(&stillhousev1.SimulateRecallRequest{
		MaterialLotId: seed.lot.String(),
	}))
	if err != nil {
		t.Fatalf("SimulateRecall: %v", err)
	}
	m := resp.Msg
	if m.GetSupplierName() != "Bad Grain Co" || m.GetSupplierLot() != "SL-999" {
		t.Errorf("one up: supplier %q lot %q", m.GetSupplierName(), m.GetSupplierLot())
	}
	if len(m.GetMashes()) != 1 || m.GetMashes()[0].GetMashNo() != 41 {
		t.Fatalf("mashes: %+v", m.GetMashes())
	}
	if len(m.GetGauges()) != 1 {
		t.Fatalf("gauges: got %d, want 1: %+v", len(m.GetGauges()), m.GetGauges())
	}
	if g := m.GetGauges()[0]; g.GetContainerName() != "Recall receiver" || g.GetVoided() {
		t.Errorf("gauge: %+v", g)
	}
	// The note is the product of this feature, not decoration: the person
	// reading a recall is not reading the documentation.
	if !strings.Contains(m.GetExactnessNote(), "could carry it") &&
		!strings.Contains(m.GetExactnessNote(), "COULD carry it") {
		t.Errorf("exactness note does not state the boundary: %q", m.GetExactnessNote())
	}
}

// A bottling run that drew from the container BEFORE the affected spirit
// arrived cannot contain it. Including it would destroy good stock.
func TestSimulateRecall_BottlingBeforeArrivalIsNotImplicated(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewTraceabilityService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	gaugeAt := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	seed := seedRecallChain(t, f, gaugeAt)

	before := bottle(t, f, seed.container, gaugeAt.AddDate(0, 0, -5), "LOT-BEFORE", 100)
	after := bottle(t, f, seed.container, gaugeAt.AddDate(0, 0, 3), "LOT-AFTER", 200)
	_ = before

	resp, err := svc.SimulateRecall(f.ctx, connect.NewRequest(&stillhousev1.SimulateRecallRequest{
		MaterialLotId: seed.lot.String(),
	}))
	if err != nil {
		t.Fatalf("SimulateRecall: %v", err)
	}
	got := map[string]bool{}
	for _, l := range resp.Msg.GetPackagedLots() {
		got[l.GetLotCode()] = true
	}
	if got["LOT-BEFORE"] {
		t.Error("a lot bottled before the affected spirit arrived was implicated — that destroys good stock")
	}
	if !got["LOT-AFTER"] {
		t.Errorf("a lot bottled after the spirit arrived was NOT implicated — that leaves bad stock on a shelf: %+v",
			resp.Msg.GetPackagedLots())
	}
	if resp.Msg.GetBottlesPackaged() != 200 {
		t.Errorf("bottles packaged: got %d, want only the 200 bottled after", resp.Msg.GetBottlesPackaged())
	}
	_ = after
}

// One down: the removal list is what a recall notice is written from, so
// the customer has to be on it.
func TestSimulateRecall_NamesWhoReceivedIt(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewTraceabilityService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	gaugeAt := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	seed := seedRecallChain(t, f, gaugeAt)
	pi := bottle(t, f, seed.container, gaugeAt.AddDate(0, 0, 3), "LOT-SHIPPED", 200)

	pool := testdb.AdminPool(t)
	var cust uuid.UUID
	if err := pool.QueryRow(f.ctx, `
		INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'The Bottle Shop','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust); err != nil {
		t.Fatalf("customer: %v", err)
	}
	if _, err := pool.Exec(f.ctx, `
		INSERT INTO packaging_removals (tenant_id, removal_no, packaged_inventory_id, customer_id, destination_name,
		                                bottles_removed, removal_date, bottle_size_ml, bottle_abv_pct,
		                                total_litres, total_laa, duty_rate_per_laa, duty_amount_cad)
		VALUES ($1, floor(random()*100000)::int, $2,$3,'The Bottle Shop',50,$4, 750, 40, 37.5, 15, 13.864, 207.96)`,
		f.tenant.ID, pi, cust, gaugeAt.AddDate(0, 0, 5)); err != nil {
		t.Fatalf("removal: %v", err)
	}

	resp, err := svc.SimulateRecall(f.ctx, connect.NewRequest(&stillhousev1.SimulateRecallRequest{
		MaterialLotId: seed.lot.String(),
	}))
	if err != nil {
		t.Fatalf("SimulateRecall: %v", err)
	}
	if len(resp.Msg.GetRemovals()) != 1 {
		t.Fatalf("removals: got %d, want 1: %+v", len(resp.Msg.GetRemovals()), resp.Msg.GetRemovals())
	}
	r := resp.Msg.GetRemovals()[0]
	if r.GetCustomerName() != "The Bottle Shop" || r.GetBottles() != 50 {
		t.Errorf("removal: %+v", r)
	}
}

// A lot that has never been mashed has made nothing, and that must not
// read the same as a lot that cannot be found.
func TestSimulateRecall_UnusedLotSaysSo(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewTraceabilityService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	resp, err := svc.SimulateRecall(f.ctx, connect.NewRequest(&stillhousev1.SimulateRecallRequest{
		MaterialLotId: uuid.NewString(),
	}))
	if err != nil {
		t.Fatalf("SimulateRecall: %v", err)
	}
	if resp.Msg.GetNote() == "" {
		t.Error("an unused lot produced an empty result with no explanation")
	}
	if len(resp.Msg.GetPackagedLots()) != 0 {
		t.Error("an unused lot implicated packaged stock")
	}
}

// A voided distillation run's spirit went back out of the ledger, so it
// is not in any bottle and must not widen the search. Without this, a
// void — which is how Stillhouse reverses a distillation, so an ordinary
// event — would implicate every lot bottled from that tank afterwards.
func TestSimulateRecall_VoidedRunDoesNotWidenTheSearch(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewTraceabilityService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	gaugeAt := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	seed := seedRecallChain(t, f, gaugeAt)
	bottle(t, f, seed.container, gaugeAt.AddDate(0, 0, 3), "LOT-AFTER-VOID", 200)

	// Standing: the lot after the gauge is implicated.
	first, err := svc.SimulateRecall(f.ctx, connect.NewRequest(&stillhousev1.SimulateRecallRequest{
		MaterialLotId: seed.lot.String(),
	}))
	if err != nil {
		t.Fatalf("SimulateRecall: %v", err)
	}
	if len(first.Msg.GetPackagedLots()) != 1 {
		t.Fatalf("fixture is wrong: expected the lot to be implicated before the void, got %+v",
			first.Msg.GetPackagedLots())
	}

	if _, err := testdb.AdminPool(t).Exec(f.ctx,
		`UPDATE distillation_runs SET voided_at = NOW() WHERE tenant_id = $1`, f.tenant.ID); err != nil {
		t.Fatalf("void: %v", err)
	}

	after, err := svc.SimulateRecall(f.ctx, connect.NewRequest(&stillhousev1.SimulateRecallRequest{
		MaterialLotId: seed.lot.String(),
	}))
	if err != nil {
		t.Fatalf("SimulateRecall after void: %v", err)
	}
	if n := len(after.Msg.GetPackagedLots()); n != 0 {
		t.Errorf("a voided run still implicated %d packaged lot(s) — its spirit is not on any shelf", n)
	}
	if after.Msg.GetBottlesPackaged() != 0 {
		t.Errorf("bottles implicated after void: %d", after.Msg.GetBottlesPackaged())
	}
	// The gauge is still reported, marked voided: the operator asked what
	// happened to this lot, and "it was distilled and then reversed" is
	// part of the answer.
	if len(after.Msg.GetGauges()) != 1 || !after.Msg.GetGauges()[0].GetVoided() {
		t.Errorf("the voided gauge should still be listed and flagged: %+v", after.Msg.GetGauges())
	}
}
