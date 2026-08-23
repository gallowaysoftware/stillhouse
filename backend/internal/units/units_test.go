package units

import "testing"

// The two types must not be interchangeable. This is a compile-time
// property, so what is tested here is the arithmetic of the explicit
// conversions and the range rules — the type distinction itself is
// enforced by the compiler and would be a build failure, not a test
// failure.
func TestConversionsAreExplicitAndCorrect(t *testing.T) {
	if got := Fraction(0.4).AsPercent(); got != 40 {
		t.Errorf("Fraction(0.4).AsPercent() = %v, want 40", got)
	}
	if got := Percent(40).AsFraction(); got != 0.4 {
		t.Errorf("Percent(40).AsFraction() = %v, want 0.4", got)
	}
	// Round-tripping must not drift, because these figures end up
	// multiplied by volumes to produce litres of absolute alcohol.
	for _, p := range []Percent{0, 0.5, 40, 62.5, 94.8, 100} {
		if got := p.AsFraction().AsPercent(); got != p {
			t.Errorf("%v round-tripped to %v", p, got)
		}
	}
}

func TestFractionRangeNamesTheLikelyMistake(t *testing.T) {
	// The mistake somebody actually makes is typing a percentage into a
	// fraction's slot, so the message should say what to write instead
	// rather than restating the range.
	err := ValidateFraction("mash efficiency", Fraction(78))
	if err == nil {
		t.Fatal("78 was accepted as a fraction")
	}
	if !contains(err.Error(), "0.78") {
		t.Errorf("error %q does not suggest the value they probably meant", err)
	}

	for _, f := range []Fraction{0, 0.5, 1} {
		if err := ValidateFraction("x", f); err != nil {
			t.Errorf("ValidateFraction(%v) rejected a valid fraction: %v", f, err)
		}
	}
	if ValidateFraction("x", Fraction(-0.1)) == nil {
		t.Error("a negative fraction was accepted")
	}
}

// The asymmetry is the whole point and is worth pinning: a percentage
// validator cannot catch the dangerous direction, because 0.40 is a
// legal percentage. Nothing here can fix that — only the type can.
func TestPercentValidatorCannotCatchTheDangerousDirection(t *testing.T) {
	if err := ValidatePercent("abv", Percent(0.4)); err != nil {
		t.Errorf("0.4 was rejected as a percentage: %v — it is a legal, if weak, "+
			"strength, which is why the type distinction matters more than the range check", err)
	}
	if ValidatePercent("abv", Percent(101)) == nil {
		t.Error("101%% was accepted")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
