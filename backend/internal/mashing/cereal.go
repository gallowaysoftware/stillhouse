// Package mashing turns a mash's actual grain bill and recorded readings
// into the guidance a distiller needs while the tun is still hot: what
// temperature the starch will gelatinise at, whether the bill forces a
// separate cereal cook, whether the recorded pH and thickness sit in the
// bands the amylases want, and how much of the available extract was
// actually converted.
//
// Every figure here is sourced from the IBD/CIBD distilling curriculum
// (Module 1 — cereals, cereal wort production, materials handling) and is
// cited on the constant that carries it. Where the curriculum gives no
// figure for a cereal, this package says so rather than interpolating —
// guidance that is silently wrong is worse than no guidance.
//
// Deliberately absent: anything resembling a brewer's equipment profile.
// A distillery wash is not lautered and boiled the way a beer wort is —
// the whole mash goes to the fermenter and on to the still — so boil-off
// rates, trub losses and kettle deadspace have no meaning here.
package mashing

// Cereal identifies a grain species. Gelatinisation temperature is a
// property of the starch granule, so it varies by species rather than by
// whether the grain arrived malted.
type Cereal int

const (
	CerealUnspecified Cereal = iota
	CerealBarley
	CerealWheat
	CerealRye
	CerealMaize
	CerealRice
	CerealOat
	CerealOther
)

// TemperatureRange is an inclusive band in degrees Celsius.
type TemperatureRange struct {
	MinC, MaxC float64
}

func (r TemperatureRange) Contains(c float64) bool { return c >= r.MinC && c <= r.MaxC }
func (r TemperatureRange) Zero() bool              { return r.MinC == 0 && r.MaxC == 0 }

// Gelatinisation returns the published gelatinisation range for a cereal,
// and whether the curriculum gives one at all.
//
// Source: CIBD Module 1 — cereal wort production. "Gelatinization typically
// occurs between fifty-two and eighty degrees Celsius, depending on the
// cereal species."
func Gelatinisation(c Cereal) (TemperatureRange, bool) {
	switch c {
	case CerealBarley:
		// "barley at sixty-one to sixty-two degrees Celsius"
		return TemperatureRange{61, 62}, true
	case CerealWheat:
		// "Barley and wheat gelatinise relatively easily between
		// fifty-two and sixty-five degrees Celsius."
		return TemperatureRange{52, 65}, true
	case CerealRye:
		// "followed by rye at sixty to sixty-five degrees Celsius"
		return TemperatureRange{60, 65}, true
	case CerealMaize:
		// "Maize... requiring temperatures between seventy and eighty
		// degrees Celsius to fully gelatinise." High-amylose maize needs
		// above eighty, and is corrected by cooking at eighty-five plus.
		return TemperatureRange{70, 80}, true
	case CerealRice:
		// "Maize and rice require significantly higher temperatures,
		// typically seventy to eighty degrees Celsius"
		return TemperatureRange{70, 80}, true
	default:
		// Oats and anything else: the curriculum gives no figure, so we
		// give none. Callers must handle !ok rather than get a guess.
		return TemperatureRange{}, false
	}
}

// Enzyme activity windows. Source: CIBD Module 1 — cereal wort production.
var (
	// BetaAmylase: "Beta-amylase, an exo-enzyme, functions best between
	// fifty-four and sixty-six degrees Celsius", optimum around 62–64.
	// Favouring it maximises maltose and therefore fermentability.
	BetaAmylase = TemperatureRange{54, 66}
	// BetaAmylaseOptimum: "If you hold a mash at sixty-two degrees, you
	// heavily favour beta-amylase activity, maximising maltose production
	// for high fermentability."
	BetaAmylaseOptimum = TemperatureRange{62, 64}

	// AlphaAmylase: "Alpha-amylase, an endo-enzyme, operates optimally
	// between sixty-six and seventy-one degrees Celsius", optimum ~70.
	AlphaAmylase        = TemperatureRange{66, 71}
	AlphaAmylaseOptimum = TemperatureRange{69, 71}

	// Saccharification is where both amylases are active: "Both
	// saccharification enzymes operate optimally between sixty and
	// seventy degrees Celsius."
	Saccharification = TemperatureRange{60, 70}

	// MashPH: "The optimal pH for amylase activity is between five point
	// two and five point six." Above 5.5 — typically from high residual
	// alkalinity in the liquor — activity falls off sharply.
	MashPH             = Range{5.2, 5.6}
	MashPHAlkalineFrom = 5.5

	// MashThickness is the water-to-grain ratio in litres per kilogram:
	// "typically falls between two point five and three point five litres
	// per kilogram."
	MashThickness = Range{2.5, 3.5}
)

const (
	// EnzymeDenaturationC — "Adding malt or exogenous enzymes to a mash
	// that is still above eighty degrees Celsius will instantly denature
	// the proteins." Also the point at which beta-amylase in the mash is
	// destroyed.
	EnzymeDenaturationC = 80.0

	// MashOutC — "we raise the temperature to seventy-five degrees Celsius
	// for the mash-out. This higher temperature denatures the enzymes,
	// halting further conversion, and reduces wort viscosity."
	MashOutC = 75.0

	// HighAmyloseMaizeCookC — high-amylose maize needs above 80 °C to
	// disrupt its crystalline structure; the curriculum's correction is to
	// raise the cook to at least eighty-five degrees.
	HighAmyloseMaizeCookC = 85.0

	// PreGelatinisationC / PreGelatinisationMinutes — the separate cereal
	// cook: "cooking the maize slurry separately at ninety degrees Celsius
	// for thirty minutes to fully disrupt the granules."
	PreGelatinisationC       = 90.0
	PreGelatinisationMinutes = 30
)

// Range is an inclusive numeric band for quantities that aren't temperatures.
type Range struct {
	Min, Max float64
}

func (r Range) Contains(v float64) bool { return v >= r.Min && v <= r.Max }
