package pricing

import (
	"strconv"
	"strings"
	"testing"
)

// The band boundary is a provincial choice, and the 750 mL bottle a
// distillery actually fills falls on a different side of it in each
// province. That is the whole reason a schedule replaced a flat rate, so
// it is what gets pinned.
func TestDepositBandsBySizeAndProvince(t *testing.T) {
	cases := []struct {
		code   string
		sizeML int32
		want   float64
	}{
		// Alberta bands at 1 L: a standard bottle is in the LOWER band.
		// This is the case the old flat 25¢ got wrong.
		{"CA-AB", 375, 0.10},
		{"CA-AB", 750, 0.10},
		{"CA-AB", 1000, 0.10}, // inclusive upper bound
		{"CA-AB", 1001, 0.25},
		{"CA-AB", 1750, 0.25},
		// Ontario bands at 630 mL: a standard bottle is in the UPPER
		// band. Same bottle, other side of the boundary.
		{"CA-ON", 375, 0.10},
		{"CA-ON", 630, 0.10}, // inclusive upper bound
		{"CA-ON", 631, 0.20},
		{"CA-ON", 750, 0.20},
		// British Columbia abolished its bands in 2020: one rate, and
		// the size genuinely does not matter.
		{"CA-BC", 200, 0.10},
		{"CA-BC", 750, 0.10},
		{"CA-BC", 3000, 0.10},
	}
	for _, c := range cases {
		j := Find(c.code)
		if j == nil {
			t.Fatalf("%s not in Jurisdictions", c.code)
		}
		got := j.ContainerDeposit.For(c.sizeML)
		if !got.Known() {
			t.Errorf("%s %d mL: rate is unknown, want %.2f", c.code, c.sizeML, c.want)
			continue
		}
		if got.Value != c.want {
			t.Errorf("%s %d mL: deposit %.2f, want %.2f", c.code, c.sizeML, got.Value, c.want)
		}
	}
}

// A jurisdiction added without deposit data must make the report say so.
// Zero and "we don't know" are the same number in a total and only one of
// them is ever true.
func TestEmptyDepositScheduleRefusesRatherThanReturningZero(t *testing.T) {
	var s DepositSchedule
	r := s.For(750)
	if r.Known() {
		t.Fatalf("empty schedule returned a usable rate %+v", r)
	}
	if r.Note == "" {
		t.Error("an unknown rate has to explain its own absence")
	}
	// And a schedule whose bands do not reach the container is the same
	// kind of gap, not a container that owes nothing.
	bounded := DepositSchedule{{MaxML: 500, Rate: Rate{Value: 0.10, Provenance: Sourced}}}
	if got := bounded.For(750); got.Known() {
		t.Errorf("container above every band returned %+v, want unknown", got)
	}
}

// Quebec's glass expansion was postponed to 2027, so a glass spirits
// bottle carries no deposit there today — while a plastic one carries
// 10¢. Stillhouse does not record container material, so the only honest
// answer is to refuse and explain. This test exists because the file
// previously carried 20¢, which is neither answer.
func TestQuebecDepositRefusesPendingTheGlassExpansion(t *testing.T) {
	j := Find("CA-QC")
	if j == nil {
		t.Fatal("CA-QC not in Jurisdictions")
	}
	r := j.ContainerDeposit.For(750)
	if r.Known() {
		t.Fatalf("Quebec returned a deposit of %.2f; glass spirits are not under "+
			"deposit until 2027 and Stillhouse cannot tell glass from plastic", r.Value)
	}
	for _, want := range []string{"2027", "glass", "plastic"} {
		if !strings.Contains(r.Note, want) {
			t.Errorf("Quebec's refusal does not mention %q; the operator cannot act on it", want)
		}
	}
}

// Sourced is a claim about where a number came from. A Sourced rate with
// no source or no date cannot be checked years later, which is the only
// reason the grade exists.
func TestSourcedRatesNameTheirSourceAndDate(t *testing.T) {
	check := func(what string, r Rate) {
		if r.Provenance != Sourced {
			return
		}
		if r.Source == "" {
			t.Errorf("%s is marked sourced with no source", what)
		}
		if r.AsOf == "" || r.AsOf == "unknown" {
			t.Errorf("%s is marked sourced with AsOf %q", what, r.AsOf)
		}
	}
	for _, j := range Jurisdictions {
		for i, b := range j.ContainerDeposit {
			check(j.Code+" deposit band "+strconv.Itoa(i), b.Rate)
		}
		check(j.Code+" recycling fee", j.ContainerRecyclingFeeCAD)
		check(j.Code+" sales tax", j.SalesTaxPct)
		check(j.Code+" markup", j.WholesaleMarkupPctOfLanded)
	}
}

// Every jurisdiction carries a schedule. An entry added without one would
// pass the band tests above by simply not being in them.
func TestEveryJurisdictionCarriesADepositSchedule(t *testing.T) {
	for _, j := range Jurisdictions {
		if len(j.ContainerDeposit) == 0 {
			t.Errorf("%s has no deposit schedule; use flatDeposit(unknownRate(...)) "+
				"so the report explains itself rather than reporting nil", j.Code)
		}
	}
}
