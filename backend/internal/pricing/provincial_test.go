package pricing

import (
	"math"
	"testing"
)

// dutyPerLAA is the rate in force from 2026-04-01; see internal/excise.
const dutyPerLAA = 14.117

func find(t *testing.T, rows []Breakdown, jurisdiction string) Breakdown {
	t.Helper()
	for _, r := range rows {
		if r.Jurisdiction == jurisdiction {
			return r
		}
	}
	t.Fatalf("no breakdown for %s", jurisdiction)
	return Breakdown{}
}

func TestFederalExciseIsLAABased(t *testing.T) {
	// A 750 mL bottle at 40 % holds 0.3 L of absolute alcohol.
	rows := ComputeBreakdown(20, 750, 40, dutyPerLAA)
	want := 0.3 * dutyPerLAA
	for _, r := range rows {
		if math.Abs(r.FederalExciseCAD-want) > 1e-9 {
			t.Errorf("%s: excise %.4f, want %.4f", r.Jurisdiction, r.FederalExciseCAD, want)
		}
	}
	// Excise follows the alcohol, not the province — every row is identical.
	first := rows[0].FederalExciseCAD
	for _, r := range rows[1:] {
		if r.FederalExciseCAD != first {
			t.Errorf("%s: excise differs by province, which it must not", r.Jurisdiction)
		}
	}
}

func TestShelfIsTheSumOfItsParts(t *testing.T) {
	rows := ComputeBreakdown(25, 750, 43, dutyPerLAA)
	for _, r := range rows {
		want := r.FOBCAD + r.MarkupCAD + r.PerLitreCAD + r.BasicTaxCAD +
			r.FederalExciseCAD + r.ContainerDepositCAD
		if math.Abs(r.ShelfBeforeSalesTax-want) > 1e-9 {
			t.Errorf("%s: shelf %.4f != sum of components %.4f", r.Jurisdiction, r.ShelfBeforeSalesTax, want)
		}
	}
}

// TestAlbertaIsPerLitreNotPercentage — AGLC charges a flat per-litre rate
// rather than an ad valorem markup, so its markup component must stay zero
// while its per-litre component scales with bottle size.
func TestAlbertaIsPerLitreNotPercentage(t *testing.T) {
	small := find(t, ComputeBreakdown(30, 375, 40, dutyPerLAA), "CA-AB")
	large := find(t, ComputeBreakdown(30, 750, 40, dutyPerLAA), "CA-AB")

	if small.MarkupCAD != 0 || large.MarkupCAD != 0 {
		t.Error("Alberta must not apply an ad valorem markup")
	}
	if !(large.PerLitreCAD > small.PerLitreCAD) {
		t.Error("Alberta's per-litre charge must scale with bottle size")
	}
	// Doubling the bottle doubles the per-litre component exactly.
	if math.Abs(large.PerLitreCAD-2*small.PerLitreCAD) > 1e-9 {
		t.Errorf("750 mL per-litre %.4f should be twice 375 mL's %.4f",
			large.PerLitreCAD, small.PerLitreCAD)
	}
	// And it does not move with FOB.
	dearer := find(t, ComputeBreakdown(60, 750, 40, dutyPerLAA), "CA-AB")
	if dearer.PerLitreCAD != large.PerLitreCAD {
		t.Error("a per-litre charge must not depend on the FOB price")
	}
}

// TestPercentageProvincesScaleWithFOB — the mirror of the Alberta case.
func TestPercentageProvincesScaleWithFOB(t *testing.T) {
	cheap := find(t, ComputeBreakdown(20, 750, 40, dutyPerLAA), "CA-ON")
	dear := find(t, ComputeBreakdown(40, 750, 40, dutyPerLAA), "CA-ON")
	if math.Abs(dear.MarkupCAD-2*cheap.MarkupCAD) > 1e-9 {
		t.Errorf("doubling FOB should double an ad valorem markup: %.4f vs %.4f",
			dear.MarkupCAD, cheap.MarkupCAD)
	}
}

// TestOnSiteRetailNetIsAlwaysTheFOB pins a property that is currently a
// tautology rather than a calculation.
//
// The on-site shelf price is built as FOB + basic tax + excise + deposit,
// and the net then subtracts exactly those three again — so the result can
// only ever be the FOB that went in. It is displayed as a computed column
// beside the others, which reads as though it carries information.
//
// That may be the intended model (sell on-site below the monopoly shelf
// price and net your wholesale price), but it is worth being deliberate
// about: the alternative reading is that selling on-site at the monopoly's
// shelf price nets FOB + markup + per-litre, which is materially more and
// is usually the reason a distillery opens a shop. Pinned so the choice is
// made on purpose rather than drifting.
func TestOnSiteRetailNetIsAlwaysTheFOB(t *testing.T) {
	for _, fob := range []float64{12, 25, 47.5} {
		for _, r := range ComputeBreakdown(fob, 750, 40, dutyPerLAA) {
			if math.Abs(r.OnSiteRetailNetCAD-fob) > 1e-9 {
				t.Errorf("%s: on-site net %.4f, expected it to equal the FOB %.4f",
					r.Jurisdiction, r.OnSiteRetailNetCAD, fob)
			}
		}
	}
}

func TestEveryJurisdictionIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Markups {
		if m.Jurisdiction == "" || m.Name == "" {
			t.Errorf("jurisdiction %+v is missing an identifier", m)
		}
		if seen[m.Jurisdiction] {
			t.Errorf("duplicate jurisdiction %s", m.Jurisdiction)
		}
		seen[m.Jurisdiction] = true
		// Canada-only, per the project's framing.
		if len(m.Jurisdiction) < 5 || m.Jurisdiction[:3] != "CA-" {
			t.Errorf("%s is not a Canadian ISO 3166-2 code", m.Jurisdiction)
		}
		if m.MarkupPct < 0 || m.PerLitreCAD < 0 || m.SpiritsBasicTaxPct < 0 || m.ContainerDepositCAD < 0 {
			t.Errorf("%s has a negative rate", m.Jurisdiction)
		}
		if m.Notes == "" {
			t.Errorf("%s has no caveat note; the UI shows one", m.Jurisdiction)
		}
	}
	if len(Markups) == 0 {
		t.Fatal("no jurisdictions configured")
	}
}

func TestZeroAlcoholPaysNoExcise(t *testing.T) {
	for _, r := range ComputeBreakdown(10, 750, 0, dutyPerLAA) {
		if r.FederalExciseCAD != 0 {
			t.Errorf("%s: a 0 %% product should attract no excise, got %.4f",
				r.Jurisdiction, r.FederalExciseCAD)
		}
	}
}
