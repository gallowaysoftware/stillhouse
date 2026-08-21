package rpc

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN A1. `excise.Owed` was called in exactly one place — a removal — and
// the B266 derived its whole duty figure from removal totals. That is the
// excise-warehouse pattern: hold packaged spirits non-duty-paid, pay when
// they leave for the duty-paid market.
//
// A spirits licensee WITHOUT an excise warehouse licence cannot possess
// non-duty-paid packaged spirits at all (EDM3-1-1 ¶18), and duty becomes
// payable at the time the spirits are packaged (¶29). For that licensee
// Stillhouse reported duty in the month of sale when CRA expects it in the
// month of bottling — a timing error on a filed return, carrying interest.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// bottleAndRemove runs one bottling run and removes every bottle from it,
// returning the duty each side recorded. The two figures together are what
// the whole feature is about: exactly one of them is non-zero.
func bottleAndRemove(t *testing.T, f *dutyFixture, bottles int32, when string) (
	packagingDuty, removalDuty float64) {
	t.Helper()
	prod := f.product(t, "Basis "+uuid.NewString()[:8], 750, 40)
	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: f.basisTank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: bottles,
		LotCode: "BASIS-" + uuid.NewString()[:8], BottlingDate: when,
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun (%s): %v", when, err)
	}
	_, packagingDuty = f.runDuty(t, bottled.Msg.GetRun().GetId())

	removed, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: bottled.Msg.GetPackaged().GetId(), BottlesRemoved: bottles,
		DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
		DestinationName: "LCBO", RemovalDate: when,
	}))
	if err != nil {
		t.Fatalf("CreateRemoval (%s): %v", when, err)
	}
	return packagingDuty, removed.Msg.GetRemoval().GetDutyAmountCad()
}

// expectedDuty is the duty on `bottles` 750 mL bottles at 40%, computed
// independently of the code under test.
func expectedDuty(t *testing.T, bottles int32, on time.Time) float64 {
	t.Helper()
	band, err := excise.RateOn(on)
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	return float64(bottles) * 0.750 * 0.40 * band.PerLAAOver7Pct
}

// Without a warehouse licence, duty crystallises at the bottling run and
// the removal that follows carries none. This is the case Stillhouse got
// wrong for every distillery that is not an excise warehouse — which,
// for a craft distiller, is most of them.
func TestDutyCrystallisesAtPackagingWithoutWarehouseLicence(t *testing.T) {
	f := newBasisFixture(t)
	f.assertDutyPoint(t, sqlcgen.DutyPointAtPackaging)

	today := time.Now().UTC()
	packaging, removal := bottleAndRemove(t, f, 200, today.Format("2006-01-02"))

	want := expectedDuty(t, 200, today)
	if !near(packaging, want, 1e-6) {
		t.Errorf("duty at packaging: got %v, want %v", packaging, want)
	}
	if removal != 0 {
		t.Errorf("the removal also charged %v — this stock was already dutied when it was bottled", removal)
	}
}

// With a warehouse licence the old behaviour is the right behaviour: the
// bottling run is not a duty event and the removal is.
func TestDutyCrystallisesAtRemovalWithWarehouseLicence(t *testing.T) {
	f := newBasisFixture(t)
	f.warehouseLicensed(t)

	today := time.Now().UTC()
	packaging, removal := bottleAndRemove(t, f, 200, today.Format("2006-01-02"))

	want := expectedDuty(t, 200, today)
	if packaging != 0 {
		t.Errorf("the bottling run charged %v — an excise warehouse holds packaged spirits non-duty-paid", packaging)
	}
	if !near(removal, want, 1e-6) {
		t.Errorf("duty at removal: got %v, want %v", removal, want)
	}
}

// The whole point of the cutover, and the one property that must hold in
// both directions: across a change of basis, every litre is dutied exactly
// once.
//
// Stock bottled before the cutover was never dutied at its bottling, so
// its removal must still carry duty — otherwise those litres are dutied
// never. Stock bottled after was dutied at packaging, so its removal must
// carry none — otherwise those litres are dutied twice.
func TestNoLitreIsDutiedTwiceOrNeverAcrossACutover(t *testing.T) {
	f := newBasisFixture(t)
	// Everything from 1 June 2026 is on the packaging basis; before that,
	// this tenant was recording duty at removal, and some of it is filed.
	cut := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	f.cutover(t, cut)

	oldStockPackaging, oldStockRemoval := bottleAndRemove(t, f, 100, "2026-05-20")
	newStockPackaging, newStockRemoval := bottleAndRemove(t, f, 100, "2026-06-20")

	wantOld := expectedDuty(t, 100, time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	wantNew := expectedDuty(t, 100, time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC))

	// Pre-cutover stock: dutied on the way out, exactly as it always was.
	if oldStockPackaging != 0 {
		t.Errorf("stock bottled before the cutover was dutied at packaging (%v) — "+
			"that re-attributes duty into a period that may already be filed", oldStockPackaging)
	}
	if !near(oldStockRemoval, wantOld, 1e-6) {
		t.Errorf("pre-cutover stock removal duty: got %v, want %v — "+
			"these litres were never dutied at bottling, so if the removal drops them they are dutied never",
			oldStockRemoval, wantOld)
	}

	// Post-cutover stock: dutied at bottling, and the removal adds nothing.
	if !near(newStockPackaging, wantNew, 1e-6) {
		t.Errorf("post-cutover packaging duty: got %v, want %v", newStockPackaging, wantNew)
	}
	if newStockRemoval != 0 {
		t.Errorf("post-cutover stock was dutied again on removal (%v) — dutied twice", newStockRemoval)
	}

	// Stated as the invariant: each parcel pays once, and the total is
	// what a hand calculation says.
	for _, c := range []struct {
		name              string
		packaging, remove float64
		want              float64
	}{
		{"pre-cutover parcel", oldStockPackaging, oldStockRemoval, wantOld},
		{"post-cutover parcel", newStockPackaging, newStockRemoval, wantNew},
	} {
		if got := c.packaging + c.remove; !near(got, c.want, 1e-6) {
			t.Errorf("%s paid %v across both events, want %v", c.name, got, c.want)
		}
	}
}

// The duty point is derived from the licence, not chosen. Setting the
// licence number moves it and clearing it moves it back — there is no way
// for the two to disagree, because the database computes one from the
// other.
func TestDutyPointFollowsTheWarehouseLicence(t *testing.T) {
	f := newBasisFixture(t)
	f.assertDutyPoint(t, sqlcgen.DutyPointAtPackaging)

	f.warehouseLicensed(t)
	f.assertDutyPoint(t, sqlcgen.DutyPointAtRemoval)

	if _, err := f.pool.Exec(f.ctx,
		`UPDATE tenants SET excise_warehouse_licence_number = NULL WHERE id = $1`,
		f.tenant.ID); err != nil {
		t.Fatalf("clear licence: %v", err)
	}
	f.assertDutyPoint(t, sqlcgen.DutyPointAtPackaging)

	// An empty string is not a licence either — a UI that submits a
	// cleared text field sends "" rather than NULL, and treating that as
	// "holds a warehouse licence" would put the tenant on the wrong basis
	// silently.
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE tenants SET excise_warehouse_licence_number = '' WHERE id = $1`,
		f.tenant.ID); err != nil {
		t.Fatalf("blank licence: %v", err)
	}
	f.assertDutyPoint(t, sqlcgen.DutyPointAtPackaging)
}

// The return has to add up on either basis, and across a cutover it has to
// add up out of both halves.
func TestB266DutyComesFromWhicheverEventCrystallisedIt(t *testing.T) {
	f := newBasisFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	cut := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	f.cutover(t, cut)

	// One period holding both: stock bottled on the 10th under the old
	// basis and removed on the 20th, plus stock bottled on the 20th under
	// the new one.
	oldPack, oldRemove := bottleAndRemove(t, f, 100, "2026-06-10")
	newPack, newRemove := bottleAndRemove(t, f, 60, "2026-06-20")

	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	rep := resp.Msg.GetReport()

	if got, want := rep.GetDutyPoint(), stillhousev1.DutyPoint_DUTY_POINT_AT_PACKAGING; got != want {
		t.Errorf("duty point on the return: got %v, want %v", got, want)
	}
	if got, want := rep.GetDutyPointEffectiveFrom(), "2026-06-15"; got != want {
		t.Errorf("cutover on the return: got %q, want %q", got, want)
	}

	// Total duty is both halves. Choosing one would drop the other, and
	// this is exactly the period where that shows.
	wantTotal := oldPack + oldRemove + newPack + newRemove
	if got := rep.GetDutyPayableCad(); !near(got, round2cents(wantTotal), 0.011) {
		t.Errorf("duty payable: got %v, want %v (%v at packaging + %v at removal)",
			got, wantTotal, newPack, oldRemove)
	}
	// And each half is reported on its own line, against the quantity it
	// is charged on.
	if got := rep.GetPackagedDutiedOver7DutyCad(); !near(got, round2cents(newPack), 0.011) {
		t.Errorf("duty at packaging line: got %v, want %v", got, newPack)
	}
	if got := rep.GetPackagedRemovedOver7DutyCad(); !near(got, round2cents(oldRemove), 0.011) {
		t.Errorf("duty at removal line: got %v, want %v", got, oldRemove)
	}
	if got, want := rep.GetPackagedDutiedOver7Laa()*rep.GetDutyRatePerLaa(),
		rep.GetPackagedDutiedOver7DutyCad(); !near(got, want, 0.011) {
		t.Errorf("packaging line doesn't multiply out: %v LAA × %v = %v, line says %v",
			rep.GetPackagedDutiedOver7Laa(), rep.GetDutyRatePerLaa(), got, want)
	}

	// The duty-paid / non-duty-paid split of what was packaged. 60 bottles
	// went in duty-paid, 100 did not.
	if got, want := rep.GetPackagedDutyPaidBottles(), int32(60); got != want {
		t.Errorf("packaged duty-paid bottles: got %d, want %d", got, want)
	}
	if got, want := rep.GetPackagedNonDutyPaidBottles(), int32(100); got != want {
		t.Errorf("packaged non-duty-paid bottles: got %d, want %d", got, want)
	}
	if got, want := rep.GetPackagedDutyPaidBottles()+rep.GetPackagedNonDutyPaidBottles(),
		rep.GetPackagedPackagedBottles(); got != want {
		t.Errorf("the two duty treatments sum to %d but %d bottles were packaged", got, want)
	}
}
