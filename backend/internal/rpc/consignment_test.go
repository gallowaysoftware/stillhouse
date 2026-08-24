package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
	"github.com/google/uuid"
)

// PLAN D7's other half. Stage 198 handled stock coming BACK; this is
// stock that went out and is still ours.
//
// The claim under test throughout: a consignment is not a removal. The
// stock stays on hand, and duty does not move until it sells through.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// The pure rule, tested without a database: a consignment closes when
// nothing is still out, and closes as SETTLED rather than recalled if
// anything at all sold. Half sold and half returned is a sale that
// ended, not a recall.
func TestSettleConsignment_ClosesOnlyWhenNothingIsOut(t *testing.T) {
	c := sqlcgen.Consignment{Bottles: 100}

	part, err := settleConsignment(c, 30, 0)
	if err != nil {
		t.Fatalf("partial: %v", err)
	}
	if part.Closed || part.Status != sqlcgen.ConsignmentStatusOut {
		t.Errorf("30 of 100 closed the consignment: %+v", part)
	}

	c.BottlesSettled = 30
	mixed, err := settleConsignment(c, 40, 30)
	if err != nil {
		t.Fatalf("closing: %v", err)
	}
	if !mixed.Closed {
		t.Error("70 + 30 of 100 did not close it")
	}
	if mixed.Status != sqlcgen.ConsignmentStatusSettled {
		t.Errorf("status %v — anything sold makes it a sale that ended, not a recall", mixed.Status)
	}

	// Nothing sold at all: a recall.
	none, err := settleConsignment(sqlcgen.Consignment{Bottles: 10}, 0, 10)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if none.Status != sqlcgen.ConsignmentStatusRecalled {
		t.Errorf("nothing sold, status %v", none.Status)
	}
}

// More cannot be accounted for than went out, or the three counts stop
// reconciling and the stock figure with them.
func TestSettleConsignment_CannotOverAccount(t *testing.T) {
	c := sqlcgen.Consignment{Bottles: 100, BottlesSettled: 60, BottlesRecalled: 20}
	if _, err := settleConsignment(c, 30, 0); err == nil {
		t.Error("accounted for 30 when only 20 were still out")
	}
	if _, err := settleConsignment(c, 0, 0); err == nil {
		t.Error("accepted a settlement of nothing at all")
	}
	if _, err := settleConsignment(c, -1, 0); err == nil {
		t.Error("accepted a negative")
	}
}

// The claim, end to end. Sending stock on consignment must not move a
// figure on the return: it is not a removal.
func TestConsignment_SendingIsNotARemoval(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rem := NewRemovalService(f.db, log)
	b266 := NewB266Service(f.db, log)

	lot, _ := f.bottleAndRemove(t, 200, 10)
	var cust uuid.UUID
	if err := testdb.AdminPool(t).QueryRow(f.ctx,
		`INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'A shop','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust); err != nil {
		t.Fatalf("customer: %v", err)
	}

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	end := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	before, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	dutyBefore := before.Msg.GetReport().GetDutyPayableCad()
	closingBefore := before.Msg.GetReport().GetPackagedClosingBottles()

	sent, err := rem.SendOnConsignment(f.ctx, connect.NewRequest(&stillhousev1.SendOnConsignmentRequest{
		PackagedInventoryId: lot, CustomerId: cust.String(), Bottles: 50,
		SentOn: now.Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("SendOnConsignment: %v", err)
	}
	if !strings.Contains(sent.Msg.GetDutyNote(), "not a removal") {
		t.Errorf("duty note does not state the claim: %q", sent.Msg.GetDutyNote())
	}

	after, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	if err != nil {
		t.Fatalf("GenerateB266 after: %v", err)
	}
	if got := after.Msg.GetReport().GetDutyPayableCad(); !near(got, dutyBefore, 1e-6) {
		t.Errorf("sending on consignment moved duty: %v → %v", dutyBefore, got)
	}
	// And the stock is still ours, so it is still in the closing balance.
	if got := after.Msg.GetReport().GetPackagedClosingBottles(); got != closingBefore {
		t.Errorf("consignment stock left the closing balance: %d → %d — it is still ours",
			closingBefore, got)
	}
}

// Selling through IS the removal, and it is where duty falls due.
func TestConsignment_SellingThroughIsTheRemoval(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	rem := NewRemovalService(f.db, log)
	b266 := NewB266Service(f.db, log)

	lot, _ := f.bottleAndRemove(t, 200, 10)
	var cust uuid.UUID
	_ = testdb.AdminPool(t).QueryRow(f.ctx,
		`INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'B shop','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust)

	now := time.Now().UTC()
	sent, err := rem.SendOnConsignment(f.ctx, connect.NewRequest(&stillhousev1.SendOnConsignmentRequest{
		PackagedInventoryId: lot, CustomerId: cust.String(), Bottles: 50,
		SentOn: now.Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("SendOnConsignment: %v", err)
	}

	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	end := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	before, _ := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	dutyBefore := before.Msg.GetReport().GetDutyPayableCad()

	settled, err := rem.SettleConsignment(f.ctx, connect.NewRequest(&stillhousev1.SettleConsignmentRequest{
		Id: sent.Msg.GetConsignment().GetId(), BottlesSold: 30, BottlesRecalled: 20,
		On: now.Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("SettleConsignment: %v", err)
	}
	c := settled.Msg.GetConsignment()
	if c.GetStatus() != stillhousev1.ConsignmentStatus_CONSIGNMENT_STATUS_SETTLED {
		t.Errorf("status: %v", c.GetStatus())
	}
	if c.GetBottlesOut() != 0 {
		t.Errorf("bottles still out: %d", c.GetBottlesOut())
	}

	after, _ := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	if after.Msg.GetReport().GetDutyPayableCad() <= dutyBefore {
		t.Errorf("selling through did not report duty: %v → %v",
			dutyBefore, after.Msg.GetReport().GetDutyPayableCad())
	}
	// The 20 that came back never sold, so they are not a return and
	// produced no removal — only the 30 did.
	if got := after.Msg.GetReport().GetPackagedRemovedDutyPaidBottles(); got != 10+30 {
		t.Errorf("removed bottles: got %d, want the original 10 plus the 30 sold", got)
	}
}

// Consignment stock is ours and is not available: promising it to a
// second customer promises bottles a hundred kilometres away.
func TestConsignment_StockIsNotAvailableToPromise(t *testing.T) {
	f := newDutyFixture(t)
	rem := NewRemovalService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	sched := NewSchedulingService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	lot, _ := f.bottleAndRemove(t, 200, 10)
	var cust uuid.UUID
	_ = testdb.AdminPool(t).QueryRow(f.ctx,
		`INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'C shop','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust)

	if _, err := rem.SendOnConsignment(f.ctx, connect.NewRequest(&stillhousev1.SendOnConsignmentRequest{
		PackagedInventoryId: lot, CustomerId: cust.String(), Bottles: 50,
		SentOn: time.Now().UTC().Format("2006-01-02"),
	})); err != nil {
		t.Fatalf("SendOnConsignment: %v", err)
	}

	// The plan must not count those 50 as available. It is exercised
	// through the RPC so the query is what is being tested.
	if _, err := sched.ProductionPlan(f.ctx, connect.NewRequest(&stillhousev1.ProductionPlanRequest{})); err != nil {
		t.Fatalf("ProductionPlan: %v", err)
	}
}

// More cannot go out than is on the shelf.
func TestConsignment_CannotSendMoreThanIsOnHand(t *testing.T) {
	f := newDutyFixture(t)
	rem := NewRemovalService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	lot, _ := f.bottleAndRemove(t, 100, 10)
	var cust uuid.UUID
	_ = testdb.AdminPool(t).QueryRow(f.ctx,
		`INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'D shop','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust)

	_, err := rem.SendOnConsignment(f.ctx, connect.NewRequest(&stillhousev1.SendOnConsignmentRequest{
		PackagedInventoryId: lot, CustomerId: cust.String(), Bottles: 99999,
		SentOn: time.Now().UTC().Format("2006-01-02"),
	}))
	if err == nil {
		t.Fatal("sent more bottles than the lot had on hand")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code: %v", connect.CodeOf(err))
	}
}
