package money

import (
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// The case the package exists for: sixty bottles at $34.95. Through a
// float64 this is 2096.9999999999995.
func TestSixtyBottlesAtThirtyFourNinetyFive(t *testing.T) {
	got := MustParse("60").Mul(MustParse("34.95"))
	if want := "2097.00"; got.String(2) != want {
		t.Errorf("60 × 34.95 = %s, want %s", got.String(2), want)
	}
	// And the float route, so the comparison is on the record rather than
	// asserted in a comment.
	if f := 60 * 34.95; f == 2097.0 {
		t.Log("this platform's float64 happens to land on 2097 for this one")
	}
}

func TestTaxOnATotal(t *testing.T) {
	subtotal := MustParse("2097.00")
	hst := subtotal.Mul(MustParse("0.13"))
	if got, want := hst.String(2), "272.61"; got != want {
		t.Errorf("13%% of 2097 = %s, want %s", got, want)
	}
	if got, want := subtotal.Add(hst).String(2), "2369.61"; got != want {
		t.Errorf("total = %s, want %s", got, want)
	}
}

// Half away from zero, not banker's: it is what an invoice reader
// expects, and what the systems a licensee reconciles against do.
func TestRoundingIsHalfAwayFromZero(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1.005", "1.01"},
		{"1.015", "1.02"},
		{"2.675", "2.68"},
		{"-1.005", "-1.01"},
		{"-2.675", "-2.68"},
		{"0.004", "0.00"},
		{"0.005", "0.01"},
		{"-0.005", "-0.01"},
	} {
		if got := MustParse(tc.in).String(2); got != tc.want {
			t.Errorf("round(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
	// The distinction that makes this package worth having: 1.005 as a
	// float64 is 1.00499999999999989 and rounds DOWN. As an exact
	// decimal it is one and a half cents and rounds up.
	if got := MustParse("1.005").String(2); got != "1.01" {
		t.Errorf("exact 1.005 rounded to %s — the arithmetic went through a float", got)
	}
}

func TestParseToleratesWhatPeopleType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"$1,234.56", "1234.56"},
		{" 34.95 ", "34.95"},
		{"1234.5-", "-1234.50"},
		{"", "0.00"},
		{"-0", "0.00"},
	} {
		got, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if got.String(2) != tc.want {
			t.Errorf("Parse(%q) = %s, want %s", tc.in, got.String(2), tc.want)
		}
	}
	for _, bad := range []string{"abc", "1.2.3", "£5"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded", bad)
		}
	}
}

// The zero value has to be usable, or every struct holding an Amount
// needs a constructor.
func TestZeroValueIsUsable(t *testing.T) {
	var a Amount
	if !a.IsZero() {
		t.Error("the zero Amount is not zero")
	}
	if got := a.Add(MustParse("5")).String(2); got != "5.00" {
		t.Errorf("0 + 5 = %s", got)
	}
	if got := a.String(2); got != "0.00" {
		t.Errorf("zero renders as %s", got)
	}
}

// Round-tripping through the column type must not lose anything: it is
// how every stored amount gets read back.
func TestNumericRoundTrip(t *testing.T) {
	for _, s := range []string{"0", "34.95", "-1234.5678", "2097", "0.0001", "999999.9999"} {
		a := MustParse(s)
		n, err := a.Numeric(4)
		if err != nil {
			t.Fatalf("Numeric(%s): %v", s, err)
		}
		back := FromNumeric(n)
		if back.Cmp(a.RoundTo(4)) != 0 {
			t.Errorf("%s round-tripped to %s", s, back.String(4))
		}
	}
}

func TestFromNumericHandlesTheAwkwardOnes(t *testing.T) {
	if got := FromNumeric(pgtype.Numeric{}); !got.IsZero() {
		t.Error("an invalid Numeric should read as zero")
	}
	if got := FromNumeric(pgtype.Numeric{NaN: true, Valid: true}); !got.IsZero() {
		t.Error("NaN should read as zero rather than panicking")
	}
	// A positive exponent: 12 × 10^2.
	n := pgtype.Numeric{Int: big.NewInt(12), Exp: 2, Valid: true}
	if got := FromNumeric(n).String(2); got != "1200.00" {
		t.Errorf("12e2 = %s, want 1200.00", got)
	}
}

// A long invoice must not drift. Summing a hundred lines of a third of a
// cent is where a float total and an exact one part company.
func TestNoDriftAcrossManyLines(t *testing.T) {
	total := Zero()
	for i := 0; i < 300; i++ {
		total = total.Add(MustParse("0.1"))
	}
	if got, want := total.String(2), "30.00"; got != want {
		t.Errorf("300 × 0.10 = %s, want %s", got, want)
	}
	var f float64
	for i := 0; i < 300; i++ {
		f += 0.1
	}
	if f == 30.0 {
		t.Log("this platform's float sum happens to land on 30")
	}
}
