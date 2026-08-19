package maturation

import (
	"math"
	"testing"
)

const year = 365.25

func TestCurriculumThreeYearCase(t *testing.T) {
	// "Over a three-year maturation period, a cask filled at sixty three
	// percent alcohol by volume may drop to sixty percent" — in a cool,
	// humid warehouse losing about 2 % of its volume a year.
	fill := Snapshot{VolumeL: 200, StrengthPct: 63, LAA: 126}
	// Three years at 2 % a year leaves 200 × 0.98³ = 188.24 L.
	end := 200 * math.Pow(0.98, 3)
	now := Snapshot{VolumeL: end, StrengthPct: 60, LAA: end * 0.60}

	a := Assess(fill, now, 3*year, ClimateCoolHumid, "")
	if !a.Measurable {
		t.Fatalf("should be measurable: %s", a.WhyNot)
	}
	if math.Abs(a.AnnualVolumeLossPct-2) > 0.01 {
		t.Errorf("annual volume loss = %.3f %%, want 2", a.AnnualVolumeLossPct)
	}
	// Strength fell 3 points across 3 years — one a year, as described.
	if math.Abs(a.StrengthDriftPerYear-(-1)) > 0.01 {
		t.Errorf("strength drift = %.3f points/yr, want -1", a.StrengthDriftPerYear)
	}
	// Textbook cask: nothing to flag.
	for _, f := range a.Findings {
		if f.Severity > SeverityInfo {
			t.Errorf("unexpected finding on a textbook cask: %s", f.Title)
		}
	}
}

func TestLeakingCaskIsFlagged(t *testing.T) {
	fill := Snapshot{VolumeL: 200, StrengthPct: 63, LAA: 126}
	// 8 % a year — well past double the 2–3 % band.
	end := 200 * math.Pow(0.92, 2)
	now := Snapshot{VolumeL: end, StrengthPct: 61, LAA: end * 0.61}
	a := Assess(fill, now, 2*year, ClimateCoolHumid, "")
	if !hasCode(a.Findings, "loss_far_above_band") {
		t.Errorf("8 %%/yr should be flagged as a probable leak; findings: %+v", a.Findings)
	}
}

func TestSuspiciouslyTightCaskIsQueried(t *testing.T) {
	fill := Snapshot{VolumeL: 200, StrengthPct: 63, LAA: 126}
	// Barely moved in two years — plausible, but worth confirming the
	// regauge wasn't just a copy of the fill.
	now := Snapshot{VolumeL: 199, StrengthPct: 63, LAA: 199 * 0.63}
	a := Assess(fill, now, 2*year, ClimateCoolHumid, "")
	if !hasCode(a.Findings, "loss_below_band") {
		t.Errorf("a near-zero loss should be queried; findings: %+v", a.Findings)
	}
}

// TestDriftAgainstPositionIsFlagged — in a cool, humid warehouse the
// strength should fall. Rising strength points at a bad gauge, which is
// exactly what stage 117 was about.
func TestDriftAgainstPositionIsFlagged(t *testing.T) {
	fill := Snapshot{VolumeL: 200, StrengthPct: 60, LAA: 120}
	end := 200 * math.Pow(0.975, 2)
	now := Snapshot{VolumeL: end, StrengthPct: 64, LAA: end * 0.64}
	a := Assess(fill, now, 2*year, ClimateCoolHumid, "")
	if !hasCode(a.Findings, "drift_wrong_direction") {
		t.Errorf("strength rising in a cool warehouse should be flagged; findings: %+v", a.Findings)
	}
}

// TestHighShelfReversesExpectations — the whole reason to record a level.
func TestHighShelfReversesExpectations(t *testing.T) {
	low, okLow := ClimateForLevel(ClimateCoolHumid, "2")
	if !okLow || low != ClimateCoolHumid {
		t.Errorf("level 2 should stay cool/humid, got %v (parsed %v)", low, okLow)
	}
	high, okHigh := ClimateForLevel(ClimateCoolHumid, "8")
	if !okHigh || high != ClimateHotDry {
		t.Errorf("level 8 should read as hot/dry, got %v (parsed %v)", high, okHigh)
	}
	// Rising strength high in the stack is normal, not a fault.
	fill := Snapshot{VolumeL: 200, StrengthPct: 60, LAA: 120}
	end := 200 * math.Pow(0.955, 2)
	now := Snapshot{VolumeL: end, StrengthPct: 64, LAA: end * 0.64}
	a := Assess(fill, now, 2*year, ClimateCoolHumid, "level 8")
	if hasCode(a.Findings, "drift_wrong_direction") {
		t.Errorf("rising strength on a high shelf is expected, not a finding: %+v", a.Findings)
	}
	if !a.ClimateFromLevel {
		t.Error("the level should have been used")
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"3", 3, true}, {"L3", 3, true}, {"level 7", 7, true},
		{"row 2 / lvl 5", 2, true}, // first number wins; documented behaviour
		{"", 0, false}, {"top", 0, false}, {"0", 0, false},
	} {
		got, ok := ParseLevel(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("ParseLevel(%q) = %d,%v want %d,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestRefusesToAnnualiseAFortnight — the guard that stops the package
// producing a confident, ridiculous number.
func TestRefusesToAnnualiseAFortnight(t *testing.T) {
	fill := Snapshot{VolumeL: 200, StrengthPct: 63, LAA: 126}
	now := Snapshot{VolumeL: 199, StrengthPct: 63, LAA: 125.4}
	a := Assess(fill, now, 14, ClimateCoolHumid, "")
	if a.Measurable {
		t.Error("14 days is not enough history to annualise")
	}
	if a.WhyNot == "" {
		t.Error("a refusal must say why")
	}
}

func TestRefusesImpossibleGain(t *testing.T) {
	fill := Snapshot{VolumeL: 200, StrengthPct: 63, LAA: 126}
	now := Snapshot{VolumeL: 210, StrengthPct: 63, LAA: 132.3}
	a := Assess(fill, now, 2*year, ClimateCoolHumid, "")
	if a.Measurable {
		t.Error("a cask cannot gain volume; that's a gauge error, not a loss rate")
	}
}

func TestLAALossTracked(t *testing.T) {
	fill := Snapshot{VolumeL: 200, StrengthPct: 63, LAA: 126}
	end := 200 * math.Pow(0.98, 3)
	now := Snapshot{VolumeL: end, StrengthPct: 60, LAA: end * 0.60}
	a := Assess(fill, now, 3*year, ClimateCoolHumid, "")
	// Alcohol leaves faster than bulk here, because the strength fell too.
	if !(a.AnnualLAALossPct > a.AnnualVolumeLossPct) {
		t.Errorf("LAA loss %.2f should exceed volume loss %.2f when strength is falling",
			a.AnnualLAALossPct, a.AnnualVolumeLossPct)
	}
}

func hasCode(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}
