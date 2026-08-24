package pricing

// Container deposits are banded by container size, and the band boundary
// is a provincial choice rather than a national one: Alberta's is 1 L,
// Ontario's is 630 mL, and British Columbia abolished its bands entirely
// in 2020. A single rate per province cannot express any of that, and the
// flat rates this package used to carry were wrong for the commonest
// bottle a distillery fills — Alberta's was on file as 25¢, which is the
// over-1-L rate, so every 750 mL bottle sold there was reported at two
// and a half times the real deposit.
//
// So a deposit is a schedule, even where the schedule has one band.

// DepositBand is one size band of a programme's published schedule.
type DepositBand struct {
	// MaxML is the inclusive upper bound of the band in millilitres.
	// Zero means the band has no upper bound, which makes it the last.
	MaxML int32
	Rate  Rate
}

// DepositSchedule is a programme's bands, smallest first.
type DepositSchedule []DepositBand

// For returns the rate that applies to a container of the given size.
//
// An empty schedule returns Unknown rather than zero. A jurisdiction
// added without deposit data should make the report say so, not report a
// deposit liability of nothing — those two look identical in a total and
// only one of them is ever true.
func (s DepositSchedule) For(sizeML int32) Rate {
	if len(s) == 0 {
		return unknownRate(
			"no container deposit schedule is on file for this jurisdiction")
	}
	for _, b := range s {
		if b.MaxML == 0 || sizeML <= b.MaxML {
			return b.Rate
		}
	}
	// Reachable only when every band is bounded and the container is
	// bigger than all of them, which means the schedule is incomplete
	// rather than that the container is free of deposit.
	return unknownRate(
		"the container is larger than every band in the deposit schedule on file")
}

// flatDeposit builds a one-band schedule for a programme that charges the
// same on every size, or for one whose rate is not known at all.
func flatDeposit(r Rate) DepositSchedule { return DepositSchedule{{Rate: r}} }
