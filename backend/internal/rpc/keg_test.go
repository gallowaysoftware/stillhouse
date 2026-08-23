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

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// PLAN D5. The keg register tracks the VESSEL. A keg's spirits are
// already recorded elsewhere — as a marked special container at 100 L and
// above (EDM3-8-1), as packaged spirits below it — and their volume,
// strength, LAA and duty reach the B266 from there. The register carries
// none of those figures.
//
// The test that matters most below is the one asserting the B266 does not
// move when a keg is filled and shipped: the obvious design — volume_l
// and abv_pct on the keg — puts the same alcohol on a filed return twice.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func newKeg(t *testing.T, f *dutyFixture, svc *KegService, serial string, deposit float64) string {
	t.Helper()
	resp, err := svc.CreateKeg(f.ctx, connect.NewRequest(&stillhousev1.CreateKegRequest{
		Serial: serial, CapacityL: 100, Material: "stainless",
		DepositCad: deposit, DepositSet: deposit > 0,
	}))
	if err != nil {
		t.Fatalf("CreateKeg: %v", err)
	}
	return resp.Msg.GetKeg().GetId()
}

// seedMarkedContainer makes a marked special container to put in a keg.
// It is seeded directly because the fill path is the web-UI back-office
// flow and this test is about the keg, not about marking.
func seedMarkedContainer(t *testing.T, f *dutyFixture) uuid.UUID {
	t.Helper()
	tank := f.tank(t, "Keg source "+uuid.NewString()[:6], 500, 40)
	var id uuid.UUID
	if err := testdb.AdminPool(t).QueryRow(f.ctx, `
		INSERT INTO marked_special_containers
		  (tenant_id, container_no, mark, capacity_l, source_container_id,
		   volume_l, abv_pct, laa, filled_on, created_by)
		VALUES ($1, floor(random()*100000)::int, 'NS', 200, $2, 200, 40, 80, CURRENT_DATE, $3)
		RETURNING id`, f.tenant.ID, tank.ID, f.user.ID).Scan(&id); err != nil {
		t.Fatalf("marked container: %v", err)
	}
	return id
}

func kegByID(t *testing.T, f *dutyFixture, svc *KegService, id string) *stillhousev1.Keg {
	t.Helper()
	list, err := svc.ListKegs(f.ctx, connect.NewRequest(&stillhousev1.ListKegsRequest{}))
	if err != nil {
		t.Fatalf("ListKegs: %v", err)
	}
	for _, k := range list.Msg.GetKegs() {
		if k.GetId() == id {
			return k
		}
	}
	t.Fatalf("keg %s not in the register", id)
	return nil
}

func move(t *testing.T, f *dutyFixture, svc *KegService, id string,
	kind stillhousev1.KegEventKind, customer, contents string) (*connect.Response[stillhousev1.MoveKegResponse], error) {
	t.Helper()
	return svc.MoveKeg(f.ctx, connect.NewRequest(&stillhousev1.MoveKegRequest{
		KegId: id, Kind: kind, OccurredOn: time.Now().UTC().Format("2006-01-02"),
		CustomerId: customer, MarkedContainerId: contents,
	}))
}

// The whole cycle, and the deposit netting to nothing at the end of it.
func TestKeg_CycleAndDeposit(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewKegService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	id := newKeg(t, f, svc, "KEG-"+uuid.NewString()[:8], 50)
	msc := seedMarkedContainer(t, f)

	var cust uuid.UUID
	if err := testdb.AdminPool(t).QueryRow(f.ctx,
		`INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'A bar','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust); err != nil {
		t.Fatalf("customer: %v", err)
	}

	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "", msc.String()); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if got := kegByID(t, f, svc, id); got.GetStatus() != stillhousev1.KegStatus_KEG_STATUS_FILLED {
		t.Errorf("after fill: %v", got.GetStatus())
	}

	ship, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_SHIPPED, cust.String(), "")
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if !ship.Msg.GetDepositDeltaSet() || !near(ship.Msg.GetDepositDeltaCad(), 50, 1e-6) {
		t.Errorf("shipping took a deposit of %v, want +50", ship.Msg.GetDepositDeltaCad())
	}

	list, err := svc.ListKegs(f.ctx, connect.NewRequest(&stillhousev1.ListKegsRequest{}))
	if err != nil {
		t.Fatalf("ListKegs: %v", err)
	}
	if !near(list.Msg.GetTotalOutstandingDepositsCad(), 50, 1e-6) {
		t.Errorf("outstanding deposits with a keg out: %v, want 50",
			list.Msg.GetTotalOutstandingDepositsCad())
	}

	ret, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_RETURNED, "", "")
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if !near(ret.Msg.GetDepositDeltaCad(), -50, 1e-6) {
		t.Errorf("returning refunded %v, want -50", ret.Msg.GetDepositDeltaCad())
	}
	// A keg back from a customer is dirty, not available. The distinction
	// is the reason the status exists.
	if got := kegByID(t, f, svc, id); got.GetStatus() != stillhousev1.KegStatus_KEG_STATUS_RETURNED_DIRTY {
		t.Errorf("after return: %v, want returned_dirty", got.GetStatus())
	}

	list, _ = svc.ListKegs(f.ctx, connect.NewRequest(&stillhousev1.ListKegsRequest{}))
	if !near(list.Msg.GetTotalOutstandingDepositsCad(), 0, 1e-6) {
		t.Errorf("deposit did not net out after the keg came back: %v",
			list.Msg.GetTotalOutstandingDepositsCad())
	}

	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_CLEANED, "", ""); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if got := kegByID(t, f, svc, id); got.GetStatus() != stillhousev1.KegStatus_KEG_STATUS_AVAILABLE {
		t.Errorf("after cleaning: %v", got.GetStatus())
	}
}

// The one that would be expensive to get wrong. Filling and shipping a
// keg must not move a single figure on the B266 — the spirits are
// counted on the marked special container and nowhere else.
func TestKeg_DoesNotDoubleCountAlcoholOnTheReturn(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewKegService(f.db, log)
	b266 := NewB266Service(f.db, log)

	msc := seedMarkedContainer(t, f)
	id := newKeg(t, f, svc, "KEG-"+uuid.NewString()[:8], 50)

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	end := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	before, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	b := before.Msg.GetReport()

	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "", msc.String()); err != nil {
		t.Fatalf("fill: %v", err)
	}

	after, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: start, PeriodEnd: end,
	}))
	if err != nil {
		t.Fatalf("GenerateB266 after: %v", err)
	}
	a := after.Msg.GetReport()

	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"packaged marked containers LAA", a.GetPackagedMarkedContainersLaa(), b.GetPackagedMarkedContainersLaa()},
		{"bulk closing", a.GetBulkClosingLaa(), b.GetBulkClosingLaa()},
		{"packaged closing", a.GetPackagedClosingLaa(), b.GetPackagedClosingLaa()},
		{"duty payable", a.GetDutyPayableCad(), b.GetDutyPayableCad()},
	} {
		if !near(c.got, c.want, 1e-9) {
			t.Errorf("%s moved when a keg was filled: %v → %v. The keg register tracks the "+
				"vessel; the spirits are counted on the marked special container.", c.name, c.want, c.got)
		}
	}
}

// The illegal transitions are the point of the table. Filling a keg that
// is already full loses the first fill's spirits; shipping one already at
// a customer means two customers hold the same physical asset.
func TestKeg_IllegalTransitionsAreRefused(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewKegService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	id := newKeg(t, f, svc, "KEG-"+uuid.NewString()[:8], 25)
	msc := seedMarkedContainer(t, f)

	var cust uuid.UUID
	_ = testdb.AdminPool(t).QueryRow(f.ctx,
		`INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'B bar','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust)

	// Cannot ship an empty keg.
	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_SHIPPED, cust.String(), ""); err == nil {
		t.Error("shipped an empty keg")
	}

	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "", msc.String()); err != nil {
		t.Fatalf("fill: %v", err)
	}
	// Cannot fill a full one.
	_, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "", seedMarkedContainer(t, f).String())
	if err == nil {
		t.Error("filled a keg that was already full — the first fill's spirits would be lost")
	} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code: %v", connect.CodeOf(err))
	}

	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_SHIPPED, cust.String(), ""); err != nil {
		t.Fatalf("ship: %v", err)
	}
	// Cannot ship twice.
	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_SHIPPED, cust.String(), ""); err == nil {
		t.Error("shipped a keg that was already at a customer")
	}
	// A dirty keg cannot be filled.
	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_RETURNED, "", ""); err != nil {
		t.Fatalf("return: %v", err)
	}
	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "", msc.String()); err == nil {
		t.Error("filled a keg nobody had cleaned")
	}
}

// Shipping needs the customer, because the deposit is owed by somebody.
func TestKeg_ShippingNeedsACustomer(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewKegService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	id := newKeg(t, f, svc, "KEG-"+uuid.NewString()[:8], 25)
	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "",
		seedMarkedContainer(t, f).String()); err != nil {
		t.Fatalf("fill: %v", err)
	}
	_, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_SHIPPED, "", "")
	if err == nil {
		t.Fatal("shipped with no customer")
	}
	if !strings.Contains(err.Error(), "deposit is owed by somebody") {
		t.Errorf("refusal does not say why: %v", err)
	}
}

// A lost keg keeps the deposit outstanding — that is what a deposit is
// for — and does not refund it.
func TestKeg_LostKegKeepsTheDepositOutstanding(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewKegService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	id := newKeg(t, f, svc, "KEG-"+uuid.NewString()[:8], 40)
	var cust uuid.UUID
	_ = testdb.AdminPool(t).QueryRow(f.ctx,
		`INSERT INTO customers (tenant_id, name, kind) VALUES ($1,'C bar','private_retail') RETURNING id`,
		f.tenant.ID).Scan(&cust)

	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "",
		seedMarkedContainer(t, f).String()); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_SHIPPED, cust.String(), ""); err != nil {
		t.Fatalf("ship: %v", err)
	}
	if _, err := move(t, f, svc, id, stillhousev1.KegEventKind_KEG_EVENT_KIND_LOST, "", ""); err != nil {
		t.Fatalf("lost: %v", err)
	}

	list, _ := svc.ListKegs(f.ctx, connect.NewRequest(&stillhousev1.ListKegsRequest{}))
	if !near(list.Msg.GetTotalOutstandingDepositsCad(), 40, 1e-6) {
		t.Errorf("a lost keg refunded its deposit: outstanding %v, want 40",
			list.Msg.GetTotalOutstandingDepositsCad())
	}
	if list.Msg.GetLost() != 1 {
		t.Errorf("lost count: %d", list.Msg.GetLost())
	}
}

var _ = context.Background
var _ = sqlcgen.KegStatusAvailable

// The Act's threshold, enforced rather than trusted to the caller. A
// marked special container is 100 to 1500 litres; spirits in anything
// smaller are packaged exactly as a bottle is.
//
// This is not a detail the register may get wrong. If a 50 L keg could
// claim a marked special container, the same alcohol would be counted
// once as packaged and once as a special container, on a filed return.
func TestKeg_ContentsFollowTheActsThreshold(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewKegService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	small, err := svc.CreateKeg(f.ctx, connect.NewRequest(&stillhousev1.CreateKegRequest{
		Serial: "SMALL-" + uuid.NewString()[:8], CapacityL: 50,
	}))
	if err != nil {
		t.Fatalf("CreateKeg: %v", err)
	}
	// A 50 L keg offered a marked special container must be refused, and
	// told why.
	_, err = move(t, f, svc, small.Msg.GetKeg().GetId(),
		stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED, "", seedMarkedContainer(t, f).String())
	if err == nil {
		t.Fatal("a 50 L keg accepted a marked special container — the same alcohol would be " +
			"counted as both packaged and a special container")
	}
	if !strings.Contains(err.Error(), "100 L") {
		t.Errorf("refusal does not name the threshold: %v", err)
	}

	// And the reverse: a 100 L keg offered a packaged lot.
	big, err := svc.CreateKeg(f.ctx, connect.NewRequest(&stillhousev1.CreateKegRequest{
		Serial: "BIG-" + uuid.NewString()[:8], CapacityL: 200,
	}))
	if err != nil {
		t.Fatalf("CreateKeg: %v", err)
	}
	resp, err := svc.MoveKeg(f.ctx, connect.NewRequest(&stillhousev1.MoveKegRequest{
		KegId:               big.Msg.GetKeg().GetId(),
		Kind:                stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED,
		OccurredOn:          time.Now().UTC().Format("2006-01-02"),
		PackagedInventoryId: uuid.NewString(),
	}))
	if err == nil {
		t.Fatalf("a 200 L keg accepted packaged spirits: %+v", resp.Msg)
	}
	if !strings.Contains(err.Error(), "EDM3-8-1") {
		t.Errorf("refusal does not cite the passage: %v", err)
	}
}
