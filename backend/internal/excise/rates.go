// Package excise computes Canadian excise duty on spirits under the
// Excise Act, 2001.
//
// Rates are date-effective. CRA indexes them every 1 April, so the rate
// that applies is the one in force on the day the duty became payable —
// not the one in force today. Computing an amended, late or reopened
// prior-period return at today's rate against last year's quantities
// produces a wrong number on a filed document, and nothing about it looks
// wrong.
//
// The table refuses outside the span it knows rather than extrapolating,
// which is the same discipline the pricing engine and the alcoholometric
// tables follow: a figure Stillhouse cannot source is a figure Stillhouse
// does not state. When the next indexation lands, add its band — one
// struct literal — rather than editing the one below.
package excise

import (
	"fmt"
	"time"
)

// AbvThresholdPct separates the two rate bands. Spirits above it are
// charged per litre of absolute alcohol; spirits at or below it are
// charged per litre of product.
const AbvThresholdPct = 7.0

// Band is the set of spirits duty rates in force over a span of time.
type Band struct {
	// EffectiveFrom is the first day the rates apply, inclusive.
	EffectiveFrom time.Time
	// KnownUntil is the first day they are no longer known to apply,
	// exclusive — normally the next 1 April indexation. A lookup on or
	// after this date refuses, because the rate that replaced these is not
	// on file.
	KnownUntil time.Time
	// Source is the CRA notice the figures were read from, so an operator
	// can check them against the original.
	Source string

	// PerLAAOver7Pct is CAD per litre of absolute alcohol, spirits above
	// 7% ABV.
	PerLAAOver7Pct float64
	// PerLitreAtOrUnder7 is CAD per litre of product (not per LAA),
	// spirits at or below 7% ABV.
	PerLitreAtOrUnder7 float64
}

// bands is the rate history, ascending by EffectiveFrom and
// non-overlapping. Seeded with the single band Stillhouse can cite;
// earlier bands come from the EDN notice series and are a data change,
// not a code change.
//
// Adding a band: set the previous band's KnownUntil to the new band's
// EffectiveFrom, and give the new one a KnownUntil of the following
// 1 April. The table test enforces both.
var bands = []Band{
	{
		EffectiveFrom:      time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		KnownUntil:         time.Date(2027, 4, 1, 0, 0, 0, 0, time.UTC),
		Source:             "EDN104",
		PerLAAOver7Pct:     14.117,
		PerLitreAtOrUnder7: 0.358,
	},
}

// UnknownRateError is returned when no band covers the date asked for —
// either before the earliest band on file or on/after the last one's
// KnownUntil. It carries the covered span so the message can say what to
// do about it.
type UnknownRateError struct {
	On                 time.Time
	KnownFrom, KnownTo time.Time
}

func (e *UnknownRateError) Error() string {
	return fmt.Sprintf(
		"no excise duty rate on file for %s — Stillhouse knows the rates from %s to %s and will not extrapolate; add the CRA notice band for that date",
		e.On.Format("2006-01-02"),
		e.KnownFrom.Format("2006-01-02"),
		e.KnownTo.Format("2006-01-02"))
}

// Coverage returns the span the rate table can answer for: from the
// earliest band's EffectiveFrom to the latest band's KnownUntil,
// exclusive.
func Coverage() (from, to time.Time) {
	return bands[0].EffectiveFrom, bands[len(bands)-1].KnownUntil
}

// RateOn returns the band in force on a date. Only the calendar day
// matters — rates change at midnight and duty is a daily event — so the
// time of day and the location are discarded before comparing, which
// keeps a removal dated in local time from resolving to the previous
// band.
func RateOn(on time.Time) (Band, error) {
	day := time.Date(on.Year(), on.Month(), on.Day(), 0, 0, 0, 0, time.UTC)
	for i := len(bands) - 1; i >= 0; i-- {
		b := bands[i]
		if !day.Before(b.EffectiveFrom) && day.Before(b.KnownUntil) {
			return b, nil
		}
	}
	from, to := Coverage()
	return Band{}, &UnknownRateError{On: day, KnownFrom: from, KnownTo: to}
}

// Owed returns (rate_per_LAA, total_CAD) for a quantity of spirits dutied
// on a given date.
//
// For spirits above 7% ABV the per-LAA rate is the working unit and is
// returned. At or below 7% the charge is per litre of product, so the
// per-LAA figure is returned as 0 — a caller that multiplies a ≤7%
// quantity by a per-LAA rate is asking the wrong question, and a zero
// makes that visible rather than plausible.
func Owed(on time.Time, volumeL, abvPct float64) (ratePerLAA, totalCAD float64, err error) {
	b, err := RateOn(on)
	if err != nil {
		return 0, 0, err
	}
	if abvPct > AbvThresholdPct {
		laa := volumeL * abvPct / 100
		return b.PerLAAOver7Pct, laa * b.PerLAAOver7Pct, nil
	}
	return 0, volumeL * b.PerLitreAtOrUnder7, nil
}

// DutyOnLAA returns the duty on a quantity of absolute alcohol at the rate
// in force on a date.
//
// Bulk spirits are always above the 7% band — that is what makes them
// spirits — so the per-LAA rate is the one that applies, and there is no
// litres-of-product figure to charge the low-strength rate against. Callers
// holding a packaged quantity should use Owed instead, which picks the band
// from the strength.
func DutyOnLAA(on time.Time, laa float64) (ratePerLAA, totalCAD float64, err error) {
	b, err := RateOn(on)
	if err != nil {
		return 0, 0, err
	}
	return b.PerLAAOver7Pct, laa * b.PerLAAOver7Pct, nil
}
