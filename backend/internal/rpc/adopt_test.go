package rpc

import (
	"errors"
	"math"
	"testing"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// TestAdoptByWeightMatchesCRAProcedure replays the worked example printed
// in the CRA publication under "Mass/Density Procedure", Example 1:
//
//	Scale tank indication  20 135 kg
//	Hydrometer indication  922.6
//	Temperature            20 °C
//	A = 1.0851, B = 53.7 %
//	20 135 × 1.0851       = 21 848.4885 L of spirits at 20 °C
//	21 848.4885 × 53.7 %  = 11 732.638 L of absolute alcohol
//
// This is the calculation a distillery adopting Stillhouse actually
// performs on day one, so it is worth pinning to the published example
// rather than to our own arithmetic.
func TestAdoptByWeightMatchesCRAProcedure(t *testing.T) {
	got, err := resolveAdoptedStock(&stillhousev1.AdoptOpeningInventoryRequest{
		MassKg:          20135,
		MassKgSet:       true,
		DensityKgM3:     922.6,
		DensityKgM3Set:  true,
		TemperatureC:    20,
		TemperatureCSet: true,
	})
	if err != nil {
		t.Fatalf("resolveAdoptedStock: %v", err)
	}
	if got.StrengthPct20C != 53.7 {
		t.Errorf("strength = %v, want 53.7", got.StrengthPct20C)
	}
	if math.Abs(got.VolumeL20C-21848.4885) > 1e-3 {
		t.Errorf("volume at 20 °C = %.4f, want 21848.4885", got.VolumeL20C)
	}
	if math.Abs(got.LAA()-11732.638) > 1e-2 {
		t.Errorf("LAA = %.4f, want ~11732.638", got.LAA())
	}
	if got.Source != stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_DENSITY {
		t.Errorf("source = %v, want TABLE_DENSITY", got.Source)
	}
}

// TestAdoptByWeightAtOtherTemperatures — CRA's Example 2 of the same
// procedure, which is read warm. A scale reading needs no volume
// correction of its own, but the strength still comes off the tables at
// the temperature the hydrometer was read at.
func TestAdoptByWeightAtOtherTemperatures(t *testing.T) {
	got, err := resolveAdoptedStock(&stillhousev1.AdoptOpeningInventoryRequest{
		MassKg:          23876,
		MassKgSet:       true,
		DensityKgM3:     937.4,
		DensityKgM3Set:  true,
		TemperatureC:    10,
		TemperatureCSet: true,
	})
	if err != nil {
		t.Fatalf("resolveAdoptedStock: %v", err)
	}
	if got.StrengthPct20C != 50.0 {
		t.Errorf("strength = %v, want 50.0", got.StrengthPct20C)
	}
	// CRA: 23 876 × 1.0762 = 25 695.3512 L at 20 °C
	if math.Abs(got.VolumeL20C-25695.3512) > 1e-3 {
		t.Errorf("volume at 20 °C = %.4f, want 25695.3512", got.VolumeL20C)
	}
	// CRA: × 50 % = 12 847.676 L of absolute alcohol
	if math.Abs(got.LAA()-12847.676) > 1e-2 {
		t.Errorf("LAA = %.4f, want ~12847.676", got.LAA())
	}
	// The mass route applies no volume factor — column A lands on 20 °C
	// litres directly.
	if got.VolumeFactorC != 1 {
		t.Errorf("volume factor = %v; the mass route must not apply one", got.VolumeFactorC)
	}
}

func TestAdoptByDippedVolume(t *testing.T) {
	got, err := resolveAdoptedStock(&stillhousev1.AdoptOpeningInventoryRequest{
		VolumeL:         21643,
		VolumeLSet:      true,
		DensityKgM3:     897.4,
		DensityKgM3Set:  true,
		TemperatureC:    30,
		TemperatureCSet: true,
	})
	if err != nil {
		t.Fatalf("resolveAdoptedStock: %v", err)
	}
	// The volume route DOES correct: 21 643 × 0.9909 = 21 446.0487 L.
	if math.Abs(got.VolumeL20C-21446.0487) > 1e-3 {
		t.Errorf("volume at 20 °C = %.4f, want 21446.0487", got.VolumeL20C)
	}
	if got.StrengthPct20C != 61.7 {
		t.Errorf("strength = %v, want 61.7", got.StrengthPct20C)
	}
}

// TestAdoptWithoutAScaleStillWorks — a distiller with a dipstick and an
// alcoholometer that reads corrected strength can still adopt.
func TestAdoptWithoutAScaleStillWorks(t *testing.T) {
	got, err := resolveAdoptedStock(&stillhousev1.AdoptOpeningInventoryRequest{
		VolumeL:    200,
		VolumeLSet: true,
		AbvPct:     62.5,
	})
	if err != nil {
		t.Fatalf("resolveAdoptedStock: %v", err)
	}
	if got.StrengthPct20C != 62.5 || got.VolumeL20C != 200 {
		t.Errorf("got %+v, want the figures through unchanged", got)
	}
	// And it is honestly marked as uncorrected.
	if got.Source != stillhousev1.StrengthSource_STRENGTH_SOURCE_UNCORRECTED {
		t.Errorf("source = %v, want UNCORRECTED", got.Source)
	}
}

func TestAdoptRefusesIncompleteMeasurements(t *testing.T) {
	for name, req := range map[string]*stillhousev1.AdoptOpeningInventoryRequest{
		"nothing at all": {},
		"mass with no hydrometer": {
			MassKg: 100, MassKgSet: true, TemperatureC: 20, TemperatureCSet: true,
		},
		"mass with no temperature": {
			MassKg: 100, MassKgSet: true, DensityKgM3: 920, DensityKgM3Set: true,
		},
		"both mass and volume": {
			MassKg: 100, MassKgSet: true, VolumeL: 100, VolumeLSet: true,
			DensityKgM3: 920, DensityKgM3Set: true, TemperatureC: 20, TemperatureCSet: true,
		},
		"density out of range": {
			MassKg: 100, MassKgSet: true, DensityKgM3: 500, DensityKgM3Set: true,
			TemperatureC: 20, TemperatureCSet: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveAdoptedStock(req); err == nil {
				t.Error("want an error rather than a silently wrong balance")
			}
		})
	}
}

func TestAdoptMissingTemperatureIsTyped(t *testing.T) {
	_, err := resolveAdoptedStock(&stillhousev1.AdoptOpeningInventoryRequest{
		MassKg: 100, MassKgSet: true, DensityKgM3: 920, DensityKgM3Set: true,
	})
	if !errors.Is(err, errMissingTemperature) {
		t.Errorf("got %v, want errMissingTemperature so it surfaces as invalid_argument", err)
	}
}
