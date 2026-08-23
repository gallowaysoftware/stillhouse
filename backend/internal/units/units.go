// Package units distinguishes a fraction from a percentage in the type
// system, because Stillhouse holds both and they are a hundredfold
// apart.
//
// The problem this exists for is narrow and real. `abv_pct` is 0–100.
// `extract_pct`, `moisture_pct` and the three recipe efficiencies are
// fractions in [0,1]. Same suffix, same product. Worse,
// MashEfficiency.Pct (a percentage) sits beside
// RecipeVersion.MashEfficiencyFraction (a fraction) — the same concept at two
// scales, three letters apart.
//
// Range validation catches one direction and only one. A recipe
// efficiency of 78 is obviously a percentage in a fraction's slot, and
// gets rejected. An ABV of 0.40 is a legal percentage — it is a very
// weak beer — so nothing rejects it, and that is the direction that
// understates duty: forty percent read as four tenths of one percent
// prices a bottle at a hundredth of what it owes.
//
// A named type cannot be got wrong the same way. Fraction and Percent
// are distinct types with no implicit conversion between them, so
// passing one where the other belongs does not compile, and converting
// is a call somebody has to write and a reader can see.
package units

import "fmt"

// Fraction is a proportion in [0,1]. Extract, moisture, and the three
// recipe efficiencies are all fractions.
type Fraction float64

// Percent is a proportion in [0,100]. Strength (ABV) is a percent, and
// so is anything a person would say aloud with the word "percent".
type Percent float64

// AsPercent converts explicitly. Written out at the call site so a
// reader can see the hundredfold happen.
func (f Fraction) AsPercent() Percent { return Percent(f * 100) }

// AsFraction converts explicitly, the other way.
func (p Percent) AsFraction() Fraction { return Fraction(p / 100) }

// Float returns the underlying number for arithmetic with plain floats —
// masses, volumes, litres of absolute alcohol. Deliberately a method
// rather than an implicit conversion: it is the one place the type
// stops protecting you, and it should be visible.
func (f Fraction) Float() float64 { return float64(f) }

// Float returns the underlying number.
func (p Percent) Float() float64 { return float64(p) }

// Valid reports whether the fraction is in range. Zero is valid — a
// material with no fermentable extract is a real thing — but negative
// and above one are not.
func (f Fraction) Valid() bool { return f >= 0 && f <= 1 }

// Valid reports whether the percentage is in range.
func (p Percent) Valid() bool { return p >= 0 && p <= 100 }

// ValidateFraction returns an error naming the field and, where the
// value looks like a percentage that landed in a fraction's slot, says
// so — because that is the mistake somebody actually made.
func ValidateFraction(name string, f Fraction) error {
	if f.Valid() {
		return nil
	}
	if f > 1 && f <= 100 {
		return fmt.Errorf("%s is %g, which is out of range for a fraction — "+
			"if you meant %g%%, write %g", name, float64(f), float64(f), float64(f)/100)
	}
	return fmt.Errorf("%s must be between 0 and 1; got %g", name, float64(f))
}

// ValidatePercent is the mirror. It cannot catch the dangerous
// direction — 0.40 is a legal percentage and there is no way to know it
// was meant as 40% — which is exactly why the types matter more than the
// validators.
func ValidatePercent(name string, p Percent) error {
	if p.Valid() {
		return nil
	}
	return fmt.Errorf("%s must be between 0 and 100; got %g", name, float64(p))
}
