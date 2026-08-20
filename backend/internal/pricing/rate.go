package pricing

import "fmt"

// Provenance records how much a rate can be trusted. It exists because
// the previous version of this package carried five provincial markup
// percentages as bare float64 constants with no indication that not one
// of them came from a published schedule — and a pricing page that looks
// authoritative is worse than one that admits what it doesn't know.
//
// Provincial liquor statutes do not set markup rates. Both jurisdictions
// checked delegate: PEI's Liquor Control Act Regulations s.89 has the
// Commission issue price lists, and Ontario's Liquor Licence and Control
// Act 2019 contains no rate at all. Rates live in board policy documents
// that change without a legislative amendment, so every one of them has
// to carry a date and a source or it is folklore.
type Provenance int

const (
	// Unknown — no rate is available. Anything depending on it must
	// refuse to compute rather than substitute a guess.
	Unknown Provenance = iota
	// Indicative — from a secondary source (trade press, an aggregator).
	// Usable for planning, flagged in the UI, not for quoting a customer.
	Indicative
	// Sourced — from the board's or the legislature's own published
	// material, with the URL and date recorded.
	Sourced
)

func (p Provenance) String() string {
	switch p {
	case Sourced:
		return "sourced"
	case Indicative:
		return "indicative"
	default:
		return "unknown"
	}
}

// Rate is a number that came from somewhere, with the somewhere attached.
type Rate struct {
	Value      float64
	Provenance Provenance
	// Source is a URL or the name of the document the value came from.
	Source string
	// AsOf is the ISO date the value was published or last confirmed.
	AsOf string
	// Note carries anything a reader needs in order not to misuse it.
	Note string
}

// Known reports whether the rate can be used in a calculation at all.
func (r Rate) Known() bool { return r.Provenance != Unknown }

// Or returns the rate's value, or fallback when it isn't known. Use only
// where a missing rate genuinely means zero — a province with no
// container deposit, say — never to paper over a rate nobody has found.
func (r Rate) Or(fallback float64) float64 {
	if !r.Known() {
		return fallback
	}
	return r.Value
}

// unknownRate builds a rate that explains its own absence.
func unknownRate(note string) Rate {
	return Rate{Provenance: Unknown, Note: note}
}

// Missing describes a rate a calculation needed and did not have.
type Missing struct {
	// What names the rate, e.g. "Ontario spirits wholesale mark-up".
	What string
	// Why explains what is blocked and where to get the number.
	Why string
}

func (m Missing) Error() string { return fmt.Sprintf("%s: %s", m.What, m.Why) }
