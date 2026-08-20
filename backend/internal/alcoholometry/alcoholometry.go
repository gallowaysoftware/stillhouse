// Package alcoholometry resolves alcoholic strength and volume against the
// CRA "Canadian Alcoholometric Tables 1980" — the legal basis for
// determining the strength and volume of spirits for excise purposes in
// Canada.
//
// The tables are computed from the OIML general formula (International
// Recommendation No. 22, 1972) and published by CRA at:
//
//	https://www.canada.ca/en/revenue-agency/services/tax/technical-information/
//	  excise-duty/tables-alcoholometry/canadian-alcoholometric-tables-1980.html
//
// # Why this exists
//
// Alcoholic strength is only meaningful at a reference temperature. CRA's
// reference is 20 °C: approved instruments are "hydrometers for alcohol"
// graduated in units of density (kg/m³) at 20 °C, read together with an
// approved Celsius thermometer. A reading taken at any other temperature
// describes a liquid that has expanded or contracted, so BOTH the strength
// and the volume have to be resolved back to 20 °C before they mean
// anything on a B266.
//
// The three published columns, in CRA's own notation:
//
//	A — factor to convert kilograms of spirits to litres of spirits at 20 °C
//	B — percentage of absolute ethyl alcohol by volume at 20 °C
//	C — factor to convert litres of spirits at the temperature of
//	    measurement to litres of spirits at 20 °C
//
// So the quantity B266 actually wants is:
//
//	LAA = (observed litres × C) × (B / 100)
//
// Both corrections matter, and for warm spirit they compound: a warm
// sample is less dense, so an uncorrected hydrometer overstates strength,
// while the expanded volume overstates litres.
//
// # The tables are supplied by the operator
//
// They are NOT shipped with Stillhouse. They are Crown material, and the
// Government of Canada's terms allow non-commercial reproduction but not
// commercial redistribution without written permission — which would make
// a paid hosted Stillhouse a licensing problem. The operator downloads the
// ZIP from CRA once and points STILLHOUSE_ALCOHOLOMETRIC_TABLES at it; see
// load.go. Every lookup returns ErrNotLoaded until then, and the
// uncorrected path keeps working, so a missing file degrades one feature
// rather than stopping the server.
//
// # Range
//
// Temperatures −20.0 °C to +40.0 °C in 0.5 °C steps; densities 780.0 to
// 999.4 kg/m³ in 0.2 kg/m³ steps, with the valid density span varying by
// temperature. Readings between grid points are bilinearly interpolated;
// readings exactly on the grid reproduce the published value exactly, so a
// filing built from on-grid readings matches the printed table digit for
// digit.
package alcoholometry

import (
	"fmt"
	"math"
)

// Reading is one resolved row of the tables. Field names follow CRA's
// column notation; see the package doc.
type Reading struct {
	// StrengthPct is column B — percentage of absolute ethyl alcohol by
	// volume at 20 °C.
	StrengthPct float64
	// VolumeFactor is column C — multiply litres measured at the
	// measurement temperature by this to get litres at 20 °C.
	VolumeFactor float64
	// LitresPerKg is column A — multiply kilograms of spirits by this to
	// get litres of spirits at 20 °C. Carried for gauging by weight;
	// Stillhouse gauges by volume today.
	LitresPerKg float64
}

type table struct {
	srcSHA    [32]byte
	srcName   string
	tempMin   int // tenths of °C
	tempStep  int // tenths of °C
	densStep  int // tenths of kg/m³
	densStart []int
	rowCount  []int
	rowOffset []int
	// rows holds A, B, C consecutively for each row, in the order the
	// per-temperature blocks appear.
	rows []float64
}

// SourceSHA256 returns the SHA-256 of the file that was loaded, so a
// deployment can prove which published table it files against.
func SourceSHA256() ([32]byte, error) {
	t, err := get()
	if err != nil {
		return [32]byte{}, err
	}
	return t.srcSHA, nil
}

// SourceName is the filename the tables were read from.
func SourceName() string {
	mu.RLock()
	defer mu.RUnlock()
	if loaded == nil {
		return ""
	}
	return loaded.srcName
}

// RangeError reports a reading that falls outside the published tables.
type RangeError struct {
	What     string
	Value    float64
	Min, Max float64
	Unit     string
}

func (e *RangeError) Error() string {
	return fmt.Sprintf("alcoholometry: %s %.4g %s is outside the Canadian Alcoholometric Tables (%.4g–%.4g %s)",
		e.What, e.Value, e.Unit, e.Min, e.Max, e.Unit)
}

// TemperatureRange returns the temperature span the tables cover, in °C.
func TemperatureRange() (lo, hi float64, err error) {
	t, err := get()
	if err != nil {
		return 0, 0, err
	}
	lo, hi = t.tempSpan()
	return lo, hi, nil
}

func (t *table) tempSpan() (lo, hi float64) {
	return float64(t.tempMin) / 10,
		float64(t.tempMin+(len(t.densStart)-1)*t.tempStep) / 10
}

// rowAt returns the reading at grid indices (ti, ri).
func (t *table) rowAt(ti, ri int) Reading {
	o := (t.rowOffset[ti] + ri) * 3
	return Reading{
		LitresPerKg:  t.rows[o],
		StrengthPct:  t.rows[o+1],
		VolumeFactor: t.rows[o+2],
	}
}

func lerp(a, b Reading, f float64) Reading {
	return Reading{
		StrengthPct:  a.StrengthPct + (b.StrengthPct-a.StrengthPct)*f,
		VolumeFactor: a.VolumeFactor + (b.VolumeFactor-a.VolumeFactor)*f,
		LitresPerKg:  a.LitresPerKg + (b.LitresPerKg-a.LitresPerKg)*f,
	}
}

// atTemp interpolates along the density axis at temperature index ti.
func (t *table) atTemp(ti int, densityTenths float64) (Reading, error) {
	lo := float64(t.densStart[ti])
	hi := lo + float64((t.rowCount[ti]-1)*t.densStep)
	if densityTenths < lo-1e-9 || densityTenths > hi+1e-9 {
		return Reading{}, &RangeError{
			What: "density", Value: densityTenths / 10,
			Min: lo / 10, Max: hi / 10, Unit: "kg/m³",
		}
	}
	pos := (densityTenths - lo) / float64(t.densStep)
	i := int(math.Floor(pos))
	if i >= t.rowCount[ti]-1 {
		return t.rowAt(ti, t.rowCount[ti]-1), nil
	}
	f := pos - float64(i)
	if f < 1e-9 {
		return t.rowAt(ti, i), nil
	}
	return lerp(t.rowAt(ti, i), t.rowAt(ti, i+1), f), nil
}

// Lookup resolves a hydrometer indication (density in kg/m³) taken at
// tempC against the tables. This is the primary entry point: CRA's
// approved instrument for fiscal determination is a hydrometer graduated
// in density at a 20 °C reference.
func Lookup(tempC, densityKgM3 float64) (Reading, error) {
	t, err := get()
	if err != nil {
		return Reading{}, err
	}
	tLo, tHi := t.tempSpan()
	if tempC < tLo-1e-9 || tempC > tHi+1e-9 {
		return Reading{}, &RangeError{What: "temperature", Value: tempC, Min: tLo, Max: tHi, Unit: "°C"}
	}
	dTenths := densityKgM3 * 10

	pos := (tempC*10 - float64(t.tempMin)) / float64(t.tempStep)
	i := int(math.Floor(pos))
	if i >= len(t.densStart)-1 {
		return t.atTemp(len(t.densStart)-1, dTenths)
	}
	f := pos - float64(i)
	if f < 1e-9 {
		return t.atTemp(i, dTenths)
	}
	a, err := t.atTemp(i, dTenths)
	if err != nil {
		return Reading{}, err
	}
	b, err := t.atTemp(i+1, dTenths)
	if err != nil {
		return Reading{}, err
	}
	return lerp(a, b, f), nil
}

// DensitySpan returns the density range the tables cover at tempC, narrowed
// to the overlap of the bracketing temperature rows so interpolation never
// runs off the end of either.
func DensitySpan(tempC float64) (lo, hi float64, err error) {
	t, err := get()
	if err != nil {
		return 0, 0, err
	}
	lo, hi = t.densSpan(tempC)
	return lo, hi, nil
}

func (t *table) densSpan(tempC float64) (loKgM3, hiKgM3 float64) {
	pos := (tempC*10 - float64(t.tempMin)) / float64(t.tempStep)
	i := int(math.Floor(pos))
	if i < 0 {
		i = 0
	}
	if i > len(t.densStart)-1 {
		i = len(t.densStart) - 1
	}
	j := i
	if pos-float64(i) > 1e-9 && i < len(t.densStart)-1 {
		j = i + 1
	}
	lo := math.Max(float64(t.densStart[i]), float64(t.densStart[j]))
	hi := math.Min(
		float64(t.densStart[i]+(t.rowCount[i]-1)*t.densStep),
		float64(t.densStart[j]+(t.rowCount[j]-1)*t.densStep),
	)
	return lo / 10, hi / 10
}

// LookupByStrength resolves the tables from a strength already expressed at
// 20 °C — the output of a density meter that applies its own correction, or
// a strength the operator looked up by hand. It finds the density carrying
// that strength at the measurement temperature, which is what the volume
// factor C is indexed by.
//
// Use Lookup when you have a raw hydrometer indication; use this when the
// instrument has already given you a 20 °C strength but the volume still
// needs correcting off the measurement temperature.
func LookupByStrength(tempC, strengthPct20C float64) (Reading, error) {
	t, err := get()
	if err != nil {
		return Reading{}, err
	}
	if strengthPct20C < 0 || strengthPct20C > 100 {
		return Reading{}, &RangeError{What: "strength", Value: strengthPct20C, Min: 0, Max: 100, Unit: "%"}
	}
	tLo, tHi := t.tempSpan()
	if tempC < tLo-1e-9 || tempC > tHi+1e-9 {
		return Reading{}, &RangeError{What: "temperature", Value: tempC, Min: tLo, Max: tHi, Unit: "°C"}
	}

	// Strength falls monotonically as density rises, so bisect the density
	// axis for the density carrying the requested strength. 60 halvings
	// resolve the span far below the 0.2 kg/m³ grid.
	lo, hi := t.densSpan(tempC)
	for n := 0; n < 60; n++ {
		mid := (lo + hi) / 2
		r, err := Lookup(tempC, mid)
		if err != nil {
			return Reading{}, err
		}
		if r.StrengthPct > strengthPct20C {
			lo = mid
		} else {
			hi = mid
		}
	}
	r, err := Lookup(tempC, (lo+hi)/2)
	if err != nil {
		return Reading{}, err
	}
	// Report the strength the caller asserted rather than the bisection's
	// reconstruction of it — A and C are what we were after.
	r.StrengthPct = strengthPct20C
	return r, nil
}

// AbsoluteAlcohol returns litres of absolute ethyl alcohol at 20 °C for
// observedVolumeL litres of spirits gauged at tempC with the given
// hydrometer indication, following CRA's volume/density procedure:
//
//	litres at 20 °C = observed litres × C
//	LAA             = litres at 20 °C × B / 100
func AbsoluteAlcohol(tempC, densityKgM3, observedVolumeL float64) (laa, volumeL20C float64, r Reading, err error) {
	r, err = Lookup(tempC, densityKgM3)
	if err != nil {
		return 0, 0, Reading{}, err
	}
	volumeL20C = observedVolumeL * r.VolumeFactor
	return volumeL20C * r.StrengthPct / 100, volumeL20C, r, nil
}
