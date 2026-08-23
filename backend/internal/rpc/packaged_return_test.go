package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN D7. Returns restock and credit; the one thing they must never do
// is relieve duty. It crystallised when the goods were packaged or
// removed and does not un-crystallise because they came back — that is a
// refund claim with a B256 behind it, which is A9 and blocked.
//
// A return that quietly reduced duty payable would understate a filed
// return, which is the single failure this product exists to prevent.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// bottleAndRemove puts a run into packaged inventory and ships some of
// it duty-paid, returning the lot id and the removal id.
func (f *dutyFixture) bottleAndRemove(t *testing.T, bottles, removed int32) (string, string) {
	t.Helper()
	f.warehouseLicensed(t)
	f.stamps(t, "CA-ON", 5000)
	tank := f.tank(t, "Return tank "+uuid.NewString()[:6], 2000, 70)
	prod := f.product(t, "Return Vodka "+uuid.NewString()[:6], 750, 40)

	run, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: bottles,
		LotCode: "RET-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	lot := run.Msg.GetPackaged().GetId()

	rm, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: lot, BottlesRemoved: removed,
		DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
		DestinationName: "A retailer",
	}))
	if err != nil {
		t.Fatalf("CreateRemoval: %v", err)
	}
	return lot, rm.Msg.GetRemoval().GetId()
}

func (f *dutyFixture) bottlesOnHand(t *testing.T, lotID string) int32 {
	t.Helper()
	id, err := uuid.Parse(lotID)
	if err != nil {
		t.Fatalf("parse lot id: %v", err)
	}
	return f.lot(t, id).BottlesOnHand
}

func TestPackagedReturn_RestocksSaleableAndLeavesDutyAlone(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rem := NewRemovalService(f.db, log)
	b266 := NewB266Service(f.db, log)

	// A bottling run and a removal, so there is duty-paid stock in the
	// market to come back.
	lot, removal := f.bottleAndRemove(t, 120, 40)

	// The removal is dated today, so the period that contains the duty is
	// this month, not an arbitrary one.
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	periodEnd := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	before, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: periodStart, PeriodEnd: periodEnd,
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	dutyBefore := before.Msg.GetReport().GetDutyPayableCad()
	if dutyBefore <= 0 {
		t.Fatalf("fixture produced no duty: %v", dutyBefore)
	}

	resp, err := rem.RecordPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.RecordPackagedReturnRequest{
		PackagedInventoryId: lot,
		RemovalId:           removal,
		Bottles:             10,
		Condition:           stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_SALEABLE,
		ReturnedOn:          now.Format("2006-01-02"),
		Reason:              "delisted",
	}))
	if err != nil {
		t.Fatalf("RecordPackagedReturn: %v", err)
	}
	// The note is the product of this feature: the operator recording a
	// return is exactly who will otherwise assume the duty came back too.
	if !strings.Contains(resp.Msg.GetDutyNote(), "B256") {
		t.Errorf("duty note does not name the refund path: %q", resp.Msg.GetDutyNote())
	}
	// The duty that was paid on those bottles is carried on the row, so
	// the claim can be evidenced rather than asserted.
	if !resp.Msg.GetPackagedReturn().GetDutyPaidSet() {
		t.Error("no duty_paid recorded against a return matched to its removal")
	}

	after, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: periodStart, PeriodEnd: periodEnd,
	}))
	if err != nil {
		t.Fatalf("GenerateB266 after: %v", err)
	}
	if got := after.Msg.GetReport().GetDutyPayableCad(); !near(got, dutyBefore, 1e-6) {
		t.Errorf("a return changed duty payable: %v → %v. Duty does not un-crystallise "+
			"because goods came back; that is a B256 claim (PLAN A9).", dutyBefore, got)
	}
}

// The walk. Packaged closing balances are recovered by undoing what
// happened after the period end, and a saleable return adds bottles to
// the running total — so it has to be undone there too, or a period
// already filed silently restates.
func TestPackagedReturn_DoesNotRestateAFiledPeriod(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rem := NewRemovalService(f.db, log)
	b266 := NewB266Service(f.db, log)

	lot, removal := f.bottleAndRemove(t, 120, 40)

	before, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	closingBefore := before.Msg.GetReport().GetPackagedClosingBottles()

	// A return dated well AFTER the period closed.
	if _, err := rem.RecordPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.RecordPackagedReturnRequest{
		PackagedInventoryId: lot, RemovalId: removal, Bottles: 10,
		Condition:  stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_SALEABLE,
		ReturnedOn: time.Now().UTC().Format("2006-01-02"),
		Reason:     "returned months later",
	})); err != nil {
		t.Fatalf("RecordPackagedReturn: %v", err)
	}

	after, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 after: %v", err)
	}
	if got := after.Msg.GetReport().GetPackagedClosingBottles(); got != closingBefore {
		t.Errorf("April's closing bottles moved when stock came back in August: %d → %d. "+
			"The reverse walk must undo returns the way it undoes runs and removals.",
			closingBefore, got)
	}
}

// Unsaleable stock came back and stays off the shelf. Restocking it would
// put something that cannot be sold into a figure that says it can.
func TestPackagedReturn_UnsaleableDoesNotRestock(t *testing.T) {
	f := newDutyFixture(t)
	rem := NewRemovalService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	lot, removal := f.bottleAndRemove(t, 120, 40)

	onHandBefore := f.bottlesOnHand(t, lot)
	if _, err := rem.RecordPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.RecordPackagedReturnRequest{
		PackagedInventoryId: lot, RemovalId: removal, Bottles: 10,
		Condition:  stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_UNSALEABLE,
		ReturnedOn: "2026-04-20", Reason: "broken in transit",
	})); err != nil {
		t.Fatalf("RecordPackagedReturn: %v", err)
	}
	if got := f.bottlesOnHand(t, lot); got != onHandBefore {
		t.Errorf("unsaleable stock was restocked: %d → %d", onHandBefore, got)
	}
}

// You cannot get back more than went out. Without the check a mistyped
// count restocks bottles that never existed and the lot stops tying out.
func TestPackagedReturn_CannotReturnMoreThanLeft(t *testing.T) {
	f := newDutyFixture(t)
	rem := NewRemovalService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	lot, removal := f.bottleAndRemove(t, 120, 40)

	_, err := rem.RecordPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.RecordPackagedReturnRequest{
		PackagedInventoryId: lot, RemovalId: removal, Bottles: 100000,
		Condition:  stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_SALEABLE,
		ReturnedOn: "2026-04-20",
	}))
	if err == nil {
		t.Fatal("returned more bottles than were ever removed")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code: %v", connect.CodeOf(err))
	}
}

// Whether returned stock can be sold again is the decision the record
// exists to capture, so it is not defaulted.
func TestPackagedReturn_ConditionIsRequired(t *testing.T) {
	f := newDutyFixture(t)
	rem := NewRemovalService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	lot, removal := f.bottleAndRemove(t, 120, 40)

	_, err := rem.RecordPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.RecordPackagedReturnRequest{
		PackagedInventoryId: lot, RemovalId: removal, Bottles: 5,
		ReturnedOn: "2026-04-20",
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("unspecified condition accepted: %v", err)
	}
}

// A void takes back off exactly what it put on, and keeps the row.
func TestPackagedReturn_VoidReversesTheRestock(t *testing.T) {
	f := newDutyFixture(t)
	rem := NewRemovalService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	lot, removal := f.bottleAndRemove(t, 120, 40)
	before := f.bottlesOnHand(t, lot)

	got, err := rem.RecordPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.RecordPackagedReturnRequest{
		PackagedInventoryId: lot, RemovalId: removal, Bottles: 10,
		Condition:  stillhousev1.PackagedReturnCondition_PACKAGED_RETURN_CONDITION_SALEABLE,
		ReturnedOn: "2026-04-20",
	}))
	if err != nil {
		t.Fatalf("RecordPackagedReturn: %v", err)
	}
	if f.bottlesOnHand(t, lot) != before+10 {
		t.Fatalf("restock did not happen")
	}

	if _, err := rem.VoidPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.VoidPackagedReturnRequest{
		Id: got.Msg.GetPackagedReturn().GetId(), Reason: "recorded against the wrong lot",
	})); err != nil {
		t.Fatalf("VoidPackagedReturn: %v", err)
	}
	if after := f.bottlesOnHand(t, lot); after != before {
		t.Errorf("void did not reverse the restock: %d, want %d", after, before)
	}

	// A void needs a reason: it is the only record of why the row is there.
	_, err = rem.VoidPackagedReturn(f.ctx, connect.NewRequest(&stillhousev1.VoidPackagedReturnRequest{
		Id: got.Msg.GetPackagedReturn().GetId(),
	}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("void without a reason: %v", err)
	}
}
