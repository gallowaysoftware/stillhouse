package alcoholometry

import (
	"bufio"
	"encoding/hex"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// srcSHA256Hex pins the exact published table we test against: the
// ALC_TAB.TXT inside CRA's "Canadian Alcoholometric Tables 1980" ZIP. If
// ALC_TAB points at some other file, this fails loudly rather than
// silently changing what we file.
const srcSHA256Hex = "5c1ca869418bd60920c46fdde7462ab16eb2c24424da15b7803dc587f4676ace"

func TestSourceProvenance(t *testing.T) {
	requireTables(t)
	sum, err := SourceSHA256()
	if err != nil {
		t.Fatalf("SourceSHA256: %v", err)
	}
	if got := hex.EncodeToString(sum[:]); got != srcSHA256Hex {
		t.Errorf("loaded %s, which is not the published CRA table\n got %s\nwant %s",
			SourceName(), got, srcSHA256Hex)
	}
}

// TestCRAWorkedExamples replays the four examples printed in the CRA
// publication under "EXAMPLES OF PRACTICAL USE". These are the closest
// thing to a conformance suite the tables have.
func TestCRAWorkedExamples(t *testing.T) {
	requireTables(t)
	tests := []struct {
		name                string
		tempC, density      float64
		wantA, wantB, wantC float64
	}{
		// Mass/Density Procedure, Example 1.
		{"mass/density 1", 20, 922.6, 1.0851, 53.7, 1.0000},
		// Mass/Density Procedure, Example 2.
		{"mass/density 2", 10, 937.4, 1.0762, 50.0, 1.0080},
		// Volume/Density Procedure, Example 1.
		{"volume/density 1", 20, 905.8, 1.1053, 61.5, 1.0000},
		// Volume/Density Procedure, Example 2.
		{"volume/density 2", 30, 897.4, 1.1058, 61.7, 0.9909},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Lookup(tc.tempC, tc.density)
			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			if r.LitresPerKg != tc.wantA {
				t.Errorf("A = %v, want %v", r.LitresPerKg, tc.wantA)
			}
			if r.StrengthPct != tc.wantB {
				t.Errorf("B = %v, want %v", r.StrengthPct, tc.wantB)
			}
			if r.VolumeFactor != tc.wantC {
				t.Errorf("C = %v, want %v", r.VolumeFactor, tc.wantC)
			}
		})
	}
}

// TestCRAVolumeDensityEndToEnd reproduces the full arithmetic CRA prints for
// Volume/Density Example 2, through AbsoluteAlcohol.
func TestCRAVolumeDensityEndToEnd(t *testing.T) {
	requireTables(t)
	laa, vol20, r, err := AbsoluteAlcohol(30, 897.4, 21643.0)
	if err != nil {
		t.Fatalf("AbsoluteAlcohol: %v", err)
	}
	// CRA: 21 643.0 L x 0.9909 = 21 446.0487 L at 20 C
	if math.Abs(vol20-21446.0487) > 1e-4 {
		t.Errorf("volume at 20 C = %.4f, want 21446.0487", vol20)
	}
	// CRA: 21 446.0487 x 61.7% = 13 232.212 L of absolute ethyl alcohol
	if math.Abs(laa-13232.212) > 1e-3 {
		t.Errorf("LAA = %.4f, want ~13232.212", laa)
	}
	if r.StrengthPct != 61.7 {
		t.Errorf("B = %v, want 61.7", r.StrengthPct)
	}
}

// TestReferenceAnchors checks the two ends of the 20 C row. CRA states the
// density of absolute ethyl alcohol as 789.239 1233 kg/m3 at 20 C, which
// lands in the 789.4 bucket at the published 0.2 kg/m3 resolution.
func TestReferenceAnchors(t *testing.T) {
	requireTables(t)
	if r, err := Lookup(20, 789.4); err != nil || r.StrengthPct != 100.0 {
		t.Errorf("789.4 @ 20 C = %v (err %v), want strength 100.0", r.StrengthPct, err)
	}
	if r, err := Lookup(20, 998.2); err != nil || r.StrengthPct != 0.0 {
		t.Errorf("998.2 @ 20 C = %v (err %v), want strength 0.0", r.StrengthPct, err)
	}
}

// TestVolumeFactorIdentityAt20C — the whole point of the reference
// temperature is that no volume correction applies there.
func TestVolumeFactorIdentityAt20C(t *testing.T) {
	requireTables(t)
	for d := 790.0; d <= 998.0; d += 0.2 {
		r, err := Lookup(20, d)
		if err != nil {
			continue
		}
		if r.VolumeFactor != 1.0 {
			t.Fatalf("C at 20 C, density %.1f = %v, want 1.0", d, r.VolumeFactor)
			return
		}
	}
}

// TestWarmSpiritOverstatesBothWays is the finding this package exists to
// close: an uncorrected warm reading overstates strength AND volume, so
// LAA computed the naive way is too high.
func TestWarmSpiritOverstatesBothWays(t *testing.T) {
	requireTables(t)
	const density, warm = 930.0, 28.0
	warmR, err := Lookup(warm, density)
	if err != nil {
		t.Fatalf("Lookup warm: %v", err)
	}
	coldR, err := Lookup(20, density)
	if err != nil {
		t.Fatalf("Lookup at reference: %v", err)
	}
	// Same hydrometer indication, read warm, is genuinely weaker spirit.
	if !(warmR.StrengthPct < coldR.StrengthPct) {
		t.Errorf("strength at %.0f C (%v) should be below strength at 20 C (%v)",
			warm, warmR.StrengthPct, coldR.StrengthPct)
	}
	// And its volume shrinks on the way back to 20 C.
	if !(warmR.VolumeFactor < 1.0) {
		t.Errorf("C at %.0f C = %v, want < 1.0", warm, warmR.VolumeFactor)
	}

	naiveLAA := 1000 * coldR.StrengthPct / 100
	trueLAA, _, _, err := AbsoluteAlcohol(warm, density, 1000)
	if err != nil {
		t.Fatalf("AbsoluteAlcohol: %v", err)
	}
	if !(trueLAA < naiveLAA) {
		t.Errorf("corrected LAA %.3f should be below uncorrected %.3f", trueLAA, naiveLAA)
	}
	t.Logf("1000 L at %.0f C, hydrometer %.1f: uncorrected %.2f L LAA, corrected %.2f L LAA (%.2f L overstated)",
		warm, density, naiveLAA, trueLAA, naiveLAA-trueLAA)
}

func TestLookupByStrengthRoundTrip(t *testing.T) {
	requireTables(t)
	for _, tc := range []struct{ tempC, density float64 }{
		{20, 922.6}, {10, 937.4}, {30, 897.4}, {5, 950.0}, {35, 860.0},
	} {
		direct, err := Lookup(tc.tempC, tc.density)
		if err != nil {
			t.Fatalf("Lookup(%v, %v): %v", tc.tempC, tc.density, err)
		}
		back, err := LookupByStrength(tc.tempC, direct.StrengthPct)
		if err != nil {
			t.Fatalf("LookupByStrength: %v", err)
		}
		if math.Abs(back.VolumeFactor-direct.VolumeFactor) > 1e-3 {
			t.Errorf("at %.1f C / %.1f: C round-trip %v vs %v",
				tc.tempC, tc.density, back.VolumeFactor, direct.VolumeFactor)
		}
		if math.Abs(back.LitresPerKg-direct.LitresPerKg) > 1e-3 {
			t.Errorf("at %.1f C / %.1f: A round-trip %v vs %v",
				tc.tempC, tc.density, back.LitresPerKg, direct.LitresPerKg)
		}
	}
}

func TestInterpolationBetweenGridPoints(t *testing.T) {
	requireTables(t)
	lo, err := Lookup(20, 922.6)
	if err != nil {
		t.Fatal(err)
	}
	hi, err := Lookup(20, 922.8)
	if err != nil {
		t.Fatal(err)
	}
	mid, err := Lookup(20, 922.7)
	if err != nil {
		t.Fatal(err)
	}
	want := (lo.StrengthPct + hi.StrengthPct) / 2
	if math.Abs(mid.StrengthPct-want) > 1e-9 {
		t.Errorf("midpoint strength %v, want %v", mid.StrengthPct, want)
	}
	// Same across the temperature axis.
	a, _ := Lookup(20.0, 930.0)
	b, _ := Lookup(20.5, 930.0)
	m, _ := Lookup(20.25, 930.0)
	if math.Abs(m.StrengthPct-(a.StrengthPct+b.StrengthPct)/2) > 1e-9 {
		t.Errorf("temperature midpoint %v, want %v", m.StrengthPct, (a.StrengthPct+b.StrengthPct)/2)
	}
}

func TestOutOfRange(t *testing.T) {
	requireTables(t)
	if _, err := Lookup(45, 900); err == nil {
		t.Error("temperature above +40 C should be rejected")
	}
	if _, err := Lookup(-25, 900); err == nil {
		t.Error("temperature below -20 C should be rejected")
	}
	if _, err := Lookup(20, 700); err == nil {
		t.Error("density below the table should be rejected")
	}
	if _, err := Lookup(20, 1100); err == nil {
		t.Error("density above the table should be rejected")
	}
	var re *RangeError
	_, err := Lookup(45, 900)
	if !asRangeError(err, &re) {
		t.Fatalf("want *RangeError, got %T", err)
	}
	if re.What != "temperature" {
		t.Errorf("RangeError.What = %q, want temperature", re.What)
	}
}

func asRangeError(err error, target **RangeError) bool {
	re, ok := err.(*RangeError)
	if ok {
		*target = re
	}
	return ok
}

// TestAgainstFullSourceTable replays every row of the published ASCII table
// back through Lookup — the grid the loader built must reproduce the file
// it was built from, exactly, at every one of its ~800k rows.
//
//	ALC_TAB=/path/to/ALC_TAB.TXT go test ./internal/alcoholometry/
func TestAgainstFullSourceTable(t *testing.T) {
	requireTables(t)
	raw, _, err := readSource(os.Getenv(alcTabEnv))
	if err != nil {
		t.Fatalf("read source: %v", err)
	}

	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	rows, mismatches := 0, 0
	for sc.Scan() {
		fields := strings.Fields(strings.TrimRight(sc.Text(), "\r"))
		if len(fields) != 5 {
			continue
		}
		vals := make([]float64, 5)
		ok := true
		for i, s := range fields {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				ok = false
				break
			}
			vals[i] = v
		}
		if !ok {
			continue
		}
		rows++
		r, err := Lookup(vals[0], vals[1])
		if err != nil {
			t.Errorf("row %.1f/%.1f: %v", vals[0], vals[1], err)
			mismatches++
			continue
		}
		if r.LitresPerKg != vals[2] || r.StrengthPct != vals[3] || r.VolumeFactor != vals[4] {
			if mismatches < 10 {
				t.Errorf("row %.1f/%.1f: got A=%v B=%v C=%v, want A=%v B=%v C=%v",
					vals[0], vals[1], r.LitresPerKg, r.StrengthPct, r.VolumeFactor,
					vals[2], vals[3], vals[4])
			}
			mismatches++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rows == 0 {
		t.Fatal("no rows parsed from source")
	}
	if mismatches > 0 {
		t.Errorf("%d of %d rows disagree with the published table", mismatches, rows)
	}
	t.Logf("verified %d published rows", rows)
}
