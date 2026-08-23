package pricing

import (
	"math"
	"strings"
	"testing"
)

const dutyPerLAA = 14.117 // internal/excise, in force 2026-04-01

func bottle() Input {
	return Input{
		FOBCAD: 25, BottleSizeML: 750, BottleABVPct: 40,
		FederalDutyPerLAA: dutyPerLAA,
	}
}

func find(t *testing.T, rows []JurisdictionBreakdown, code string) JurisdictionBreakdown {
	t.Helper()
	for _, r := range rows {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("no breakdown for %s", code)
	return JurisdictionBreakdown{}
}

// TestUnknownRateRefusesToCompute is the property this rewrite exists for.
// Ontario's spirits mark-up is not published, so the wholesale channel
// must decline rather than produce a confident wrong price.
func TestUnknownRateRefusesToCompute(t *testing.T) {
	on := find(t, Compute(bottle()), "CA-ON")
	if on.Wholesale.Computable {
		t.Error("Ontario wholesale must not compute — the spirits mark-up is unpublished")
	}
	if len(on.Wholesale.Missing) == 0 {
		t.Fatal("a refusal must say what is missing")
	}
	// And the explanation has to be actionable, not just "unknown".
	why := on.Wholesale.Missing[0].Why
	if !strings.Contains(why, "pricing@lcbo.com") {
		t.Errorf("the missing-rate note should say where to get the number, got: %q", why)
	}
	// A refusal leaves the money fields alone rather than half-filling them.
	if on.Wholesale.PriceToBuyerCAD != 0 || on.Wholesale.MarkupCAD != 0 {
		t.Error("an uncomputable channel must not report partial figures")
	}
}

// TestPEIWholesaleAlsoRefuses — PEI delegates pricing to Commission price
// lists, so there is no rate to use there either.
func TestPEIWholesaleAlsoRefuses(t *testing.T) {
	pe := find(t, Compute(bottle()), "CA-PE")
	if pe.Wholesale.Computable {
		t.Error("PEI wholesale must not compute — reg. s.89 delegates pricing to the Commission")
	}
}

func TestLandedCostCompoundsFederalDutyBeforeMarkup(t *testing.T) {
	in := bottle()
	// 750 mL at 40% is 0.3 L of absolute alcohol.
	wantExcise := 0.3 * dutyPerLAA
	if math.Abs(in.FederalExciseCAD()-wantExcise) > 1e-9 {
		t.Errorf("excise = %.4f, want %.4f", in.FederalExciseCAD(), wantExcise)
	}
	// Landed cost includes the duty, so an ad valorem mark-up is charged
	// on top of it — the two compound rather than add.
	if math.Abs(in.LandedCostCAD()-(25+wantExcise)) > 1e-9 {
		t.Errorf("landed cost = %.4f, want %.4f", in.LandedCostCAD(), 25+wantExcise)
	}
	ns := find(t, Compute(in), "CA-NS")
	if !ns.Wholesale.Computable {
		t.Fatal("NS carries an indicative mark-up and should compute")
	}
	// The mark-up is a fraction of landed cost, not of FOB. If it were
	// charged on FOB alone it would be smaller by 0.85 × the duty.
	onFOBOnly := 25 * 0.85
	if ns.Wholesale.MarkupCAD <= onFOBOnly {
		t.Errorf("mark-up %.4f should exceed the FOB-only figure %.4f — it applies to landed cost",
			ns.Wholesale.MarkupCAD, onFOBOnly)
	}
}

// TestAlbertaIsVolumetric — a flat per-litre mark-up must not move with
// the supplier quote, and must scale with bottle size.
func TestAlbertaIsVolumetric(t *testing.T) {
	cheap, dear := bottle(), bottle()
	dear.FOBCAD = 60
	a := find(t, Compute(cheap), "CA-AB").Wholesale
	b := find(t, Compute(dear), "CA-AB").Wholesale
	if a.MarkupCAD != b.MarkupCAD {
		t.Errorf("a per-litre mark-up must not depend on FOB: %.4f vs %.4f", a.MarkupCAD, b.MarkupCAD)
	}
	small := bottle()
	small.BottleSizeML = 375
	c := find(t, Compute(small), "CA-AB").Wholesale
	if math.Abs(a.MarkupCAD-2*c.MarkupCAD) > 1e-9 {
		t.Errorf("750 mL mark-up %.4f should be twice the 375 mL one %.4f", a.MarkupCAD, c.MarkupCAD)
	}
}

// TestWholesaleNetsTheQuote — the distillery keeps its quote; everything
// above it belongs to the board, the carrier or the Crown. This used to
// be dressed up as a computed column that could only ever echo the FOB.
func TestWholesaleNetsTheQuote(t *testing.T) {
	for _, fob := range []float64{12, 25, 47.5} {
		in := bottle()
		in.FOBCAD = fob
		ns := find(t, Compute(in), "CA-NS").Wholesale
		if ns.DistilleryNetCAD != fob {
			t.Errorf("net %.2f, want the quote %.2f", ns.DistilleryNetCAD, fob)
		}
		// And the buyer pays materially more, which is the actual point.
		if ns.PriceToBuyerCAD <= fob {
			t.Errorf("buyer pays %.2f, which is not above the quote %.2f", ns.PriceToBuyerCAD, fob)
		}
	}
}

// TestOnSiteNeedsAShelfPrice — the distillery sets it, so nothing can be
// computed until it does.
func TestOnSiteNeedsAShelfPrice(t *testing.T) {
	ns := find(t, Compute(bottle()), "CA-NS").OnSiteRetail
	if ns.Computable {
		t.Error("on-site cannot be priced without a shelf price")
	}
	if len(ns.Missing) == 0 || !strings.Contains(ns.Missing[0].Why, "your own shop") {
		t.Errorf("should ask for the shelf price, got %+v", ns.Missing)
	}
}

// TestOnSiteIsACeilingWhileTheProvincialCutIsUnknown — the arithmetic
// runs, but the caller must be told the province's share is unrecorded,
// because assuming zero is the optimistic error.
func TestOnSiteIsACeiling(t *testing.T) {
	in := bottle()
	in.OnSiteRetailPriceCAD = 60
	ns := find(t, Compute(in), "CA-NS").OnSiteRetail
	if !ns.Computable {
		t.Fatal("with a shelf price it should compute")
	}
	if ns.LowestProvenance != Unknown {
		t.Error("an unrecorded provincial cut must drag the provenance to unknown")
	}
	if len(ns.Missing) == 0 {
		t.Error("and must say so")
	}
	// The distillery keeps the shelf price less excise, tax and deposit.
	want := 60 - in.FederalExciseCAD() - ns.SalesTaxCAD - ns.ContainerDepositCAD
	if math.Abs(ns.DistilleryNetCAD-want) > 1e-9 {
		t.Errorf("net %.4f, want %.4f", ns.DistilleryNetCAD, want)
	}
	// On-site should beat wholesale at a sensible shelf price — that's why
	// distilleries open shops.
	if ns.DistilleryNetCAD <= find(t, Compute(in), "CA-NS").Wholesale.DistilleryNetCAD {
		t.Error("on-site at $60 should net more than a $25 wholesale quote")
	}
}

// TestSalesTaxIsBackedOutOfAShelfPrice — a shop's shelf price includes
// tax; adding tax on top of it would overstate what the province takes.
func TestSalesTaxIsBackedOutOfAShelfPrice(t *testing.T) {
	in := bottle()
	in.OnSiteRetailPriceCAD = 57.50
	ns := find(t, Compute(in), "CA-NS").OnSiteRetail // NS HST 15%
	want := 57.50 - 57.50/1.15
	if math.Abs(ns.SalesTaxCAD-want) > 1e-9 {
		t.Errorf("sales tax %.4f, want %.4f (backed out, not added on)", ns.SalesTaxCAD, want)
	}
	if ns.SalesTaxCAD >= 57.50*0.15 {
		t.Error("tax was added on top of a tax-inclusive price")
	}
}

// TestExportCarriesItsAssumption — export looks good precisely because of
// the duty relief, so the assumption must travel with the number.
func TestExportCarriesItsAssumption(t *testing.T) {
	ex := find(t, Compute(bottle()), "CA-ON").Export
	if !ex.Computable {
		t.Fatal("export should compute — no provincial rate is needed")
	}
	if ex.FederalExciseCAD != 0 {
		t.Error("a genuine export is modelled as relieved of federal excise")
	}
	if ex.MarkupCAD != 0 {
		t.Error("no provincial mark-up applies to an export")
	}
	if len(ex.Missing) == 0 {
		t.Fatal("the duty-relief assumption must be surfaced, not buried")
	}
	if !strings.Contains(ex.Missing[0].Why, "NOT an export") {
		t.Errorf("should warn that interprovincial is not export, got: %q", ex.Missing[0].Why)
	}
}

// TestProvenanceTracksTheWeakestRate — a figure is only as good as the
// worst number in it.
func TestProvenanceTracksTheWeakestRate(t *testing.T) {
	ns := find(t, Compute(bottle()), "CA-NS").Wholesale
	if ns.LowestProvenance != Indicative {
		t.Errorf("NS leans on an indicative mark-up, so the result is indicative, got %v",
			ns.LowestProvenance)
	}
}

func TestEveryJurisdictionIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, j := range Jurisdictions {
		if !strings.HasPrefix(j.Code, "CA-") {
			t.Errorf("%s is not a Canadian ISO 3166-2 code", j.Code)
		}
		if seen[j.Code] {
			t.Errorf("duplicate jurisdiction %s", j.Code)
		}
		seen[j.Code] = true
		if j.Name == "" {
			t.Errorf("%s has no name", j.Code)
		}
		// Every unknown rate must explain itself — an unexplained gap is
		// indistinguishable from an oversight.
		for name, r := range map[string]Rate{
			"wholesale markup": j.WholesaleMarkupPctOfLanded,
			"per litre":        j.WholesalePerLitreCAD,
			"COSD":             j.COSDPerLitreCAD,
			"provincial tax":   j.ProvincialSpiritsTaxPct,
			"on-site":          j.OnSite.Rate,
		} {
			if r.Provenance == Unknown && r.Note == "" && r.Value == 0 {
				// A zero-valued unknown with no note is only acceptable
				// where the jurisdiction simply has no such charge.
				continue
			}
			if r.Provenance == Unknown && r.Note == "" {
				t.Errorf("%s %s: unknown rate with no explanation", j.Code, name)
			}
			if r.Provenance != Unknown && r.Source == "" {
				t.Errorf("%s %s: known rate with no source", j.Code, name)
			}
		}
	}
	if len(Jurisdictions) == 0 {
		t.Fatal("no jurisdictions configured")
	}
}

func TestZeroAlcoholPaysNoExcise(t *testing.T) {
	in := bottle()
	in.BottleABVPct = 0
	if in.FederalExciseCAD() != 0 {
		t.Errorf("a 0%% product attracts no excise, got %.4f", in.FederalExciseCAD())
	}
}

// Every rate the domain model carries has a Source and an AsOf, and
// until stage 166 none of it reached anywhere an operator could see. A
// price you cannot trace is a price you cannot quote.
func TestEveryComputedFigureCitesItsRates(t *testing.T) {
	in := Input{
		FOBCAD: 30, FreightCAD: 2, BottleSizeML: 750, BottleABVPct: 40,
		FederalDutyPerLAA: 13.864, OnSiteRetailPriceCAD: 45,
	}
	out := Compute(in)
	if len(out) == 0 {
		t.Fatal("no jurisdictions computed")
	}

	var checked int
	for _, j := range out {
		for _, ch := range []ChannelResult{j.Wholesale, j.OnSiteRetail} {
			if !ch.Computable {
				continue
			}
			checked++
			if len(ch.Citations) == 0 {
				t.Errorf("%s %s produced a price citing no rates at all",
					j.Name, ch.Channel)
				continue
			}
			for _, c := range ch.Citations {
				if c.What == "" {
					t.Errorf("%s %s cites a rate with no name", j.Name, ch.Channel)
				}
				// A sourced rate must say where it came from. An
				// indicative one need not — that is what indicative
				// means — but it must still be labelled as such, which
				// the provenance does.
				if c.Provenance == Sourced && c.Source == "" {
					t.Errorf("%s %s: %q claims to be sourced but names no source",
						j.Name, ch.Channel, c.What)
				}
				if c.Provenance == Sourced && c.AsOf == "" {
					t.Errorf("%s %s: %q claims to be sourced but has no as-of date",
						j.Name, ch.Channel, c.What)
				}
			}
			// The weakest citation and the reported provenance have to
			// agree, or the label on the figure is not describing the
			// figure.
			weakest := Sourced
			for _, c := range ch.Citations {
				if c.Provenance < weakest {
					weakest = c.Provenance
				}
			}
			if weakest != ch.LowestProvenance {
				t.Errorf("%s %s reports provenance %v but its weakest citation is %v",
					j.Name, ch.Channel, ch.LowestProvenance, weakest)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no computable channel was checked")
	}
}
