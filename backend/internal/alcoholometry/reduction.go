package alcoholometry

import (
	"errors"
	"fmt"
)

// WaterStrengthPct is the strength of the demineralised water used to
// reduce spirit — zero, by definition. Named so the mass balance below
// reads as what it is.
const WaterStrengthPct = 0.0

// Reduction is a plan for proofing spirit down to a target strength.
//
// # Why this isn't arithmetic
//
// The obvious calculation conserves alcohol and subtracts:
//
//	final volume = start volume × start strength ÷ target strength
//	water        = final volume − start volume
//
// That final volume is right. The water figure is not. Ethanol and water
// hydrogen-bond on mixing and the blend occupies less volume than its
// parts did separately — one to two percent, which on a 1,000 L reduction
// is tens of litres. The curriculum's own guidance is to "add a small
// excess of water and then verify the final strength", which is sound
// practice and a poor number.
//
// Because the CRA tables carry density (column A, litres per kilogram),
// the contraction doesn't have to be guessed. Run the balance in mass,
// where nothing contracts, and convert back:
//
//	spirit mass = start volume ÷ A(start strength)
//	final mass  = final volume ÷ A(target strength)
//	water mass  = final mass − spirit mass
//	water volume = water mass × A(0 %)
//
// All volumes are at 20 °C, the reference the strengths are defined at.
//
// # Doing it by weight instead
//
// The mass figures below aren't a convenience — they're the better way to
// run the job. Mass is strictly additive: put 590 kg of water on top of
// 901 kg of spirit and you have 1,491 kg, with no contraction to correct
// for, no thermal expansion to compensate, and a scale that reads finer
// than a sight glass. The volume contraction only ever shows up when you
// convert back to litres.
//
// Masses follow CRA's Mass/Density Procedure: column A converts kilograms
// weighed in air to litres at 20 °C, so these are apparent (in-air) masses
// — what a scale under the tank actually reads.
type Reduction struct {
	FromVolumeL  float64
	FromStrength float64
	ToStrength   float64
	// FinalVolumeL is what the vessel holds when the reduction is done.
	// Filling to this mark is how the job is actually done on the floor.
	FinalVolumeL float64
	// WaterToAddL is the water to meter in, contraction accounted for.
	WaterToAddL float64
	// NaiveWaterL is FinalVolumeL − FromVolumeL: the figure a simple
	// volume balance gives. Carried so the UI can show the difference
	// rather than just asserting a number.
	NaiveWaterL float64
	// ContractionL is WaterToAddL − NaiveWaterL — the extra water the
	// blend swallows. Always positive for an ethanol/water reduction.
	ContractionL float64
	// LAA is the absolute alcohol, unchanged by the reduction. Adding
	// water cannot create or destroy it, so this is the invariant to
	// check the plan against.
	LAA float64

	// Mass side of the same plan, in kilograms weighed in air. Adding
	// WaterToAddKg is exact — there is no contraction term, because
	// nothing contracts in mass.
	FromMassKg   float64
	FinalMassKg  float64
	WaterToAddKg float64
}

// PlanReduction computes how to bring fromVolumeL of spirit at
// fromStrengthPct down to toStrengthPct, all at 20 °C.
func PlanReduction(fromVolumeL, fromStrengthPct, toStrengthPct float64) (Reduction, error) {
	if fromVolumeL <= 0 {
		return Reduction{}, errors.New("volume must be > 0")
	}
	if fromStrengthPct <= 0 || fromStrengthPct > 100 {
		return Reduction{}, &RangeError{What: "starting strength", Value: fromStrengthPct, Min: 0, Max: 100, Unit: "%"}
	}
	if toStrengthPct <= 0 || toStrengthPct > 100 {
		return Reduction{}, &RangeError{What: "target strength", Value: toStrengthPct, Min: 0, Max: 100, Unit: "%"}
	}
	// Water dilutes. Anything else is a different operation — blending in
	// a stronger spirit — and silently "solving" it with a negative water
	// volume would be worse than refusing.
	if toStrengthPct >= fromStrengthPct {
		return Reduction{}, fmt.Errorf(
			"cannot reduce %.2f %% to %.2f %% — adding water only lowers strength",
			fromStrengthPct, toStrengthPct)
	}

	from, err := LookupByStrength(ReferenceTemperatureC, fromStrengthPct)
	if err != nil {
		return Reduction{}, err
	}
	to, err := LookupByStrength(ReferenceTemperatureC, toStrengthPct)
	if err != nil {
		return Reduction{}, err
	}
	water, err := LookupByStrength(ReferenceTemperatureC, WaterStrengthPct)
	if err != nil {
		return Reduction{}, err
	}

	laa := fromVolumeL * fromStrengthPct / 100
	finalVolume := laa / (toStrengthPct / 100)

	spiritMass := fromVolumeL / from.LitresPerKg
	finalMass := finalVolume / to.LitresPerKg
	waterVolume := (finalMass - spiritMass) * water.LitresPerKg

	naive := finalVolume - fromVolumeL
	return Reduction{
		FromVolumeL:  fromVolumeL,
		FromStrength: fromStrengthPct,
		ToStrength:   toStrengthPct,
		FinalVolumeL: finalVolume,
		WaterToAddL:  waterVolume,
		NaiveWaterL:  naive,
		ContractionL: waterVolume - naive,
		LAA:          laa,
		FromMassKg:   spiritMass,
		FinalMassKg:  finalMass,
		WaterToAddKg: finalMass - spiritMass,
	}, nil
}

// PlanReductionFromMass is PlanReduction for a charge that was weighed
// rather than dipped — a scale-tank indication in kilograms.
func PlanReductionFromMass(fromMassKg, fromStrengthPct, toStrengthPct float64) (Reduction, error) {
	if fromMassKg <= 0 {
		return Reduction{}, errors.New("mass must be > 0")
	}
	if fromStrengthPct <= 0 || fromStrengthPct > 100 {
		return Reduction{}, &RangeError{What: "starting strength", Value: fromStrengthPct, Min: 0, Max: 100, Unit: "%"}
	}
	volumeL, err := VolumeFromMass(fromStrengthPct, fromMassKg)
	if err != nil {
		return Reduction{}, err
	}
	return PlanReduction(volumeL, fromStrengthPct, toStrengthPct)
}

// VolumeFromMass converts a scale reading to litres of spirits at 20 °C,
// per CRA's Mass/Density Procedure: litres = kilograms × A.
func VolumeFromMass(strengthPct, massKg float64) (float64, error) {
	r, err := LookupByStrength(ReferenceTemperatureC, strengthPct)
	if err != nil {
		return 0, err
	}
	return massKg * r.LitresPerKg, nil
}

// MassFromVolume is the inverse: what a tank of this spirit weighs.
func MassFromVolume(strengthPct, volumeL float64) (float64, error) {
	r, err := LookupByStrength(ReferenceTemperatureC, strengthPct)
	if err != nil {
		return 0, err
	}
	return volumeL / r.LitresPerKg, nil
}

// ReferenceTemperatureC is the temperature every strength in this package
// is expressed at.
const ReferenceTemperatureC = 20.0
