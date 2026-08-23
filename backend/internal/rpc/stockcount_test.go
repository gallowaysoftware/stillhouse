package rpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// A count sheet, and the thing that makes posting it safe: the packaged
// balance the B266 walks backwards from must stay reconcilable, so an
// adjustment is a row the walk can undo rather than an edit to the total.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestStockCountPostsAdjustmentsTheReturnCanSee(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewStockCountService(f.db, testLogger())
	b266 := NewB266Service(f.db, testLogger())

	_, lot := f.salesStock(t, 750, 40, 500)

	opened, err := svc.OpenStockCount(f.ctx, connect.NewRequest(
		&stillhousev1.OpenStockCountRequest{
			Name:  "August cycle count",
			Scope: stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_PACKAGED,
		}))
	if err != nil {
		t.Fatalf("OpenStockCount: %v", err)
	}
	count := opened.Msg.GetCount()
	if count.GetLineCount() == 0 {
		t.Fatal("a packaged count produced an empty sheet")
	}
	var line *stillhousev1.StockCountLine
	for _, l := range count.GetLines() {
		if l.GetPackagedInventoryId() == lot.ID.String() {
			line = l
		}
	}
	if line == nil {
		t.Fatal("the lot is not on the sheet")
	}
	if got, want := line.GetBookQuantity(), 500.0; got != want {
		t.Errorf("book = %v, want %v", got, want)
	}
	if line.GetCounted() {
		t.Error("a fresh line reads as counted before anybody counted it")
	}

	t.Run("a variance with no reason is refused", func(t *testing.T) {
		// A variance nobody explained is a number.
		if _, err := svc.RecordCount(f.ctx, connect.NewRequest(
			&stillhousev1.RecordCountRequest{
				LineId: line.GetId(), CountedQuantity: 488,
			})); err == nil {
			t.Error("a discrepancy was recorded with no reason")
		}
	})

	if _, err := svc.RecordCount(f.ctx, connect.NewRequest(
		&stillhousev1.RecordCountRequest{
			LineId: line.GetId(), CountedQuantity: 488,
			Reason:      stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
			Explanation: "one case short on the pallet",
			CountedBy:   "Kyle",
		})); err != nil {
		t.Fatalf("RecordCount: %v", err)
	}

	// The balance as at a moment before the count is what a return for
	// that period would have said. It must not move when the count posts.
	before := time.Now().UTC().AddDate(0, 0, -30)
	asOf := func(t *testing.T) (float64, int32) {
		t.Helper()
		var laa float64
		var bottles int32
		if err := f.db.WithTenantTx(f.ctx, f.tenant.ID,
			func(ctx context.Context, q *sqlcgen.Queries) error {
				got, e := q.SumPackagedOnHandAsOf(ctx, pgtype.Date{Valid: true, Time: before})
				if e != nil {
					return e
				}
				laa, bottles = got.TotalLaa, got.TotalBottles
				return nil
			}); err != nil {
			t.Fatalf("SumPackagedOnHandAsOf: %v", err)
		}
		return laa, bottles
	}
	laaBefore, bottlesBefore := asOf(t)

	posted, err := svc.PostStockCount(f.ctx, connect.NewRequest(
		&stillhousev1.PostStockCountRequest{Id: count.GetId()}))
	if err != nil {
		t.Fatalf("PostStockCount: %v", err)
	}
	if posted.Msg.GetAdjustmentsWritten() != 1 {
		t.Errorf("%d adjustments written, want 1", posted.Msg.GetAdjustmentsWritten())
	}

	t.Run("the lot is corrected", func(t *testing.T) {
		after, err := f.q.GetPackagedInventoryForUpdate(f.ctx, lot.ID)
		if err != nil {
			t.Fatalf("re-read lot: %v", err)
		}
		if got, want := after.BottlesOnHand, int32(488); got != want {
			t.Errorf("bottles = %d, want %d", got, want)
		}
	})

	t.Run("a period already filed does not restate", func(t *testing.T) {
		// This is the whole reason the adjustment is a row rather than an
		// edit. Without it the balance changes with nothing in the ledger
		// to undo, and a return signed a month ago quietly becomes a
		// different number.
		laaAfter, bottlesAfter := asOf(t)
		if bottlesAfter != bottlesBefore {
			t.Errorf("bottles as at %s moved from %d to %d after a count posted",
				before.Format("2006-01-02"), bottlesBefore, bottlesAfter)
		}
		if !near(laaAfter, laaBefore, 1e-6) {
			t.Errorf("LAA as at %s moved from %v to %v",
				before.Format("2006-01-02"), laaBefore, laaAfter)
		}
	})

	t.Run("it lands on line D's packaged half", func(t *testing.T) {
		today := time.Now().UTC()
		got, err := b266.GenerateB266(f.ctx, connect.NewRequest(
			&stillhousev1.GenerateB266Request{
				PeriodStart: today.AddDate(0, 0, -7).Format("2006-01-02"),
				PeriodEnd:   today.Format("2006-01-02"),
			}))
		if err != nil {
			t.Fatalf("GenerateB266: %v", err)
		}
		r := got.Msg.GetReport()
		if r.GetPackagedAdjustmentsCount() != 1 {
			t.Errorf("adjustment count = %d, want 1", r.GetPackagedAdjustmentsCount())
		}
		// 12 bottles × 0.75 L × 40 % = 3.6 LAA, missing.
		if got, want := r.GetPackagedAdjustmentsDecreaseLaa(), 3.6; !near(got, want, 1e-6) {
			t.Errorf("decrease = %v, want %v", got, want)
		}
		if got, want := r.GetPackagedAdjustmentsNetLaa(), -3.6; !near(got, want, 1e-6) {
			t.Errorf("net = %v, want %v", got, want)
		}
	})

	t.Run("posting twice is refused", func(t *testing.T) {
		if _, err := svc.PostStockCount(f.ctx, connect.NewRequest(
			&stillhousev1.PostStockCountRequest{Id: count.GetId()})); err == nil {
			t.Error("a posted count was posted again")
		}
	})

	t.Run("a posted count cannot be cancelled", func(t *testing.T) {
		if _, err := svc.CancelStockCount(f.ctx, connect.NewRequest(
			&stillhousev1.CancelStockCountRequest{
				Id: count.GetId(), Reason: "changed my mind",
			})); err == nil {
			t.Error("a count whose adjustments are in the ledger was cancelled")
		}
	})
}

// What the sheet cannot post, it names.
func TestStockCountNamesWhatItSkipped(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewStockCountService(f.db, testLogger())
	f.tank(t, "Count tank "+uuid.NewString()[:6], 500, 50)
	f.salesStock(t, 750, 40, 100)

	opened, err := svc.OpenStockCount(f.ctx, connect.NewRequest(
		&stillhousev1.OpenStockCountRequest{
			Scope: stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_ALL,
		}))
	if err != nil {
		t.Fatalf("OpenStockCount: %v", err)
	}
	count := opened.Msg.GetCount()

	// Count the vessel, leave the lot alone.
	for _, l := range count.GetLines() {
		if l.GetBulkContainerId() == "" {
			continue
		}
		if _, err := svc.RecordCount(f.ctx, connect.NewRequest(
			&stillhousev1.RecordCountRequest{
				LineId: l.GetId(), CountedQuantity: 480,
				CountedAbvPct: 50, CountedAbvPctSet: true,
				Reason:      stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
				Explanation: "dipped short",
			})); err != nil {
			t.Fatalf("RecordCount: %v", err)
		}
	}

	posted, err := svc.PostStockCount(f.ctx, connect.NewRequest(
		&stillhousev1.PostStockCountRequest{Id: count.GetId()}))
	if err != nil {
		t.Fatalf("PostStockCount: %v", err)
	}
	joined := strings.Join(posted.Msg.GetSkipped(), " | ")
	if !strings.Contains(joined, "never counted") {
		t.Errorf("an uncounted line was not named: %s", joined)
	}
	// The vessel is deliberately not posted from here: its adjustment is
	// a gauge determination with instruments and a 20 °C correction, and
	// duplicating that path would be a second implementation of the
	// arithmetic behind a B266 line.
	if !strings.Contains(joined, "vessel") {
		t.Errorf("the vessel line was not explained: %s", joined)
	}
}

// A volume with no strength says nothing about the alcohol.
func TestBulkCountNeedsAStrength(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewStockCountService(f.db, testLogger())
	f.tank(t, "Strengthless "+uuid.NewString()[:6], 500, 50)

	opened, err := svc.OpenStockCount(f.ctx, connect.NewRequest(
		&stillhousev1.OpenStockCountRequest{
			Scope: stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_BULK,
		}))
	if err != nil {
		t.Fatalf("OpenStockCount: %v", err)
	}
	lines := opened.Msg.GetCount().GetLines()
	if len(lines) == 0 {
		t.Fatal("no bulk lines")
	}
	if _, err := svc.RecordCount(f.ctx, connect.NewRequest(
		&stillhousev1.RecordCountRequest{
			LineId: lines[0].GetId(), CountedQuantity: 480,
			Reason:      stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
			Explanation: "dipped short",
		})); err == nil {
		t.Error("a vessel was counted by volume alone")
	}
}
