// Package excise computes Canadian excise duty owed on spirits removed
// from an excise warehouse, per the Excise Act, 2001.
//
// Rates are currently hardcoded at the values that took effect on
// 1 April 2026 (EDN104). When CRA changes them again — or when we need
// to compute historical removals — replace `Owed` with a date-driven
// rate table.
package excise

import "time"

const (
	// DutyRatePerLAAOver7Pct: spirits >7% ABV are taxed per litre of
	// absolute alcohol. CAD.
	DutyRatePerLAAOver7Pct = 14.117

	// DutyRatePerLAtOrUnder7: spirits at or under 7% ABV are taxed per
	// litre of product (not LAA). CAD.
	DutyRatePerLAtOrUnder7 = 0.358

	AbvThresholdPct = 7.0
)

// Owed returns (rate_per_LAA, total_CAD) for a removal. For spirits >7% ABV
// the per-LAA rate is the working unit and we return it; for ≤7% the rate
// is per litre of product, so we return 0 for rate_per_LAA and the
// caller should treat that as "use the ≤7% per-L rate" when displaying.
func Owed(_ time.Time, volumeL, abvPct float64) (ratePerLAA, totalCAD float64) {
	if abvPct > AbvThresholdPct {
		laa := volumeL * abvPct / 100
		return DutyRatePerLAAOver7Pct, laa * DutyRatePerLAAOver7Pct
	}
	return 0, volumeL * DutyRatePerLAtOrUnder7
}
