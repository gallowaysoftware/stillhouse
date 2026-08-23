package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// A cost that is missing a component must say which, not report a smaller
// number. This is the whole argument of the stage: a distillery whose
// cost of sales is the price of barley prices its whisky accordingly.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestFullCostSaysWhatItIsMissing(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewCostingService(f.db, testLogger())

	tank := f.tank(t, "Cost "+uuid.NewString()[:6], 1000, 60)
	product, err := f.q.CreateProduct(f.ctx, sqlcgen.CreateProductParams{
		TenantID: f.tenant.ID, Name: "Cost " + uuid.NewString()[:8],
		SpiritKind: sqlcgen.SpiritKindVodka, BottleSizeMl: 750, TargetAbvPct: 40,
	})
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	// Bottling consumes stamps, and a run with none is refused before it
	// gets anywhere near a cost.
	f.seedStamps(t, "CA-ON", 1000)

	bottling := NewBottlingService(f.db, testLogger())
	run, err := bottling.CreateBottlingRun(f.ctx, connect.NewRequest(
		&stillhousev1.CreateBottlingRunRequest{
			ProductId: product.ID.String(), SourceContainerId: tank.ID.String(),
			DestinationJurisdiction: "CA-ON", BottleCount: 400,
			LotCode: "COST-" + uuid.NewString()[:8], BottlingDate: "2026-08-01",
		}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	runID := run.Msg.GetRun().GetId()

	cost := func(t *testing.T) *stillhousev1.BottlingRunFullCostResponse {
		t.Helper()
		got, err := svc.BottlingRunFullCost(f.ctx, connect.NewRequest(
			&stillhousev1.BottlingRunFullCostRequest{BottlingRunId: runID}))
		if err != nil {
			t.Fatalf("BottlingRunFullCost: %v", err)
		}
		return got.Msg
	}

	t.Run("with no rates set, both components say so", func(t *testing.T) {
		c := cost(t)
		if c.GetLabour().GetAvailable() || c.GetOverhead().GetAvailable() {
			t.Fatal("labour or overhead was computed with no rates set")
		}
		if c.GetLabour().GetMissing() == "" || c.GetOverhead().GetMissing() == "" {
			t.Error("an unavailable component must say why")
		}
		if c.GetComplete() {
			t.Error("a cost with no labour and no overhead in it claimed to be complete")
		}
		if c.GetLabour().GetAmountCad() != 0 {
			t.Error("an unavailable component must not contribute an amount")
		}
	})

	if _, err := svc.SaveCostRates(f.ctx, connect.NewRequest(
		&stillhousev1.SaveCostRatesRequest{
			EffectiveFrom:        "2026-01-01",
			LabourRateCadPerHour: "32.50",
			OverheadBasis:        stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LABOUR_HOUR,
			OverheadRate:         "18.00",
		})); err != nil {
		t.Fatalf("SaveCostRates: %v", err)
	}

	t.Run("rates without hours still cannot absorb anything", func(t *testing.T) {
		// Zero hours recorded is not zero labour; it is no record of any.
		c := cost(t)
		if c.GetLabour().GetAvailable() {
			t.Error("labour was absorbed with no hours recorded")
		}
		if c.GetOverhead().GetAvailable() {
			t.Error("overhead absorbing per labour hour was computed with no hours")
		}
	})

	if _, err := svc.RecordLabour(f.ctx, connect.NewRequest(
		&stillhousev1.RecordLabourRequest{
			Subject:      &stillhousev1.LabourSubject{BottlingRunId: runID},
			WorkedOn:     "2026-08-01",
			Hours:        6,
			WorkedByName: "Kyle",
		})); err != nil {
		t.Fatalf("RecordLabour: %v", err)
	}

	t.Run("with hours, both components land and state their basis", func(t *testing.T) {
		c := cost(t)
		if !c.GetLabour().GetAvailable() {
			t.Fatalf("labour still unavailable: %s", c.GetLabour().GetMissing())
		}
		if got, want := c.GetLabour().GetAmountCad(), 195.0; !near(got, want, 1e-6) {
			t.Errorf("labour = %v, want 6 h × $32.50 = %v", got, want)
		}
		if got, want := c.GetOverhead().GetAmountCad(), 108.0; !near(got, want, 1e-6) {
			t.Errorf("overhead = %v, want 6 h × $18.00 = %v", got, want)
		}
		if c.GetLabour().GetBasis() == "" || c.GetOverhead().GetBasis() == "" {
			t.Error("a component that landed must say what it is made of")
		}
		if got, want := c.GetLabourHours(), 6.0; got != want {
			t.Errorf("hours = %v, want %v", got, want)
		}
		// 195 + 108 over 400 bottles, plus whatever materials came to.
		if c.GetPerBottleCad() <= 0 {
			t.Error("per bottle is zero with 400 bottles and $303 of conversion cost")
		}
	})

	t.Run("rates are read as at the bottling date, not as at now", func(t *testing.T) {
		// A rate that starts after the run must not touch it — otherwise
		// changing a rate restates every batch already costed, including
		// those an accountant has taken into a set of books.
		before := cost(t).GetLabour().GetAmountCad()
		if _, err := svc.SaveCostRates(f.ctx, connect.NewRequest(
			&stillhousev1.SaveCostRatesRequest{
				EffectiveFrom:        "2026-09-01",
				LabourRateCadPerHour: "99.00",
				OverheadBasis:        stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LABOUR_HOUR,
				OverheadRate:         "50.00",
			})); err != nil {
			t.Fatalf("SaveCostRates: %v", err)
		}
		if got := cost(t).GetLabour().GetAmountCad(); !near(got, before, 1e-9) {
			t.Errorf("an August run was restated to %v by a September rate (was %v)",
				got, before)
		}
	})

	t.Run("half a policy is refused", func(t *testing.T) {
		if _, err := svc.SaveCostRates(f.ctx, connect.NewRequest(
			&stillhousev1.SaveCostRatesRequest{
				EffectiveFrom: "2026-02-01",
				OverheadBasis: stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LAA,
			})); err == nil {
			t.Error("an overhead basis with no rate was accepted, and would absorb nothing")
		}
	})

	t.Run("hours are worked on exactly one thing", func(t *testing.T) {
		if _, err := svc.RecordLabour(f.ctx, connect.NewRequest(
			&stillhousev1.RecordLabourRequest{
				Subject: &stillhousev1.LabourSubject{}, Hours: 2,
			})); err == nil {
			t.Error("hours were recorded against nothing")
		}
		if _, err := svc.RecordLabour(f.ctx, connect.NewRequest(
			&stillhousev1.RecordLabourRequest{
				Subject: &stillhousev1.LabourSubject{
					BottlingRunId: runID, WorkOrderId: uuid.NewString(),
				},
				Hours: 2,
			})); err == nil {
			t.Error("hours were recorded against two things at once")
		}
	})

	t.Run("a day cannot hold more than a day of work", func(t *testing.T) {
		if _, err := svc.RecordLabour(f.ctx, connect.NewRequest(
			&stillhousev1.RecordLabourRequest{
				Subject: &stillhousev1.LabourSubject{BottlingRunId: runID}, Hours: 30,
			})); err == nil {
			t.Error("thirty hours were booked to one day")
		}
	})

	t.Run("the inventory says what it could not value", func(t *testing.T) {
		got, err := svc.InventoryValue(f.ctx, connect.NewRequest(
			&stillhousev1.InventoryValueRequest{}))
		if err != nil {
			t.Fatalf("InventoryValue: %v", err)
		}
		msg := got.Msg
		if msg.GetBasis() == "" {
			t.Error("a valuation with no stated basis is a number nobody can check")
		}
		// The tank was drawn from, so it has a costed chain; the lot came
		// from a priced run. Whatever the figures, every unvalued line
		// must carry a reason.
		for _, b := range []*stillhousev1.InventoryBucket{msg.GetWip(), msg.GetFinishedGoods()} {
			for _, l := range b.GetLines() {
				if !l.GetValued() && l.GetWhy() == "" {
					t.Errorf("%s could not be valued and does not say why", l.GetName())
				}
				if l.GetValued() && l.GetValueCad() <= 0 {
					t.Errorf("%s is marked valued at %v", l.GetName(), l.GetValueCad())
				}
			}
		}
		if msg.GetFinishedGoods().GetTotalLaa() <= 0 {
			t.Error("400 bottles of 40 % vodka is not zero LAA of finished goods")
		}
	})
}

// seedStamps receives an excise stamp order, which bottling consumes.
func (f *ledgerFixture) seedStamps(t *testing.T, jurisdiction string, count int32) {
	t.Helper()
	order, err := f.q.CreateStampOrder(f.ctx, sqlcgen.CreateStampOrderParams{
		TenantID: f.tenant.ID, Jurisdiction: jurisdiction, QuantityOrdered: count,
	})
	if err != nil {
		t.Fatalf("create stamp order: %v", err)
	}
	if _, err := f.q.ReceiveStampOrder(f.ctx, sqlcgen.ReceiveStampOrderParams{
		ID:               order.ID,
		ReceivedAt:       pgtype.Timestamptz{Valid: true, Time: time.Now()},
		QuantityReceived: count,
	}); err != nil {
		t.Fatalf("receive stamp order: %v", err)
	}
}
