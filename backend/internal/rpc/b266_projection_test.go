package rpc

import (
	"testing"
	"time"

	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
)

// The B266 projection is what CRA reads. Until stage 139 it took a
// concrete *sqlcgen.Queries, so none of the arithmetic below could be
// exercised without standing up Postgres and seeding a tenant — which is
// why the two reverse-walk defects it has shipped (stages 109 and 134)
// were both found by hand against a live system. These tests run in
// microseconds against no infrastructure.

var (
	testPeriodStart = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	testPeriodEnd   = time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	testGeneratedAt = time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC)
)

// A period with nothing in it must produce a return of zeros — not a
// return with a negative opening balance, which is what a naive
// reverse-walk produces and what a distillery's first-ever filing looks
// like.
func TestProjectB266_EmptyPeriod(t *testing.T) {
	rep := projectB266(b266Totals{}, testPeriodStart, testPeriodEnd, testGeneratedAt)

	if rep.PeriodStart != "2026-05-01" || rep.PeriodEnd != "2026-05-31" {
		t.Errorf("period: got %s → %s", rep.PeriodStart, rep.PeriodEnd)
	}
	if rep.BulkOpeningLaa != 0 || rep.BulkClosingLaa != 0 {
		t.Errorf("bulk opening/closing: got %v/%v, want 0/0", rep.BulkOpeningLaa, rep.BulkClosingLaa)
	}
	if rep.PackagedOpeningLaa != 0 || rep.PackagedClosingLaa != 0 {
		t.Errorf("packaged opening/closing: got %v/%v, want 0/0",
			rep.PackagedOpeningLaa, rep.PackagedClosingLaa)
	}
	if rep.DutyPayableCad != 0 {
		t.Errorf("duty on an empty period: got %v, want 0", rep.DutyPayableCad)
	}
	// A nil byReason map must read as zero everywhere, not panic. A tenant
	// with no bulk movement at all in the period gets exactly that map.
	if rep.BulkProductionLaa != 0 {
		t.Errorf("production from a nil reason map: got %v", rep.BulkProductionLaa)
	}
}

// The bulk walk has to close: opening + receipts − withdrawals = closing.
// This is the identity an auditor checks first.
func TestProjectB266_BulkBooksClose(t *testing.T) {
	rep := projectB266(b266Totals{
		byReason: map[string]float64{
			"production_gauge":      100,
			"transfer_in_bond":      20,
			"transfer_to_packaging": 30,
			"transfer_out_in_bond":  5,
			"loss_evaporation":      2,
			"loss_unaccounted":      1,
			"destruction":           3,
		},
		bulkClosingLAA: 500,
	}, testPeriodStart, testPeriodEnd, testGeneratedAt)

	if got, want := rep.BulkLossesLaa, 3.0; !nearly(got, want) {
		t.Errorf("losses (evaporation + unaccounted): got %v, want %v", got, want)
	}
	// 500 − (100+20) + (30+5+3+3) = 421
	if got, want := rep.BulkOpeningLaa, 421.0; !nearly(got, want) {
		t.Errorf("opening: got %v, want %v", got, want)
	}
	receipts := rep.BulkProductionLaa + rep.BulkReceivedInBondLaa
	withdrawals := rep.BulkTransferredToPackagingLaa + rep.BulkTransferredOutInBondLaa +
		rep.BulkLossesLaa + rep.BulkDestroyedLaa
	if got := rep.BulkOpeningLaa + receipts - withdrawals; !nearly(got, rep.BulkClosingLaa) {
		t.Errorf("bulk books don't close: opening + receipts - withdrawals = %v, closing = %v",
			got, rep.BulkClosingLaa)
	}
}

// Stage 134's defect, pinned. A blend moves alcohol between two of the
// distillery's own vessels: nothing enters or leaves the premises, so the
// closing balance is untouched. Counting it as a receipt drove the
// reverse-walked opening balance DOWN by the blended LAA and reported a
// receipt that never happened.
func TestProjectB266_BlendIsNotAReceipt(t *testing.T) {
	base := b266Totals{
		byReason:       map[string]float64{"production_gauge": 100},
		bulkClosingLAA: 500,
	}
	withBlend := b266Totals{
		byReason:       map[string]float64{"production_gauge": 100, "blend": 250},
		bulkClosingLAA: 500,
	}

	a := projectB266(base, testPeriodStart, testPeriodEnd, testGeneratedAt)
	b := projectB266(withBlend, testPeriodStart, testPeriodEnd, testGeneratedAt)

	if !nearly(a.BulkOpeningLaa, b.BulkOpeningLaa) {
		t.Errorf("a blend moved the opening balance: %v without, %v with — "+
			"an internal move must not touch either end of the walk",
			a.BulkOpeningLaa, b.BulkOpeningLaa)
	}
	// It is still reported, for information.
	if got, want := b.BulkBlendInLaa, 250.0; !nearly(got, want) {
		t.Errorf("blend line: got %v, want %v (reported but not counted)", got, want)
	}
}

// Adopted stock (stage 124) was in the warehouse before the period; only
// the bookkeeping is new. It must land in the opening balance, never among
// receipts — counting it as production overstates what the distillery made
// on a return CRA reads.
func TestProjectB266_AdoptedStockLandsInOpening(t *testing.T) {
	rep := projectB266(b266Totals{
		byReason:       map[string]float64{"opening_inventory": 300},
		bulkClosingLAA: 300,
	}, testPeriodStart, testPeriodEnd, testGeneratedAt)

	if got, want := rep.BulkOpeningLaa, 300.0; !nearly(got, want) {
		t.Errorf("opening: got %v, want %v — adopted stock belongs here", got, want)
	}
	if rep.BulkProductionLaa != 0 {
		t.Errorf("adopted stock leaked into production: got %v, want 0", rep.BulkProductionLaa)
	}
	if got, want := rep.BulkOpeningInventoryAdoptedLaa, 300.0; !nearly(got, want) {
		t.Errorf("adopted line: got %v, want %v — must be visible on the return it affects", got, want)
	}
}

// Stage 109's P0, pinned. Three quantities that must not be conflated:
// what left bulk (tank gauge), what became sealed bottles, and the loss
// between them. Packaged inventory only ever received the bottles figure,
// so walking back with the tank gauge subtracts alcohol that never
// arrived — and a first-ever return came out negative.
func TestProjectB266_PackagedWalkUsesBottlesNotTankGauge(t *testing.T) {
	rep := projectB266(b266Totals{
		byReason:              map[string]float64{"transfer_to_packaging": 30},
		bottlingDrawnLAA:      30,
		bottlingPackagedLAA:   28,
		bottlingLossLAA:       2,
		bottlingBottles:       1000,
		removedLAA:            10,
		removedBottles:        400,
		packagedClosingLAA:    18,
		packagedClosingBottle: 600,
	}, testPeriodStart, testPeriodEnd, testGeneratedAt)

	// 18 − 28 + 10 = 0: a first-ever return, opening at nothing.
	if got, want := rep.PackagedOpeningLaa, 0.0; !nearly(got, want) {
		t.Errorf("packaged opening: got %v, want %v", got, want)
	}
	if rep.PackagedOpeningLaa < 0 {
		t.Errorf("packaged opening is negative (%v) — inventory cannot start below zero",
			rep.PackagedOpeningLaa)
	}
	if got := rep.PackagedOpeningLaa + rep.PackagedPackagedLaa - rep.PackagedRemovedDutyPaidLaa; !nearly(got, rep.PackagedClosingLaa) {
		t.Errorf("packaged books don't close: %v vs closing %v", got, rep.PackagedClosingLaa)
	}
	// The two sections reconcile: what left bulk for the line is what
	// became bottles plus what was lost getting there.
	if got := rep.PackagedPackagedLaa + rep.PackagedPackagingLossLaa; !nearly(got, rep.BulkTransferredToPackagingLaa) {
		t.Errorf("packaged %v + loss %v = %v, but bulk says %v left for packaging",
			rep.PackagedPackagedLaa, rep.PackagedPackagingLossLaa, got,
			rep.BulkTransferredToPackagingLaa)
	}
}

// The two rate bands are not charged in the same unit — >7% ABV pays per
// litre of absolute alcohol, ≤7% pays per litre of product. Reporting one
// blended "rate per LAA" against a total LAA made the return fail its own
// arithmetic as soon as a period contained both.
func TestProjectB266_BothDutyBands(t *testing.T) {
	band, err := excise.RateOn(testPeriodStart)
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	over7LAA, under7L := 7.775, 40.0
	over7Duty := over7LAA * band.PerLAAOver7Pct     // 109.7597…
	under7Duty := under7L * band.PerLitreAtOrUnder7 // 14.32

	rep := projectB266(b266Totals{
		dutyBand:             band,
		removedLAA:           over7LAA,
		removedBottles:       120,
		removedDutyCAD:       over7Duty + under7Duty,
		removedOver7LAA:      over7LAA,
		removedOver7DutyCAD:  over7Duty,
		removedOver7Bottles:  80,
		removedUnder7Litres:  under7L,
		removedUnder7DutyCAD: under7Duty,
		removedUnder7Bottles: 40,
	}, testPeriodStart, testPeriodEnd, testGeneratedAt)

	if got := rep.PackagedRemovedOver7DutyCad + rep.PackagedRemovedUnder7DutyCad; !nearly(got, rep.DutyPayableCad) {
		t.Errorf("bands don't sum to total: %v + %v = %v, total %v",
			rep.PackagedRemovedOver7DutyCad, rep.PackagedRemovedUnder7DutyCad,
			got, rep.DutyPayableCad)
	}
	// Each band's line must check against the quantity it is charged on.
	if got, want := rep.PackagedRemovedOver7Laa*rep.DutyRatePerLaa, rep.PackagedRemovedOver7DutyCad; !within(got, want, 0.005) {
		t.Errorf(">7%% band: %v LAA × %v = %v, but the line says %v",
			rep.PackagedRemovedOver7Laa, rep.DutyRatePerLaa, got, want)
	}
	if got, want := rep.PackagedRemovedUnder7Litres*rep.DutyRatePerLitreUnder7, rep.PackagedRemovedUnder7DutyCad; !within(got, want, 0.005) {
		t.Errorf("≤7%% band: %v L × %v = %v, but the line says %v",
			rep.PackagedRemovedUnder7Litres, rep.DutyRatePerLitreUnder7, got, want)
	}
	// Quoting the per-LAA rate against the blended total is the mistake
	// stage 134 fixed: 7.775 LAA at $14.117 is $109.76, not $124.08.
	if nearly(rep.PackagedRemovedOver7Laa*rep.DutyRatePerLaa, rep.DutyPayableCad) {
		t.Error("total duty equals the >7% band alone — the ≤7% band was dropped")
	}
	if rep.DutyRatePerLaa != band.PerLAAOver7Pct {
		t.Errorf("rate per LAA: got %v, want %v", rep.DutyRatePerLaa, band.PerLAAOver7Pct)
	}
	if rep.DutyRatePerLitreUnder7 != band.PerLitreAtOrUnder7 {
		t.Errorf("rate per litre ≤7%%: got %v, want %v",
			rep.DutyRatePerLitreUnder7, band.PerLitreAtOrUnder7)
	}
}

// Money and LAA are rounded on the way out — to cents and to four decimal
// places respectively. Negatives reach both: a cask's strength drift is
// negative in a cool, humid warehouse, which is the normal Canadian case.
func TestRoundingIsSymmetricAboutZero(t *testing.T) {
	for _, c := range []struct {
		name     string
		fn       func(float64) float64
		in, want float64
	}{
		{"round4 positive", round4, 0.12345, 0.1235},
		{"round4 negative", round4, -0.12345, -0.1235},
		{"round4 half", round4, -1.00005, -1.0001},
		// 0.125 is exactly representable, so this lands precisely on the
		// half and shows what the old `int(x*100 + 0.5)` idiom got wrong:
		// int() truncates toward zero, so int(-12.5 + 0.5) is -12 and the
		// negative rounded to -0.12 against +0.13 for the positive.
		{"round2 positive half", round2cents, 0.125, 0.13},
		{"round2 negative half", round2cents, -0.125, -0.13},
		{"round2 float noise", round2cents, 109.75970000000001, 109.76},
	} {
		if got := c.fn(c.in); got != c.want {
			t.Errorf("%s: %v → %v, want %v", c.name, c.in, got, c.want)
		}
	}
	// Symmetry, stated directly: rounding must not favour one sign.
	for _, x := range []float64{0.12345, 1.5, 0.00005, 987.65432} {
		if round4(-x) != -round4(x) {
			t.Errorf("round4 asymmetric at %v: +%v vs %v", x, round4(x), round4(-x))
		}
		if round2cents(-x) != -round2cents(x) {
			t.Errorf("round2cents asymmetric at %v: +%v vs %v", x, round2cents(x), round2cents(-x))
		}
	}
}

// A reason the projection doesn't know about must not silently become a
// receipt or a withdrawal. New bulk movement reasons get added over time;
// each one needs a deliberate line, and until it has one the walk should
// be unchanged rather than quietly wrong.
func TestProjectB266_UnknownReasonIsInert(t *testing.T) {
	known := projectB266(b266Totals{
		byReason:       map[string]float64{"production_gauge": 100},
		bulkClosingLAA: 500,
	}, testPeriodStart, testPeriodEnd, testGeneratedAt)
	plus := projectB266(b266Totals{
		byReason:       map[string]float64{"production_gauge": 100, "some_future_reason": 77},
		bulkClosingLAA: 500,
	}, testPeriodStart, testPeriodEnd, testGeneratedAt)

	if !nearly(known.BulkOpeningLaa, plus.BulkOpeningLaa) {
		t.Errorf("an unrecognised reason moved the opening balance: %v vs %v",
			known.BulkOpeningLaa, plus.BulkOpeningLaa)
	}
}

// The projection is pure: same totals in, same return out, and the
// generation timestamp comes from the caller rather than the clock.
func TestProjectB266_IsPure(t *testing.T) {
	totals := b266Totals{
		byReason:            map[string]float64{"production_gauge": 12.5, "blend": 3},
		bulkClosingLAA:      88.125,
		bottlingPackagedLAA: 4.25,
		removedDutyCAD:      60.005,
	}
	a := projectB266(totals, testPeriodStart, testPeriodEnd, testGeneratedAt)
	b := projectB266(totals, testPeriodStart, testPeriodEnd, testGeneratedAt)

	if a.BulkOpeningLaa != b.BulkOpeningLaa || a.DutyPayableCad != b.DutyPayableCad {
		t.Error("projection is not deterministic")
	}
	if got := a.GeneratedAt.AsTime(); !got.Equal(testGeneratedAt) {
		t.Errorf("generated_at: got %v, want the caller's %v", got, testGeneratedAt)
	}
}

func within(a, b, tol float64) bool { return a-b < tol && b-a < tol }
