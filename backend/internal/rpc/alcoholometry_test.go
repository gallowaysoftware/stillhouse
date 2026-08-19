package rpc

import (
	"errors"
	"math"
	"testing"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// TestResolveStrengthPaths pins the three ways a strength can reach the
// ledger. The distinction matters on a B266: only the density path is a
// determination against the published tables.
func TestResolveStrengthPaths(t *testing.T) {
	t.Run("density and temperature resolve through the tables", func(t *testing.T) {
		// CRA Volume/Density Example 2: 897.4 kg/m³ at 30 °C is 61.7 % with
		// a volume factor of 0.9909.
		got, err := resolveStrength(strengthInput{
			ObservedVolumeL: 21643.0,
			DensityKgM3:     897.4,
			DensityIsSet:    true,
			TemperatureC:    30,
			TemperatureSet:  true,
		})
		if err != nil {
			t.Fatalf("resolveStrength: %v", err)
		}
		if got.Source != stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_DENSITY {
			t.Errorf("source = %v, want TABLE_DENSITY", got.Source)
		}
		if got.StrengthPct20C != 61.7 {
			t.Errorf("strength = %v, want 61.7", got.StrengthPct20C)
		}
		if got.VolumeFactorC != 0.9909 {
			t.Errorf("C = %v, want 0.9909", got.VolumeFactorC)
		}
		if math.Abs(got.VolumeL20C-21446.0487) > 1e-4 {
			t.Errorf("volume at 20 °C = %.4f, want 21446.0487", got.VolumeL20C)
		}
		if math.Abs(got.LAA()-13232.212) > 1e-3 {
			t.Errorf("LAA = %.4f, want ~13232.212", got.LAA())
		}
	})

	t.Run("a hydrometer indication without a temperature is refused", func(t *testing.T) {
		_, err := resolveStrength(strengthInput{
			ObservedVolumeL: 100,
			DensityKgM3:     920.0,
			DensityIsSet:    true,
		})
		if err == nil {
			t.Fatal("want an error — a density reading is meaningless without its temperature")
		}
		// It must reach the operator as a bad-input error, not as an
		// opaque internal one.
		var connectErr *connect.Error
		if !errors.As(alcoholometryError(err), &connectErr) {
			t.Fatalf("want a *connect.Error, got %T", alcoholometryError(err))
		}
		if connectErr.Code() != connect.CodeInvalidArgument {
			t.Errorf("code = %v, want invalid_argument", connectErr.Code())
		}
	})

	t.Run("instrument-corrected strength still gets its volume corrected", func(t *testing.T) {
		got, err := resolveStrength(strengthInput{
			ObservedVolumeL: 1000,
			AbvPct:          61.7,
			TemperatureC:    30,
			TemperatureSet:  true,
		})
		if err != nil {
			t.Fatalf("resolveStrength: %v", err)
		}
		if got.Source != stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_STRENGTH {
			t.Errorf("source = %v, want TABLE_STRENGTH", got.Source)
		}
		// The caller's strength is taken as given...
		if got.StrengthPct20C != 61.7 {
			t.Errorf("strength = %v, want 61.7 unchanged", got.StrengthPct20C)
		}
		// ...but 1000 L of warm spirit is less than 1000 L at 20 °C.
		if !(got.VolumeL20C < 1000) {
			t.Errorf("volume at 20 °C = %v, want < 1000", got.VolumeL20C)
		}
		if math.Abs(got.VolumeFactorC-0.9909) > 1e-3 {
			t.Errorf("C = %v, want ~0.9909", got.VolumeFactorC)
		}
	})

	t.Run("no temperature means nothing is corrected", func(t *testing.T) {
		got, err := resolveStrength(strengthInput{ObservedVolumeL: 1000, AbvPct: 62.0})
		if err != nil {
			t.Fatalf("resolveStrength: %v", err)
		}
		if got.Source != stillhousev1.StrengthSource_STRENGTH_SOURCE_UNCORRECTED {
			t.Errorf("source = %v, want UNCORRECTED", got.Source)
		}
		if got.StrengthPct20C != 62.0 || got.VolumeL20C != 1000 || got.VolumeFactorC != 1.0 {
			t.Errorf("uncorrected path altered the reading: %+v", got)
		}
	})

	t.Run("at the reference temperature the correction is a no-op", func(t *testing.T) {
		got, err := resolveStrength(strengthInput{
			ObservedVolumeL: 500,
			AbvPct:          53.7,
			TemperatureC:    ReferenceTemperatureC,
			TemperatureSet:  true,
		})
		if err != nil {
			t.Fatalf("resolveStrength: %v", err)
		}
		if got.VolumeFactorC != 1.0 || got.VolumeL20C != 500 {
			t.Errorf("at 20 °C nothing should move: %+v", got)
		}
	})

	t.Run("an out-of-range reading is rejected", func(t *testing.T) {
		if _, err := resolveStrength(strengthInput{
			ObservedVolumeL: 100, DensityKgM3: 500, DensityIsSet: true,
			TemperatureC: 20, TemperatureSet: true,
		}); err == nil {
			t.Error("density below the tables should be refused")
		}
		if _, err := resolveStrength(strengthInput{
			ObservedVolumeL: 100, AbvPct: 40,
			TemperatureC: 60, TemperatureSet: true,
		}); err == nil {
			t.Error("temperature above +40 °C should be refused")
		}
	})
}

// TestWarmGaugeOverstatesDuty is the regression this stage exists to
// prevent: filing a warm gauge as if it were at 20 °C inflates LAA, and
// LAA is what excise duty is charged on.
func TestWarmGaugeOverstatesDuty(t *testing.T) {
	const observedVolume, density, tankTemp = 1000.0, 930.0, 28.0

	corrected, err := resolveStrength(strengthInput{
		ObservedVolumeL: observedVolume,
		DensityKgM3:     density,
		DensityIsSet:    true,
		TemperatureC:    tankTemp,
		TemperatureSet:  true,
	})
	if err != nil {
		t.Fatalf("resolveStrength: %v", err)
	}
	// What the old code would have stored: the hydrometer indication read
	// off the 20 °C scale, applied to the uncorrected volume.
	naive, err := resolveStrength(strengthInput{
		ObservedVolumeL: observedVolume,
		DensityKgM3:     density,
		DensityIsSet:    true,
		TemperatureC:    ReferenceTemperatureC,
		TemperatureSet:  true,
	})
	if err != nil {
		t.Fatalf("resolveStrength: %v", err)
	}
	if !(corrected.LAA() < naive.LAA()) {
		t.Fatalf("corrected LAA %.4f should be below uncorrected %.4f", corrected.LAA(), naive.LAA())
	}
	t.Logf("%.0f L gauged at %.0f °C, hydrometer %.1f: %.2f L LAA corrected vs %.2f L uncorrected (%.2f L overstated)",
		observedVolume, tankTemp, density, corrected.LAA(), naive.LAA(), naive.LAA()-corrected.LAA())
}

func TestStrengthSourceRoundTrip(t *testing.T) {
	for _, s := range []stillhousev1.StrengthSource{
		stillhousev1.StrengthSource_STRENGTH_SOURCE_UNCORRECTED,
		stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_DENSITY,
		stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_STRENGTH,
	} {
		if got := strengthSourceToProto(strengthSourceToDB(s)); got != s {
			t.Errorf("round trip %v -> %v", s, got)
		}
	}
	// An unset source must never be mistaken for a determination.
	if got := strengthSourceToDB(stillhousev1.StrengthSource_STRENGTH_SOURCE_UNSPECIFIED); got != "uncorrected" {
		t.Errorf("UNSPECIFIED mapped to %q, want uncorrected", got)
	}
}
