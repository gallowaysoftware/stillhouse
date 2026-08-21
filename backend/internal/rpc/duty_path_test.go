package rpc

import (
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The two handlers that decide what lands on a filed return —
// CreateBottlingRun computes the LAA that becomes packaged inventory, and
// CreateRemoval computes the duty — were invoked by no test at all. The
// existing DB-backed tests seed through raw sqlc and assert on query
// results: they cover the SQL, not the handlers, so every guard and every
// arithmetic step between the request and the row was unexercised.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN, same as the
// ledger, B266 and full-path tests.

type dutyFixture struct {
	*ledgerFixture
	bottling *BottlingService
	removal  *RemovalService
}

func newDutyFixture(t *testing.T) *dutyFixture {
	t.Helper()
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return &dutyFixture{
		ledgerFixture: f,
		bottling:      NewBottlingService(f.db, log),
		removal:       NewRemovalService(f.db, log),
	}
}

func (f *dutyFixture) product(t *testing.T, name string, sizeML int32, abv float64) sqlcgen.Product {
	t.Helper()
	p, err := f.q.CreateProduct(f.ctx, sqlcgen.CreateProductParams{
		TenantID: f.tenant.ID, Name: name,
		SpiritKind: sqlcgen.SpiritKindVodka, BottleSizeMl: sizeML, TargetAbvPct: abv,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	return p
}

// stamps puts `count` received stamps on hand for a jurisdiction. Bottling
// refuses without them, so every bottling test needs this.
func (f *dutyFixture) stamps(t *testing.T, jurisdiction string, count int32) {
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

func (f *dutyFixture) lot(t *testing.T, id uuid.UUID) sqlcgen.PackagedInventory {
	t.Helper()
	pi, err := f.q.GetPackagedInventoryForUpdate(f.ctx, id)
	if err != nil {
		t.Fatalf("read packaged inventory: %v", err)
	}
	return pi
}

// A bottling run must conserve alcohol across the source→packaged
// transition. What the handler draws from the tank is what becomes
// bottles plus what is lost at the filler — and because a product bottled
// below its source strength is diluted on the way, the LITRES drawn are
// fewer than the litres bottled while the LAA matches exactly. Getting
// that backwards is stage 109's P0.
func TestCreateBottlingRunConservesLAA(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 1000)

	// 1000 L at 70% = 700 LAA in the tank; bottling 40% product.
	tank := f.tank(t, "Bottling tank", 1000, 70)
	prod := f.product(t, "Handler Vodka", 750, 40)
	beforeVol, beforeLAA := f.balance(t, tank.ID)

	const bottles = 600
	const lossL = 1.5

	resp, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId:               prod.ID.String(),
		SourceContainerId:       tank.ID.String(),
		DestinationJurisdiction: "CA-ON",
		BottleCount:             bottles,
		BottlingLossL:           lossL,
		LotCode:                 "LOT-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	run := resp.Msg.GetRun()

	bottleVolumeL := float64(bottles) * 750 / 1000 // 450 L of finished spirit
	bottleLAA := bottleVolumeL * 40 / 100          // 180 LAA
	lossLAA := lossL * 40 / 100                    // 0.6 LAA
	wantDrawnLAA := bottleLAA + lossLAA            // 180.6 LAA
	wantDrawnVolume := wantDrawnLAA / 70 * 100     // 258 L out of a 70% tank

	if got := run.GetTankGaugeLaa(); !near(got, wantDrawnLAA, 1e-6) {
		t.Errorf("tank gauge LAA: got %v, want %v", got, wantDrawnLAA)
	}
	if got := run.GetTankGaugeVolumeL(); !near(got, wantDrawnVolume, 1e-6) {
		t.Errorf("tank gauge volume: got %v L, want %v L", got, wantDrawnVolume)
	}
	// The dilution direction, stated: fewer litres leave the tank than go
	// into bottles, because water is added on the way.
	if run.GetTankGaugeVolumeL() >= bottleVolumeL {
		t.Errorf("drew %v L to fill %v L of 40%% bottles from a 70%% tank — "+
			"bottling below source strength must draw less liquid, not more",
			run.GetTankGaugeVolumeL(), bottleVolumeL)
	}

	afterVol, afterLAA := f.balance(t, tank.ID)
	if got := beforeLAA - afterLAA; !near(got, wantDrawnLAA, 1e-6) {
		t.Errorf("tank lost %v LAA but the run drew %v — %v LAA unaccounted",
			got, wantDrawnLAA, got-wantDrawnLAA)
	}
	if got := beforeVol - afterVol; !near(got, wantDrawnVolume, 1e-6) {
		t.Errorf("tank lost %v L but the run drew %v L", got, wantDrawnVolume)
	}

	// Packaged inventory received the bottles, not the tank gauge.
	pkg := resp.Msg.GetPackaged()
	if got := pkg.GetBottlesOnHand(); got != bottles {
		t.Errorf("bottles on hand: got %d, want %d", got, bottles)
	}
}

// You cannot bottle stronger than the source — the system can dilute but
// has no way to add ethanol. Silently rewriting the tank's strength to
// absorb the difference would hide an operator error on a duty-relevant
// figure.
func TestCreateBottlingRunRefusesStrongerThanSource(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 100)
	tank := f.tank(t, "Weak tank", 1000, 35)
	prod := f.product(t, "Too Strong", 750, 40)

	_, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 10, LotCode: "STRONG",
	}))
	if err == nil {
		t.Fatal("bottling 40% product from a 35% tank was accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
}

// Stamps are Crown-controlled: a run that would apply more than are on
// hand must be refused before any alcohol moves, not reconciled after.
func TestCreateBottlingRunRefusesWithoutStamps(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 50)
	tank := f.tank(t, "Stamp tank", 1000, 70)
	prod := f.product(t, "Understamped", 750, 40)
	beforeVol, beforeLAA := f.balance(t, tank.ID)

	_, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 100, LotCode: "NOSTAMP",
	}))
	if err == nil {
		t.Fatal("bottling 100 bottles against 50 stamps was accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
	// And nothing moved.
	afterVol, afterLAA := f.balance(t, tank.ID)
	if !near(beforeVol, afterVol, 1e-9) || !near(beforeLAA, afterLAA, 1e-9) {
		t.Errorf("a refused run still drained the tank: %v L/%v LAA → %v L/%v LAA",
			beforeVol, beforeLAA, afterVol, afterLAA)
	}
}

// The duty figure on the return, computed by the handler rather than
// asserted against a hand-inserted row. >7% ABV is charged per litre of
// absolute alcohol.
func TestCreateRemovalComputesDutyOver7(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 1000)
	tank := f.tank(t, "Duty tank", 1000, 70)
	prod := f.product(t, "Duty Vodka", 750, 40)

	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 600,
		LotCode: "DUTY-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	piID := bottled.Msg.GetPackaged().GetId()

	const removed = 240
	resp, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: piID,
		BottlesRemoved:      removed,
		DestinationKind:     stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
		DestinationName:     "LCBO",
	}))
	if err != nil {
		t.Fatalf("CreateRemoval: %v", err)
	}
	r := resp.Msg.GetRemoval()

	wantLitres := float64(removed) * 750 / 1000 // 180 L
	wantLAA := wantLitres * 40 / 100            // 72 LAA
	wantDuty := wantLAA * excise.DutyRatePerLAAOver7Pct

	if got := r.GetTotalLitres(); !near(got, wantLitres, 1e-9) {
		t.Errorf("total litres: got %v, want %v", got, wantLitres)
	}
	if got := r.GetTotalLaa(); !near(got, wantLAA, 1e-9) {
		t.Errorf("total LAA: got %v, want %v", got, wantLAA)
	}
	if got := r.GetDutyRatePerLaa(); got != excise.DutyRatePerLAAOver7Pct {
		t.Errorf("rate per LAA: got %v, want %v", got, excise.DutyRatePerLAAOver7Pct)
	}
	if got := r.GetDutyAmountCad(); !near(got, wantDuty, 1e-6) {
		t.Errorf("duty: got %v, want %v", got, wantDuty)
	}
	// The line has to multiply out — an auditor checks this first.
	if got := r.GetTotalLaa() * r.GetDutyRatePerLaa(); !near(got, r.GetDutyAmountCad(), 1e-6) {
		t.Errorf("%v LAA × %v = %v, but the row says %v",
			r.GetTotalLaa(), r.GetDutyRatePerLaa(), got, r.GetDutyAmountCad())
	}

	// Stock moved by exactly what left.
	pkg := f.lot(t, uuid.MustParse(piID))
	if got, want := pkg.BottlesOnHand, int32(600-removed); got != want {
		t.Errorf("bottles on hand: got %d, want %d", got, want)
	}
	if got, want := pkg.BottlesRemoved, int32(removed); got != want {
		t.Errorf("bottles removed counter: got %d, want %d", got, want)
	}
}

// At or below 7% ABV the charge is per litre of PRODUCT, not per LAA, and
// the per-LAA rate on the row is left at zero so nothing multiplies the
// wrong pair of numbers together. Reporting one blended rate against a
// total LAA is the defect stage 134 fixed on the return; this pins it at
// the row that feeds it.
func TestCreateRemovalComputesDutyUnder7(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 1000)
	tank := f.tank(t, "Cooler tank", 1000, 40)
	prod := f.product(t, "Ready to Drink", 355, 5)

	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 500,
		LotCode: "RTD-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}

	const removed = 200
	resp, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: bottled.Msg.GetPackaged().GetId(),
		BottlesRemoved:      removed,
		DestinationKind:     stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
		DestinationName:     "LCBO",
	}))
	if err != nil {
		t.Fatalf("CreateRemoval: %v", err)
	}
	r := resp.Msg.GetRemoval()

	wantLitres := float64(removed) * 355 / 1000 // 71 L of product
	wantDuty := wantLitres * excise.DutyRatePerLAtOrUnder7

	if got := r.GetTotalLitres(); !near(got, wantLitres, 1e-9) {
		t.Errorf("total litres: got %v, want %v", got, wantLitres)
	}
	if got := r.GetDutyAmountCad(); !near(got, wantDuty, 1e-6) {
		t.Errorf("duty: got %v, want %v (per litre of product, not per LAA)", got, wantDuty)
	}
	if got := r.GetDutyRatePerLaa(); got != 0 {
		t.Errorf("rate per LAA on a ≤7%% row: got %v, want 0 — this band is not charged per LAA", got)
	}
	// The mistake this guards: charging the ≤7% band at the >7% rate
	// against its LAA overstates the duty by more than 4×.
	if near(r.GetDutyAmountCad(), wantLitres*5/100*excise.DutyRatePerLAAOver7Pct, 1e-6) {
		t.Error("≤7% removal was charged at the per-LAA rate")
	}
}

// Removing more bottles than are on hand must be refused, not clamped and
// not allowed to go negative.
func TestCreateRemovalRefusesOverdraw(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 1000)
	tank := f.tank(t, "Overdraw tank", 1000, 70)
	prod := f.product(t, "Overdraw Gin", 750, 40)

	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 100,
		LotCode: "OVER-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	piID := bottled.Msg.GetPackaged().GetId()

	_, err = f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: piID, BottlesRemoved: 101,
		DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
	}))
	if err == nil {
		t.Fatal("removing 101 of 100 bottles was accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
	if got := f.lot(t, uuid.MustParse(piID)).BottlesOnHand; got != 100 {
		t.Errorf("a refused removal moved stock: %d bottles on hand, want 100", got)
	}
}

// The lost update, pinned. Before stage 140 the handler read the on-hand
// count with no row lock, checked it in Go, and then decremented: eight
// concurrent removals of 20 against a 100-bottle lot all read 100, all
// decided there were enough, and all decremented. The table CHECK caught
// the negative — so what an operator saw was not a wrong number but an
// opaque error, which is only marginally better.
//
// With the lock, exactly five of eight succeed and the three that lose the
// race are told so. Same shape as the barrel fills fixed in stage 131.
func TestConcurrentRemovalsDoNotOverdraw(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 1000)
	tank := f.tank(t, "Race removal tank", 1000, 70)
	prod := f.product(t, "Race Whisky", 750, 40)

	const onHand, attempts, each = 100, 8, 20

	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: onHand,
		LotCode: "RACE-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	piID := bottled.Msg.GetPackaged().GetId()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		okN  int
		errs []error
	)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
				PackagedInventoryId: piID, BottlesRemoved: each,
				DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
				DestinationName: "LCBO",
			}))
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				okN++
			} else {
				errs = append(errs, e)
			}
		}()
	}
	wg.Wait()

	pkg := f.lot(t, uuid.MustParse(piID))
	if pkg.BottlesOnHand < 0 {
		t.Fatalf("bottles on hand went negative: %d", pkg.BottlesOnHand)
	}
	if got, want := int(pkg.BottlesOnHand), onHand-okN*each; got != want {
		t.Errorf("%d removals succeeded but %d bottles remain, want %d — "+
			"a withdrawal was lost", okN, got, want)
	}
	if got, want := okN, onHand/each; got != want {
		t.Errorf("%d of %d removals succeeded, want exactly %d", got, attempts, want)
	}
	// The losers must be told what happened, not handed a 500.
	for _, e := range errs {
		if got := connect.CodeOf(e); got != connect.CodeFailedPrecondition {
			t.Errorf("a losing removal returned %v, want failed_precondition: %v", got, e)
		}
	}
	// And the counters still agree with each other.
	if got, want := pkg.BottlesPackaged-pkg.BottlesRemoved, pkg.BottlesOnHand; got != want {
		t.Errorf("packaged %d − removed %d = %d, but on hand is %d",
			pkg.BottlesPackaged, pkg.BottlesRemoved, got, want)
	}
}

// Voiding a removal has to put the bottles back — both the on-hand count
// and the running removed counter — or the return double-counts the stock
// on the next period.
func TestVoidRemovalRestoresStock(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 1000)
	tank := f.tank(t, "Void tank", 1000, 70)
	prod := f.product(t, "Void Rye", 750, 40)

	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 100,
		LotCode: "VOID-" + uuid.NewString()[:8],
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	piID := bottled.Msg.GetPackaged().GetId()

	created, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: piID, BottlesRemoved: 40,
		DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
	}))
	if err != nil {
		t.Fatalf("CreateRemoval: %v", err)
	}
	if _, err := f.removal.VoidRemoval(f.ctx, connect.NewRequest(&stillhousev1.VoidRemovalRequest{
		Id: created.Msg.GetRemoval().GetId(), Reason: "shipment cancelled",
	})); err != nil {
		t.Fatalf("VoidRemoval: %v", err)
	}

	pkg := f.lot(t, uuid.MustParse(piID))
	if got := pkg.BottlesOnHand; got != 100 {
		t.Errorf("bottles on hand after void: got %d, want 100", got)
	}
	if got := pkg.BottlesRemoved; got != 0 {
		t.Errorf("removed counter after void: got %d, want 0", got)
	}
}

// Removal numbers are allocated with `SELECT MAX(removal_no) + 1` and the
// column is UNIQUE per tenant, so two removals starting at the same
// moment both read the same maximum, both claim the same number, and one
// dies on the unique constraint behind a 500 with the operator's shipment
// unrecorded.
//
// The row lock added above hides this for two removals against the SAME
// lot, because it serialises them. Two operators shipping two different
// products is the case it doesn't cover — and it is the ordinary case at
// any distillery with staff.
func TestConcurrentRemovalsAcrossLotsAllocateDistinctNumbers(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 4000)
	tank := f.tank(t, "Numbering tank", 5000, 70)

	const lots, each = 6, 10
	lotIDs := make([]string, lots)
	for i := range lotIDs {
		prod := f.product(t, "Numbered "+uuid.NewString()[:8], 750, 40)
		bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
			ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
			DestinationJurisdiction: "CA-ON", BottleCount: 100,
			LotCode: "NUM-" + uuid.NewString()[:8],
		}))
		if err != nil {
			t.Fatalf("CreateBottlingRun: %v", err)
		}
		lotIDs[i] = bottled.Msg.GetPackaged().GetId()
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		nos  []int32
		errs []error
	)
	for _, id := range lotIDs {
		wg.Add(1)
		go func(piID string) {
			defer wg.Done()
			resp, e := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
				PackagedInventoryId: piID, BottlesRemoved: each,
				DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
				DestinationName: "LCBO",
			}))
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				errs = append(errs, e)
				return
			}
			nos = append(nos, resp.Msg.GetRemoval().GetRemovalNo())
		}(id)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Errorf("%d of %d concurrent removals against different lots failed; first: %v",
			len(errs), lots, errs[0])
	}
	seen := map[int32]bool{}
	for _, n := range nos {
		if seen[n] {
			t.Errorf("removal number %d was allocated twice", n)
		}
		seen[n] = true
	}
	if len(nos) != lots {
		t.Errorf("recorded %d of %d removals — the rest are lost shipments", len(nos), lots)
	}
}
