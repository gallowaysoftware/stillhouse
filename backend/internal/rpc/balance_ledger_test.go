package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// reconcile fails if any container's alcohol is not what it was seeded with
// plus what the ledger says went in, less what it says came out, or if its
// strength is not its alcohol over its litres.
func (f *ledgerFixture) reconcile(t *testing.T, seeded ...sqlcgen.BulkContainer) {
	t.Helper()
	for _, c := range seeded {
		var in, out float64
		if err := f.pool.QueryRow(f.ctx, `
			SELECT COALESCE(SUM(laa) FILTER (WHERE destination_container_id = $1), 0),
			       COALESCE(SUM(laa) FILTER (WHERE source_container_id = $1), 0)
			FROM bulk_movements`, c.ID).Scan(&in, &out); err != nil {
			t.Fatal(err)
		}
		now, err := f.q.GetBulkContainer(f.ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want := c.CurrentLaa + in - out; !near(now.CurrentLaa, want, 1e-6) {
			t.Errorf("%s holds %.6f LAA; seeded %.6f, ledger in %.6f and out %.6f make %.6f",
				c.Name, now.CurrentLaa, c.CurrentLaa, in, out, want)
		}
		if got := now.CurrentVolumeL * now.CurrentAbvPct.Float64 / 100; !near(got, now.CurrentLaa, 1e-6) {
			t.Errorf("%s: %.4f L at %.4f%% is %.6f LAA, and it holds %.6f",
				c.Name, now.CurrentVolumeL, now.CurrentAbvPct.Float64, got, now.CurrentLaa)
		}
	}
}

func refusedWith(t *testing.T, name string, err error, code connect.Code) {
	t.Helper()
	if connect.CodeOf(err) != code || err == nil {
		t.Errorf("%s: %v, want %v", name, err, code)
	}
}

// Every way alcohol moves leaves a ledger row for exactly what moved.
func TestNoAlcoholMovesWithoutAMovement(t *testing.T) {
	const precondition, invalid = connect.CodeFailedPrecondition, connect.CodeInvalidArgument
	log := slog.New(slog.DiscardHandler)

	t.Run("regauge: weaker with the same or more litres", func(t *testing.T) {
		f := newLedgerFixture(t)
		svc := NewBarrelService(f.db, log)
		tank, cask := f.tank(t, "T", 400, 62), f.barrel(t, "C", 250)
		_, err := svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
			BarrelId: cask.ID.String(), SourceContainerId: tank.ID.String(), VolumeL: 200, AbvPct: 62}))
		must(t, err)
		for _, g := range []struct{ vol, abv float64 }{{200, 61}, {200.5, 60}, {199, 59.5}, {198, 59.5}} {
			_, err := svc.RegaugeBarrel(f.ctx, connect.NewRequest(&stillhousev1.RegaugeBarrelRequest{
				BarrelId: cask.ID.String(), NewVolumeL: g.vol, NewAbvPct: g.abv}))
			must(t, err)
			f.reconcile(t, tank, cask)
		}
		_, err = svc.RegaugeBarrel(f.ctx, connect.NewRequest(&stillhousev1.RegaugeBarrelRequest{
			BarrelId: cask.ID.String(), NewVolumeL: 198}))
		refusedWith(t, "a regauge to no strength at all", err, precondition)
	})

	t.Run("dump: slightly under book with few litres lost", func(t *testing.T) {
		for _, g := range []struct{ vol, abv float64 }{{200, 61.5}, {199, 61.5}, {199.5, 61.9}, {201, 61}, {195, 61.8}} {
			f := newLedgerFixture(t)
			svc := NewBarrelService(f.db, log)
			tank, cask, dest := f.tank(t, "T", 400, 62), f.barrel(t, "C", 250), f.tank(t, "D", 100, 50)
			_, err := svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
				BarrelId: cask.ID.String(), SourceContainerId: tank.ID.String(), VolumeL: 200, AbvPct: 62}))
			must(t, err)
			_, err = svc.DumpBarrel(f.ctx, connect.NewRequest(&stillhousev1.DumpBarrelRequest{
				BarrelId: cask.ID.String(), DestinationContainerId: dest.ID.String(), VolumeL: g.vol, AbvPct: g.abv}))
			if err != nil {
				t.Errorf("dump of %v L at %v: %v", g.vol, g.abv, err)
			}
			f.reconcile(t, tank, cask, dest)
		}
	})

	t.Run("a cask into itself", func(t *testing.T) {
		f := newLedgerFixture(t)
		svc := NewBarrelService(f.db, log)
		cask := f.barrel(t, "C", 250).ID.String()
		_, err := svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
			BarrelId: cask, SourceContainerId: cask, VolumeL: 1, AbvPct: 60}))
		refusedWith(t, "fill", err, invalid)
		_, err = svc.DumpBarrel(f.ctx, connect.NewRequest(&stillhousev1.DumpBarrelRequest{
			BarrelId: cask, DestinationContainerId: cask, VolumeL: 1, AbvPct: 60}))
		refusedWith(t, "dump", err, invalid)
	})

	t.Run("emptying a tank at a gauge under its book strength", func(t *testing.T) {
		f := newLedgerFixture(t)
		tank, cask := f.tank(t, "T", 100, 60), f.barrel(t, "C", 250)
		_, err := NewBarrelService(f.db, log).FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
			BarrelId: cask.ID.String(), SourceContainerId: tank.ID.String(), VolumeL: 100, AbvPct: 59}))
		must(t, err)
		f.reconcile(t, tank, cask)
		var delta float64
		must(t, f.pool.QueryRow(f.ctx, "SELECT delta_laa FROM inventory_adjustments WHERE container_id = $1", tank.ID).Scan(&delta))
		if !near(delta, -1, 1e-6) {
			t.Errorf("line D carries %v, want -1", delta)
		}
	})

	t.Run("blend", func(t *testing.T) {
		f := newLedgerFixture(t)
		svc := NewBulkService(f.db, log)
		a, b, dest := f.tank(t, "A", 100, 60), f.tank(t, "B", 100, 40), f.tank(t, "D", 10, 50)
		src := func(c sqlcgen.BulkContainer, vol float64) *stillhousev1.BlendSourceInput {
			return &stillhousev1.BlendSourceInput{SourceContainerId: c.ID.String(), VolumeL: vol}
		}
		_, err := svc.CreateBlend(f.ctx, connect.NewRequest(&stillhousev1.CreateBlendRequest{
			DestinationContainerId: dest.ID.String(), Sources: []*stillhousev1.BlendSourceInput{src(a, 80), src(a, 80)}}))
		refusedWith(t, "the same tank twice", err, invalid)
		_, err = svc.CreateBlend(f.ctx, connect.NewRequest(&stillhousev1.CreateBlendRequest{
			DestinationContainerId: dest.ID.String(), Sources: []*stillhousev1.BlendSourceInput{src(a, 100), src(b, 33)}}))
		must(t, err)
		f.reconcile(t, a, b, dest)
	})

	t.Run("voided gauge, then another", func(t *testing.T) {
		f := newLedgerFixture(t)
		svc := NewDistillationService(f.db, log)
		tank := f.tank(t, "T", 0, 0)
		gauge := func(vol, abv float64) string {
			run, err := svc.CreateDistillationRun(f.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{StillLabel: "S"}))
			must(t, err)
			id := run.Msg.GetRun().GetId()
			_, err = svc.RecordProductionGauge(f.ctx, connect.NewRequest(&stillhousev1.RecordProductionGaugeRequest{
				DistillationRunId: id, DestinationContainerId: tank.ID.String(), VolumeL: vol, AbvPct: abv}))
			must(t, err)
			return id
		}
		gauge(100, 60)
		weak := gauge(100, 40)
		_, err := svc.VoidDistillationRun(f.ctx, connect.NewRequest(&stillhousev1.VoidDistillationRunRequest{Id: weak, Reason: "misread"}))
		must(t, err)
		f.reconcile(t, tank)
		gauge(10, 50)
		f.reconcile(t, tank)
		if vol, laa := f.balance(t, tank.ID); !near(vol, 110, 1e-6) || !near(laa, 65, 1e-6) {
			t.Errorf("110 L and 65 LAA expected, got %v and %v", vol, laa)
		}
	})

	t.Run("voiding a gauge already drawn on", func(t *testing.T) {
		f := newLedgerFixture(t)
		dist, redist := NewDistillationService(f.db, log), NewRedistillationService(f.db, log)
		tank := f.tank(t, "T", 0, 0)
		run, err := dist.CreateDistillationRun(f.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{StillLabel: "S"}))
		must(t, err)
		_, err = dist.RecordProductionGauge(f.ctx, connect.NewRequest(&stillhousev1.RecordProductionGaugeRequest{
			DistillationRunId: run.Msg.GetRun().GetId(), DestinationContainerId: tank.ID.String(), VolumeL: 100, AbvPct: 80}))
		must(t, err)
		start := func(vol, abv float64) error {
			_, err := redist.StartRedistillation(f.ctx, connect.NewRequest(&stillhousev1.StartRedistillationRequest{
				SourceContainerId: tank.ID.String(), VolumeL: vol, AbvPct: abv,
				Reason: stillhousev1.RedistillationReason_REDISTILLATION_REASON_FEINTS_RECOVERY}))
			return err
		}
		refusedWith(t, "more litres than the tank holds", start(120, 79), precondition)
		refusedWith(t, "a strength the tank does not have", start(100, 40), precondition)
		must(t, start(50, 80))
		f.reconcile(t, tank)
		_, err = dist.VoidDistillationRun(f.ctx, connect.NewRequest(&stillhousev1.VoidDistillationRunRequest{
			Id: run.Msg.GetRun().GetId(), Reason: "x"}))
		refusedWith(t, "void", err, precondition)
		f.reconcile(t, tank)
	})

	t.Run("a count with fewer litres and more alcohol", func(t *testing.T) {
		f := newLedgerFixture(t)
		svc := NewBulkService(f.db, log)
		for _, g := range []struct{ vol, abv float64 }{{98, 62}, {101, 58}, {100, 61}, {100, 60}, {99, 60.6060606}} {
			tank := f.tank(t, "T "+uuid.NewString(), 100, 60)
			_, err := svc.RecordInventoryAdjustment(f.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
				ContainerId: tank.ID.String(), CountedVolumeL: g.vol, AbvPct: g.abv, Explanation: "count",
				Reason: stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT}))
			must(t, err)
			f.reconcile(t, tank)
		}
	})

	t.Run("out of bond at a strength the tank does not have", func(t *testing.T) {
		f := newLedgerFixture(t)
		svc := NewBulkService(f.db, log)
		tank := f.tank(t, "T", 100, 60)
		out := func(vol, abv float64) error {
			_, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
				ContainerId: tank.ID.String(), VolumeL: vol, AbvPct: abv, CounterpartyName: "Other",
				DocumentReference: "D1",
				Kind:              stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_OUT}))
			return err
		}
		refusedWith(t, "95 %", out(50, 95), precondition)
		refusedWith(t, "no strength", out(50, 0), precondition)
		must(t, out(100, 59))
		f.reconcile(t, tank)
	})

	t.Run("archiving", func(t *testing.T) {
		f := newLedgerFixture(t)
		svc := NewBulkService(f.db, log)
		full, empty := f.tank(t, "F", 100, 60), f.tank(t, "E", 0, 0)
		archive := func(c sqlcgen.BulkContainer) error {
			_, err := svc.SetBulkContainerArchived(f.ctx, connect.NewRequest(&stillhousev1.SetBulkContainerArchivedRequest{
				Id: c.ID.String(), Archived: true}))
			return err
		}
		refusedWith(t, "a full tank", archive(full), precondition)
		must(t, archive(empty))
	})

	t.Run("overfilling", func(t *testing.T) {
		f := newLedgerFixture(t)
		a, b, dest := f.tank(t, "A", 200, 60), f.tank(t, "B", 200, 60), f.barrel(t, "D", 50)
		_, err := NewBulkService(f.db, log).CreateBlend(f.ctx, connect.NewRequest(&stillhousev1.CreateBlendRequest{
			DestinationContainerId: dest.ID.String(), Sources: []*stillhousev1.BlendSourceInput{
				{SourceContainerId: a.ID.String(), VolumeL: 100}, {SourceContainerId: b.ID.String(), VolumeL: 100}}}))
		refusedWith(t, "200 L into 50", err, precondition)
	})
}

func TestVoidingABarrelEventKeepsTheLedger(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.DiscardHandler))
	strong, weak, cask := f.tank(t, "S", 400, 62), f.tank(t, "W", 400, 60), f.barrel(t, "C", 250)
	fill := func(src sqlcgen.BulkContainer, vol, abv float64) string {
		r, err := svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
			BarrelId: cask.ID.String(), SourceContainerId: src.ID.String(), VolumeL: vol, AbvPct: abv}))
		must(t, err)
		return r.Msg.GetEvent().GetId()
	}
	fill(strong, 50, 62)
	second := fill(weak, 100, 60)
	_, err := svc.VoidBarrelEvent(f.ctx, connect.NewRequest(&stillhousev1.VoidBarrelEventRequest{Id: second, Reason: "wrong cask"}))
	must(t, err)
	f.reconcile(t, strong, weak, cask)
	if vol, laa := f.balance(t, cask.ID); !near(vol, 50, 1e-6) || !near(laa, 31, 1e-6) {
		t.Errorf("the cask holds %v L and %v LAA, want 50 and 31", vol, laa)
	}
}

// One writer, so the rule that strength follows alcohol cannot be skipped.
func TestOnlySaveBalanceWritesABalance(t *testing.T) {
	entries, err := os.ReadDir(".")
	must(t, err)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") || e.Name() == "balance.go" {
			continue
		}
		src, err := os.ReadFile(e.Name())
		must(t, err)
		if strings.Contains(string(src), "UpdateBulkContainerBalance(") {
			t.Errorf("%s writes a container balance itself; use saveBalance", e.Name())
		}
	}
}
