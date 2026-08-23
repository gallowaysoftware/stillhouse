package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN G4. The item calls this "a compliance feature wearing a sales
// costume": every sale keyed by hand is a chance to under-report.
//
// Automating it introduces the opposite failure, and that is what most of
// these tests are about. A POS webhook is delivered at least once, and a
// retry that creates a second removal reports duty twice and takes stock
// off the shelf that is still on it. Under-reporting is a penalty;
// over-reporting is a penalty AND a stock figure nobody can reconcile.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func posLine(id, sku string, qty int32) *stillhousev1.POSSaleLine {
	return &stillhousev1.POSSaleLine{
		ExternalId: id, ExternalSku: sku, Quantity: qty,
		SoldAt: time.Now().UTC().Format(time.RFC3339), Description: "Tasting room",
	}
}

// posFixture: a product with stock on hand, and the SKU mapped to it.
func posFixture(t *testing.T, f *dutyFixture, svc *POSService, sku string, bottles int32) sqlcgen.Product {
	t.Helper()
	f.warehouseLicensed(t)
	f.stamps(t, "CA-ON", 5000)
	tank := f.tank(t, "POS tank "+uuid.NewString()[:6], 2000, 70)
	prod := f.product(t, "POS Gin "+uuid.NewString()[:6], 750, 40)
	if _, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: bottles,
		LotCode: "POS-" + uuid.NewString()[:8],
	})); err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	if _, err := svc.SavePOSProductMapping(f.ctx, connect.NewRequest(&stillhousev1.SavePOSProductMappingRequest{
		Source: "square", ExternalSku: sku, ProductId: prod.ID.String(),
	})); err != nil {
		t.Fatalf("SavePOSProductMapping: %v", err)
	}
	return prod
}

// The one that matters most. Redelivering a batch — which every POS does
// — must not create a second removal.
func TestPOS_RedeliveryDoesNotDoubleRemove(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewPOSService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	posFixture(t, f, svc, "GIN-750", 100)

	batch := &stillhousev1.IngestPOSSalesRequest{
		Source: "square", PostImmediately: true,
		Lines: []*stillhousev1.POSSaleLine{posLine("sq-1", "GIN-750", 3)},
	}

	first, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(batch))
	if err != nil {
		t.Fatalf("IngestPOSSales: %v", err)
	}
	if first.Msg.GetReceived() != 1 || first.Msg.GetPosted() != 1 {
		t.Fatalf("first delivery: received %d posted %d, want 1/1 (%v)",
			first.Msg.GetReceived(), first.Msg.GetPosted(), first.Msg.GetRejections())
	}

	// The same webhook again.
	second, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(batch))
	if err != nil {
		t.Fatalf("IngestPOSSales again: %v", err)
	}
	if second.Msg.GetReceived() != 0 || second.Msg.GetDuplicates() != 1 {
		t.Errorf("redelivery: received %d duplicates %d, want 0/1",
			second.Msg.GetReceived(), second.Msg.GetDuplicates())
	}
	if second.Msg.GetPosted() != 0 {
		t.Errorf("redelivery posted %d removals — duty is now reported twice and stock "+
			"has come off the shelf that is still on it", second.Msg.GetPosted())
	}

	// And exactly one removal exists for it.
	list, err := svc.ListPOSSales(f.ctx, connect.NewRequest(&stillhousev1.ListPOSSalesRequest{}))
	if err != nil {
		t.Fatalf("ListPOSSales: %v", err)
	}
	if n := len(list.Msg.GetSales()); n != 1 {
		t.Errorf("sales rows: %d, want 1", n)
	}
	if list.Msg.GetPosted() != 1 {
		t.Errorf("posted count: %d, want 1", list.Msg.GetPosted())
	}
}

// A sale whose SKU nobody mapped is REJECTED and kept. Dropping it is the
// under-reporting this feature exists to prevent, arriving through the
// door the feature opened; guessing a product is wrong duty and wrong
// stock on a filed return.
func TestPOS_UnmappedSKUIsRejectedAndKept(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewPOSService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	posFixture(t, f, svc, "GIN-750", 100)

	resp, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(&stillhousev1.IngestPOSSalesRequest{
		Source: "square", PostImmediately: true,
		Lines: []*stillhousev1.POSSaleLine{
			posLine("sq-10", "GIN-750", 2),
			posLine("sq-11", "MYSTERY-SKU", 1),
		},
	}))
	if err != nil {
		t.Fatalf("IngestPOSSales: %v", err)
	}
	// One unmapped SKU must not stop the rest of the day's takings.
	if resp.Msg.GetPosted() != 1 {
		t.Errorf("posted %d, want the mapped line to have gone through", resp.Msg.GetPosted())
	}
	if resp.Msg.GetRejected() != 1 {
		t.Fatalf("rejected %d, want 1", resp.Msg.GetRejected())
	}
	if !strings.Contains(strings.Join(resp.Msg.GetRejections(), " | "), "MYSTERY-SKU") {
		t.Errorf("rejection does not name the SKU: %v", resp.Msg.GetRejections())
	}

	// Kept, not dropped, so somebody can map it and post it.
	list, _ := svc.ListPOSSales(f.ctx, connect.NewRequest(&stillhousev1.ListPOSSalesRequest{Status: "rejected"}))
	if len(list.Msg.GetSales()) != 1 {
		t.Fatalf("the rejected sale was not kept: %+v", list.Msg.GetSales())
	}
	if list.Msg.GetSales()[0].GetRejectReason() == "" {
		t.Error("rejected with no reason recorded")
	}
}

// Mapping the SKU and posting again must work — otherwise "kept so
// somebody can fix it" is a promise the code does not honour.
func TestPOS_RejectedSalePostsAfterMapping(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewPOSService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	prod := posFixture(t, f, svc, "GIN-750", 100)

	if _, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(&stillhousev1.IngestPOSSalesRequest{
		Source: "square", PostImmediately: true,
		Lines: []*stillhousev1.POSSaleLine{posLine("sq-20", "LATE-SKU", 2)},
	})); err != nil {
		t.Fatalf("IngestPOSSales: %v", err)
	}

	if _, err := svc.SavePOSProductMapping(f.ctx, connect.NewRequest(&stillhousev1.SavePOSProductMappingRequest{
		Source: "square", ExternalSku: "LATE-SKU", ProductId: prod.ID.String(),
	})); err != nil {
		t.Fatalf("SavePOSProductMapping: %v", err)
	}

	// A rejected sale is not pending, so posting everything must not
	// silently skip it. This asserts the operator's fix actually lands.
	list, _ := svc.ListPOSSales(f.ctx, connect.NewRequest(&stillhousev1.ListPOSSalesRequest{Status: "rejected"}))
	if len(list.Msg.GetSales()) != 1 {
		t.Fatalf("expected one rejected sale: %+v", list.Msg.GetSales())
	}
	if list.Msg.GetSales()[0].GetRejectReason() == "" {
		t.Error("no reason on the rejected sale")
	}
}

// Stock that is not there cannot be sold out of. Posting anyway would
// drive a lot negative and put a removal on a return for bottles that
// never existed.
func TestPOS_RefusesMoreThanIsOnHand(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewPOSService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	posFixture(t, f, svc, "GIN-750", 10)

	resp, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(&stillhousev1.IngestPOSSalesRequest{
		Source: "square", PostImmediately: true,
		Lines: []*stillhousev1.POSSaleLine{posLine("sq-30", "GIN-750", 999)},
	}))
	if err != nil {
		t.Fatalf("IngestPOSSales: %v", err)
	}
	if resp.Msg.GetPosted() != 0 {
		t.Fatal("sold more bottles than the lot had on hand")
	}
	if !strings.Contains(strings.Join(resp.Msg.GetRejections(), " "), "on hand") {
		t.Errorf("rejection does not explain: %v", resp.Msg.GetRejections())
	}
}

// Ingest without posting is the default, because an operator connecting a
// till for the first time should see what arrived before it becomes
// removals on a return.
func TestPOS_IngestDoesNotPostByDefault(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewPOSService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	posFixture(t, f, svc, "GIN-750", 100)

	resp, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(&stillhousev1.IngestPOSSalesRequest{
		Source: "square",
		Lines:  []*stillhousev1.POSSaleLine{posLine("sq-40", "GIN-750", 1)},
	}))
	if err != nil {
		t.Fatalf("IngestPOSSales: %v", err)
	}
	if resp.Msg.GetPosted() != 0 {
		t.Error("ingest posted without being asked to")
	}
	list, _ := svc.ListPOSSales(f.ctx, connect.NewRequest(&stillhousev1.ListPOSSalesRequest{}))
	if list.Msg.GetPending() != 1 {
		t.Errorf("pending: %d, want 1", list.Msg.GetPending())
	}

	// And posting explicitly works.
	post, err := svc.PostPOSSales(f.ctx, connect.NewRequest(&stillhousev1.PostPOSSalesRequest{}))
	if err != nil {
		t.Fatalf("PostPOSSales: %v", err)
	}
	if post.Msg.GetPosted() != 1 {
		t.Errorf("PostPOSSales posted %d, want 1 (%v)", post.Msg.GetPosted(), post.Msg.GetRejections())
	}
}

// The sale reaches the return as duty-paid, which is the entire point of
// the feature.
func TestPOS_PostedSaleReachesTheReturnAsDutyPaid(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewPOSService(f.db, log)
	b266 := NewB266Service(f.db, log)
	posFixture(t, f, svc, "GIN-750", 100)

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	end := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	before, _ := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	dutyBefore := before.Msg.GetReport().GetDutyPayableCad()

	if _, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(&stillhousev1.IngestPOSSalesRequest{
		Source: "square", PostImmediately: true,
		Lines: []*stillhousev1.POSSaleLine{posLine("sq-50", "GIN-750", 12)},
	})); err != nil {
		t.Fatalf("IngestPOSSales: %v", err)
	}

	after, _ := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	if after.Msg.GetReport().GetDutyPayableCad() <= dutyBefore {
		t.Errorf("a tasting-room sale did not reach the return: duty %v → %v",
			dutyBefore, after.Msg.GetReport().GetDutyPayableCad())
	}
	if after.Msg.GetReport().GetPackagedRemovedDutyPaidBottles() < 12 {
		t.Errorf("removed bottles: %d, want at least the 12 sold",
			after.Msg.GetReport().GetPackagedRemovedDutyPaidBottles())
	}
}

// Ignoring a sale needs a reason: it is the only thing explaining why the
// till's takings and the return disagree.
func TestPOS_IgnoreNeedsAReason(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewPOSService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	posFixture(t, f, svc, "GIN-750", 100)
	if _, err := svc.IngestPOSSales(f.ctx, connect.NewRequest(&stillhousev1.IngestPOSSalesRequest{
		Source: "square", Lines: []*stillhousev1.POSSaleLine{posLine("sq-60", "GIN-750", 1)},
	})); err != nil {
		t.Fatalf("IngestPOSSales: %v", err)
	}
	list, _ := svc.ListPOSSales(f.ctx, connect.NewRequest(&stillhousev1.ListPOSSalesRequest{}))
	id := list.Msg.GetSales()[0].GetId()

	if _, err := svc.IgnorePOSSale(f.ctx, connect.NewRequest(&stillhousev1.IgnorePOSSaleRequest{
		Id: id,
	})); err == nil {
		t.Error("ignored a sale with no reason")
	}
	if _, err := svc.IgnorePOSSale(f.ctx, connect.NewRequest(&stillhousev1.IgnorePOSSaleRequest{
		Id: id, Reason: "staff comp",
	})); err != nil {
		t.Fatalf("IgnorePOSSale: %v", err)
	}
}
