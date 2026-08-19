package alcoholometry

import (
	"errors"
	"fmt"
)

// BlendSource is one vessel going into a vatting.
type BlendSource struct {
	Label       string
	VolumeL     float64 // at 20 °C
	StrengthPct float64 // at 20 °C
}

// BlendPlan is the result of vatting several parcels together, and
// optionally reducing the result to a bottling strength.
//
// # Why this isn't a weighted average
//
// The strength of a blend is not the volume-weighted mean of its parts,
// for the same reason the water in a reduction isn't a subtraction:
// ethanol and water contract on mixing, so the blend occupies less volume
// than its parts did separately. Averaging the strengths by volume
// implicitly assumes the volumes add, which they don't.
//
// Running the balance in mass fixes it — mass is additive — and the
// tables convert back. The error is small but real, and it lands on a
// number that goes on a B266.
type BlendPlan struct {
	Sources []BlendSource

	// TotalLAA is the absolute alcohol vatted together. Conserved: this is
	// simply the sum of the parts.
	TotalLAA float64
	// TotalMassKg is the apparent (in-air) mass of the blend.
	TotalMassKg float64
	// BlendVolumeL and BlendStrengthPct describe the vatting before any
	// water is added.
	BlendVolumeL     float64
	BlendStrengthPct float64
	// NaiveVolumeL is the sum of the source volumes — what a spreadsheet
	// would give. Carried so the shrinkage can be shown rather than
	// asserted.
	NaiveVolumeL float64
	ContractionL float64

	// Set when a target strength was requested.
	Reduction    *Reduction
	ReductionSet bool
}

// PlanBlend vats the sources together and, when targetStrengthPct is
// positive, works out the water needed to bring the result down to it.
func PlanBlend(sources []BlendSource, targetStrengthPct float64) (BlendPlan, error) {
	if len(sources) < 2 {
		return BlendPlan{}, errors.New("a blend needs at least two sources")
	}
	p := BlendPlan{Sources: sources}

	for _, s := range sources {
		if s.VolumeL <= 0 {
			return BlendPlan{}, fmt.Errorf("%s: volume must be > 0", sourceName(s))
		}
		if s.StrengthPct <= 0 || s.StrengthPct > 100 {
			return BlendPlan{}, &RangeError{
				What:  fmt.Sprintf("%s strength", sourceName(s)),
				Value: s.StrengthPct, Min: 0, Max: 100, Unit: "%",
			}
		}
		r, err := LookupByStrength(ReferenceTemperatureC, s.StrengthPct)
		if err != nil {
			return BlendPlan{}, err
		}
		p.TotalLAA += s.VolumeL * s.StrengthPct / 100
		p.TotalMassKg += s.VolumeL / r.LitresPerKg
		p.NaiveVolumeL += s.VolumeL
	}

	strength, volume, err := strengthForMassAndAlcohol(p.TotalMassKg, p.TotalLAA)
	if err != nil {
		return BlendPlan{}, err
	}
	p.BlendStrengthPct = strength
	p.BlendVolumeL = volume
	p.ContractionL = p.NaiveVolumeL - volume

	if targetStrengthPct > 0 {
		if targetStrengthPct >= p.BlendStrengthPct {
			return BlendPlan{}, fmt.Errorf(
				"the blend comes out at %.2f %%, so it cannot be brought to %.2f %% by adding water",
				p.BlendStrengthPct, targetStrengthPct)
		}
		red, err := PlanReduction(p.BlendVolumeL, p.BlendStrengthPct, targetStrengthPct)
		if err != nil {
			return BlendPlan{}, err
		}
		p.Reduction = &red
		p.ReductionSet = true
	}
	return p, nil
}

// strengthForMassAndAlcohol solves for the strength of a parcel with a
// known apparent mass and a known quantity of absolute alcohol.
//
// Volume and strength are coupled through the tables — litres = kg × A(B),
// and LAA = litres × B/100 — so there is no closed form. Strength rises
// monotonically as alcohol goes up for a fixed mass, which makes bisection
// both valid and quick.
func strengthForMassAndAlcohol(massKg, laa float64) (strengthPct, volumeL float64, err error) {
	if massKg <= 0 {
		return 0, 0, errors.New("blend has no mass")
	}
	lo, hi := 0.0, 100.0
	for n := 0; n < 60; n++ {
		mid := (lo + hi) / 2
		r, e := LookupByStrength(ReferenceTemperatureC, mid)
		if e != nil {
			return 0, 0, e
		}
		if massKg*r.LitresPerKg*mid/100 < laa {
			lo = mid
		} else {
			hi = mid
		}
	}
	strengthPct = (lo + hi) / 2
	r, e := LookupByStrength(ReferenceTemperatureC, strengthPct)
	if e != nil {
		return 0, 0, e
	}
	return strengthPct, massKg * r.LitresPerKg, nil
}

func sourceName(s BlendSource) string {
	if s.Label != "" {
		return s.Label
	}
	return "source"
}
