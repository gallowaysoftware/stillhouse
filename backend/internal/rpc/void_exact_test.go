package rpc

import (
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

type caskFixture struct {
	*stillFixture
	barrels *BarrelService
	cask    sqlcgen.BulkContainer
	when    *timestamppb.Timestamp
}

func newCaskFixture(t *testing.T, vol, abv float64) *caskFixture {
	t.Helper()
	s := newStillFixture(t)
	return &caskFixture{stillFixture: &stillFixture{ledgerFixture: s.ledgerFixture, dist: s.dist, b266: s.b266, tank: s.ledgerFixture.tank(t, "Source", vol, abv)},
		barrels: NewBarrelService(s.db, testLogger()), cask: s.barrel(t, "Cask", 250),
		when: timestamppb.New(time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC))}
}

func (c *caskFixture) fill(vol, abv float64) (string, error) {
	r, err := c.barrels.FillBarrel(c.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: c.cask.ID.String(), SourceContainerId: c.tank.ID.String(), VolumeL: vol, AbvPct: abv, EventDate: c.when}))
	if err != nil {
		return "", err
	}
	return r.Msg.GetEvent().GetId(), nil
}

func (c *caskFixture) dump(to sqlcgen.BulkContainer, vol, abv float64) (string, error) {
	r, err := c.barrels.DumpBarrel(c.ctx, connect.NewRequest(&stillhousev1.DumpBarrelRequest{
		BarrelId: c.cask.ID.String(), DestinationContainerId: to.ID.String(), VolumeL: vol, AbvPct: abv, EventDate: c.when}))
	if err != nil {
		return "", err
	}
	return r.Msg.GetEvent().GetId(), nil
}

func (c *caskFixture) regauge(vol, abv float64) (string, error) {
	r, err := c.barrels.RegaugeBarrel(c.ctx, connect.NewRequest(&stillhousev1.RegaugeBarrelRequest{
		BarrelId: c.cask.ID.String(), NewVolumeL: vol, NewAbvPct: abv, EventDate: c.when}))
	if err != nil {
		return "", err
	}
	return r.Msg.GetEvent().GetId(), nil
}

func (c *caskFixture) voidEvent(id string) error {
	_, err := c.barrels.VoidBarrelEvent(c.ctx, connect.NewRequest(&stillhousev1.VoidBarrelEventRequest{Id: id, Reason: "typo"}))
	return err
}

func ok(t *testing.T) func(string, error) string {
	return func(id string, err error) string {
		t.Helper()
		must(t, err)
		return id
	}
}

// holds checks the two vessels and that the return shows nothing but the
// given loss and correction.
func (c *caskFixture) holds(t *testing.T, what string, caskVol, caskLAA, tankVol, tankLAA, loss, lineD float64) {
	t.Helper()
	for _, v := range []struct {
		c        sqlcgen.BulkContainer
		vol, laa float64
	}{{c.cask, caskVol, caskLAA}, {c.tank, tankVol, tankLAA}} {
		if vol, laa := c.balance(t, v.c.ID); !near(vol, v.vol, 1e-6) || !near(laa, v.laa, 1e-6) {
			t.Errorf("%s: %s holds %v L and %v LAA, want %v and %v", what, v.c.Name, vol, laa, v.vol, v.laa)
		}
	}
	c.reconcile(t, c.tank, c.cask)
	r := c.period(t, "2026-09-01", "2026-09-30")
	if !near(r.GetBulkLossesLaa(), loss, 1e-4) || !near(r.GetBulkAdjustmentsLaa(), lineD, 1e-4) {
		t.Errorf("%s: September shows losses %v and line D %v, want %v and %v", what, r.GetBulkLossesLaa(), r.GetBulkAdjustmentsLaa(), loss, lineD)
	}
}

func TestVoidingACaskEventPutsBackEverything(t *testing.T) {
	must1 := ok(t)
	t.Run("a dump keyed short", func(t *testing.T) {
		c := newCaskFixture(t, 400, 60)
		dest := c.ledgerFixture.tank(t, "Dest", 0, 0)
		must1(c.fill(200, 60))
		typo := must1(c.dump(dest, 20, 60))
		c.holds(t, "dumped", 0, 0, 200, 120, 108, 0)
		must(t, c.voidEvent(typo))
		c.holds(t, "voided", 200, 120, 200, 120, 0, 0)
		must1(c.dump(dest, 198, 60))
		c.holds(t, "dumped properly", 0, 0, 200, 120, 1.2, 0)
	})
	t.Run("a fill that emptied the tank at a lower gauge", func(t *testing.T) {
		c := newCaskFixture(t, 200, 62.5)
		id := must1(c.fill(200, 62))
		c.holds(t, "filled", 200, 124, 0, 0, 0, -1)
		must(t, c.voidEvent(id))
		c.holds(t, "voided", 0, 0, 200, 125, 0, 0)
	})
	t.Run("a fill gauged above the book", func(t *testing.T) {
		c := newCaskFixture(t, 200, 62.5)
		id := must1(c.fill(200, 63))
		c.holds(t, "filled", 200, 126, 0, 0, 0, 1)
		must(t, c.voidEvent(id))
		c.holds(t, "voided", 0, 0, 200, 125, 0, 0)
	})
	t.Run("a regauge, and then the fill under it", func(t *testing.T) {
		c := newCaskFixture(t, 400, 60)
		fill := must1(c.fill(200, 60))
		gauge := must1(c.regauge(198, 59.5))
		c.holds(t, "regauged", 198, 117.81, 200, 120, 2.19, 0)
		refusedWith(t, "the fill under a regauge", c.voidEvent(fill), connect.CodeFailedPrecondition)
		must(t, c.voidEvent(gauge))
		c.holds(t, "regauge voided", 200, 120, 200, 120, 0, 0)
		must(t, c.voidEvent(fill))
		c.holds(t, "fill voided", 0, 0, 400, 240, 0, 0)
	})
	t.Run("a regauge with something after it", func(t *testing.T) {
		c := newCaskFixture(t, 400, 60)
		must1(c.fill(200, 60))
		first := must1(c.regauge(199, 60))
		must1(c.regauge(198, 60))
		refusedWith(t, "the earlier regauge", c.voidEvent(first), connect.CodeFailedPrecondition)
	})
	t.Run("a regauge with a count after it", func(t *testing.T) {
		c := newCaskFixture(t, 400, 60)
		must1(c.fill(200, 60))
		gauge := must1(c.regauge(198, 59.5))
		_, err := NewBulkService(c.db, testLogger()).RecordInventoryAdjustment(c.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
			ContainerId: c.cask.ID.String(), CountedVolumeL: 190, AbvPct: 59.5, Explanation: "count", OccurredAt: c.when,
			Reason: stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT}))
		must(t, err)
		refusedWith(t, "the regauge under a count", c.voidEvent(gauge), connect.CodeFailedPrecondition)
		c.holds(t, "counted", 190, 113.05, 200, 120, 2.19, -4.76)
	})
	t.Run("a dump of a cask filled since", func(t *testing.T) {
		c := newCaskFixture(t, 400, 60)
		dest := c.ledgerFixture.tank(t, "Dest", 0, 0)
		must1(c.fill(200, 60))
		dump := must1(c.dump(dest, 200, 60))
		again := must1(c.fill(200, 60))
		refusedWith(t, "the dump under a fill", c.voidEvent(dump), connect.CodeFailedPrecondition)
		must(t, c.voidEvent(again))
		must(t, c.voidEvent(dump))
		c.holds(t, "both voided", 200, 120, 200, 120, 0, 0)
	})
	t.Run("the clock", func(t *testing.T) {
		c := newCaskFixture(t, 400, 60)
		dest := c.ledgerFixture.tank(t, "Dest", 0, 0)
		clock := func() string {
			b, err := c.barrels.GetBarrel(c.ctx, connect.NewRequest(&stillhousev1.GetBarrelRequest{Id: c.cask.ID.String()}))
			must(t, err)
			return b.Msg.GetBarrel().GetFillDate()
		}
		c.when = timestamppb.New(time.Date(2022, 1, 1, 15, 0, 0, 0, time.UTC))
		misdated := must1(c.fill(200, 60))
		must(t, c.voidEvent(misdated))
		c.when = timestamppb.New(time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC))
		must1(c.fill(200, 60))
		if got := clock(); got != "2026-09-01" {
			t.Errorf("a mis-dated fill, voided, still dates the cask: %s", got)
		}
		c.when = timestamppb.New(time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC))
		mistaken := must1(c.dump(dest, 200, 60))
		must(t, c.voidEvent(mistaken))
		if got := clock(); got != "2026-09-01" {
			t.Errorf("a mistaken dump, voided, lost the fill date: %q", got)
		}
	})
}

// What the API shows for a vessel is enough to empty it.
func TestTheFiguresShownEmptyTheVessel(t *testing.T) {
	for i, v := range []struct{ vol, abv float64 }{{150, 60.66666667}, {211.33125, 61.512345}, {176.163482, 57.123456}, {99.99996, 63.995}} {
		c := newCaskFixture(t, v.vol, v.abv)
		shown, err := NewBulkService(c.db, testLogger()).GetBulkContainer(c.ctx, connect.NewRequest(&stillhousev1.GetBulkContainerRequest{Id: c.tank.ID.String()}))
		must(t, err)
		if _, err := c.fill(shown.Msg.GetContainer().GetCurrentVolumeL(), shown.Msg.GetContainer().GetCurrentAbvPct()); err != nil {
			t.Errorf("%d: filling with the figures shown: %v", i, err)
			continue
		}
		c.holds(t, "emptied", v.vol, v.vol*v.abv/100, 0, 0, 0, 0)
		cask, err := c.barrels.GetBarrel(c.ctx, connect.NewRequest(&stillhousev1.GetBarrelRequest{Id: c.cask.ID.String()}))
		must(t, err)
		b := cask.Msg.GetBarrel()
		if _, err := c.regauge(b.GetCurrentVolumeL(), b.GetCurrentAbvPct()); err != nil {
			t.Errorf("%d: regauging with the figures shown: %v", i, err)
		}
		if _, err := c.dump(c.ledgerFixture.tank(t, "D "+uuid.NewString(), 0, 0), b.GetCurrentVolumeL(), b.GetCurrentAbvPct()); err != nil {
			t.Errorf("%d: dumping with the figures shown: %v", i, err)
		}
	}
}

func TestAVesselWithSpiritInItIsNotArchived(t *testing.T) {
	c := newCaskFixture(t, 100, 60)
	svc := NewBulkService(c.db, testLogger())
	id, err := c.fill(100, 60)
	must(t, err)
	_, err = svc.SetBulkContainerArchived(c.ctx, connect.NewRequest(&stillhousev1.SetBulkContainerArchivedRequest{Id: c.tank.ID.String(), Archived: true}))
	must(t, err)
	_, err = svc.RecordInventoryAdjustment(c.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
		ContainerId: c.tank.ID.String(), CountedVolumeL: 5, AbvPct: 60, Explanation: "count",
		Reason: stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT}))
	refusedWith(t, "a count on an archived tank", err, connect.CodeFailedPrecondition)
	must(t, c.voidEvent(id))
	got, err := c.q.GetBulkContainer(c.ctx, c.tank.ID)
	must(t, err)
	if got.Archived {
		t.Error("the void put spirit into an archived tank, where no return counts it")
	}
}

func TestAVoidedAdoptionIsOffTheReturn(t *testing.T) {
	c := newCaskFixture(t, 0, 0)
	r, err := NewBulkService(c.db, testLogger()).AdoptOpeningInventory(c.ctx, connect.NewRequest(&stillhousev1.AdoptOpeningInventoryRequest{
		ContainerId: c.cask.ID.String(), VolumeL: 100, VolumeLSet: true, AbvPct: 60, FillDate: "2022-01-01", AsOf: c.when}))
	must(t, err)
	_ = r
	if got := c.period(t, "2026-09-01", "2026-09-30").GetBulkOpeningInventoryAdoptedLaa(); !near(got, 60, 1e-4) {
		t.Fatalf("adopted %v, want 60", got)
	}
	var id string
	must(t, c.pool.QueryRow(c.ctx, "SELECT id FROM barrel_events WHERE container_id = $1", c.cask.ID).Scan(&id))
	must(t, c.voidEvent(id))
	if got := c.period(t, "2026-09-01", "2026-09-30").GetBulkOpeningInventoryAdoptedLaa(); got != 0 {
		t.Errorf("a voided adoption is still on the return: %v", got)
	}
}

func TestGaugingAndVoidingAtOnce(t *testing.T) {
	s := newStillFixture(t)
	for range 12 {
		run := s.run(t)
		var wg sync.WaitGroup
		for _, f := range []func(){
			func() { _ = s.gauge(run, 7, 60, time.Now()) },
			func() { _ = s.void(run) },
		} {
			wg.Add(1)
			go func() { defer wg.Done(); f() }()
		}
		wg.Wait()
	}
	var loose int
	must(t, s.pool.QueryRow(s.ctx, `
		SELECT COUNT(*) FROM production_gauges pg JOIN distillation_runs dr ON dr.id = pg.distillation_run_id
		WHERE dr.tenant_id = $1 AND dr.voided_at IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM bulk_movements v WHERE v.reference_type = 'distillation_run_void' AND v.reference_id = dr.id)`, s.tenant.ID).Scan(&loose))
	if loose > 0 {
		t.Errorf("%d live gauges on voided runs", loose)
	}
	s.reconcile(t, s.tank)

	run, errs := s.run(t), make([]error, 4)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = s.gauge(run, 7, 60, time.Now()) }()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("a double gauge answers %v", err)
		}
	}
	if won != 1 {
		t.Errorf("%d of 4 gauges of one run were kept", won)
	}
}

func TestNothingIsFiledThatCannotBe(t *testing.T) {
	s := newStillFixture(t)
	refusedWith(t, "a gauge next year", s.gauge(s.run(t), 10, 60, time.Now().AddDate(1, 0, 0)), connect.CodeInvalidArgument)
	must(t, s.gauge(s.run(t), 100, 60, time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)))
	_, err := NewBulkService(s.db, testLogger()).RecordBulkExternalMovement(s.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: s.tank.ID.String(), VolumeL: 50, AbvPct: 60, CounterpartyName: "Other", DocumentReference: "D",
		OccurredAt: timestamppb.New(time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)),
		Kind:       stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_IN_BOND_OUT}))
	must(t, err)
	if r := s.period(t, "2026-08-01", "2026-08-31"); len(r.GetFilingBlockers()) == 0 {
		t.Errorf("August closes at %v LAA and nothing stops it being filed", r.GetBulkClosingLaa())
	}
}

// An entry is in the snapshot or refused, never accepted into a month as it
// is being filed.
func TestFilingAndEnteringAtOnce(t *testing.T) {
	s := newStillFixture(t)
	for _, m := range []string{"2026-04", "2026-05", "2026-06"} {
		start, err := time.Parse("2006-01", m)
		must(t, err)
		end := start.AddDate(0, 1, -1).Format("2006-01-02")
		gen, err := s.b266.GenerateB266(s.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{PeriodStart: m + "-01", PeriodEnd: end}))
		must(t, err)
		runs := []string{s.run(t), s.run(t), s.run(t), s.run(t)}
		var wg sync.WaitGroup
		kept := make([]bool, len(runs))
		for i, run := range runs {
			wg.Add(1)
			go func() { defer wg.Done(); kept[i] = s.gauge(run, 10, 60, start.AddDate(0, 0, 10+i)) == nil }()
		}
		var filed *connect.Response[stillhousev1.SubmitB266Response]
		wg.Add(1)
		go func() {
			defer wg.Done()
			filed, err = s.b266.SubmitB266(s.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
				PeriodId: gen.Msg.GetPeriod().GetId(), Acknowledgement: filingAcknowledgementText()}))
		}()
		wg.Wait()
		must(t, err)
		n := 0
		for _, k := range kept {
			if k {
				n++
			}
		}
		var made float64
		must(t, s.pool.QueryRow(s.ctx, "SELECT COALESCE((snapshot->>'bulkProductionLaa')::float8, 0) FROM b266_periods WHERE id = $1", gen.Msg.GetPeriod().GetId()).Scan(&made))
		if !near(made, float64(n)*6, 1e-4) {
			t.Errorf("%s: %d gauges were accepted and the filed return has %v LAA of production", m, n, made)
		}
		_ = filed
	}
}
