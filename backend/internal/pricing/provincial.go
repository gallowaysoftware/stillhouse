// Package pricing computes how a distiller's FOB price flows through each
// Canadian provincial liquor authority's markup formula to arrive at the
// shelf price a consumer pays.
//
// IMPORTANT: the rates here are publicly published as of late 2025 / early
// 2026 (LCBO cost-plus reform, Ontario spirits basic-tax reduction effective
// Aug 1 2025, etc.) but the actual numbers move frequently. Treat the
// output as an estimate for planning, not a quote to a customer.
package pricing

// ProvincialMarkup describes one Canadian jurisdiction's pricing formula.
// All rates are decimal fractions where applicable (0.30 == 30%).
type ProvincialMarkup struct {
	Jurisdiction string // ISO 3166-2, e.g. "CA-ON"
	Name         string
	// MarkupPct: the provincial monopoly's ad valorem markup on the FOB.
	// (Approximation; real markups are tiered + product-category-specific.)
	MarkupPct float64
	// PerLitreCAD: a flat per-litre cost-of-service that some boards add.
	PerLitreCAD float64
	// SpiritsBasicTaxPct: provincial basic tax on spirits (applied to FOB
	// for distillery on-site retail, with caveats per province).
	SpiritsBasicTaxPct float64
	// ContainerDepositCAD: standard returnable container deposit (per bottle).
	ContainerDepositCAD float64
	// Notes: short caveat shown in the UI alongside the breakdown.
	Notes string
}

// Markups is the working set of provincial pricing models. Add to this
// list as more jurisdictions are documented.
var Markups = []ProvincialMarkup{
	{
		Jurisdiction: "CA-ON", Name: "Ontario (LCBO)",
		MarkupPct: 0.70, PerLitreCAD: 0,
		SpiritsBasicTaxPct: 0.3075, ContainerDepositCAD: 0.20,
		Notes: "LCBO cost-plus wholesale model live April 1, 2026. " +
			"Spirits basic tax reduced from 61.5% to 30.75% on August 1, 2025 " +
			"for distillery on-site retail sales.",
	},
	{
		Jurisdiction: "CA-QC", Name: "Québec (SAQ)",
		MarkupPct: 1.05, PerLitreCAD: 0,
		SpiritsBasicTaxPct: 0, ContainerDepositCAD: 0.20,
		Notes: "SAQ markup on spirits typically exceeds 100% of FOB; " +
			"on-site distillery sales permitted since 2017. DTC to QC " +
			"consumers prohibited.",
	},
	{
		Jurisdiction: "CA-BC", Name: "British Columbia (BCLDB)",
		MarkupPct: 1.03, PerLitreCAD: 0,
		SpiritsBasicTaxPct: 0, ContainerDepositCAD: 0.10,
		Notes: "BC Craft Distillery designation gets graduated markup relief " +
			"(0–103% on first 100k L) if 100% BC inputs. Direct delivery to " +
			"private retail allowed.",
	},
	{
		Jurisdiction: "CA-AB", Name: "Alberta (AGLC)",
		MarkupPct: 0,
		PerLitreCAD: 13.76, // approx weighted-avg for spirits 22-60% ABV
		SpiritsBasicTaxPct: 0, ContainerDepositCAD: 0.25,
		Notes: "Alberta uses a flat per-litre rate, not a percentage markup. " +
			"Privatized retail; Connect Logistics distributes.",
	},
	{
		Jurisdiction: "CA-NS", Name: "Nova Scotia (NSLC)",
		MarkupPct: 0.85, PerLitreCAD: 0,
		SpiritsBasicTaxPct: 0, ContainerDepositCAD: 0.10,
		Notes: "On-site retail + offsite retail permits; first DTC bilateral " +
			"with Ontario signed March 2026.",
	},
}

// Breakdown is the per-province computation for a single product at one FOB.
type Breakdown struct {
	Jurisdiction        string
	Name                string
	FOBCAD              float64
	MarkupCAD           float64
	PerLitreCAD         float64
	BasicTaxCAD         float64
	FederalExciseCAD    float64 // CRA excise duty (LAA × current rate)
	ContainerDepositCAD float64
	ShelfBeforeSalesTax float64
	// What the distiller receives from the monopoly = FOB. For on-site
	// retail in ON (post Aug 2025), the distiller keeps shelf minus basic
	// tax. We surface both perspectives.
	OnSiteRetailNetCAD float64
	Notes              string
}

// ComputeBreakdown runs the pricing chain for every province in Markups
// given a product's FOB price, bottle size (mL), and bottle ABV (%).
// federalDutyPerLAA should be the current CRA rate (Stage 7 hardcodes
// $14.117/LAA effective 2026-04-01).
func ComputeBreakdown(
	fobCAD float64,
	bottleSizeML int32,
	bottleABVPct float64,
	federalDutyPerLAA float64,
) []Breakdown {
	volumeL := float64(bottleSizeML) / 1000
	laa := volumeL * bottleABVPct / 100
	federalExcise := laa * federalDutyPerLAA

	out := make([]Breakdown, 0, len(Markups))
	for _, m := range Markups {
		markup := fobCAD * m.MarkupPct
		perL := m.PerLitreCAD * volumeL
		basicTax := fobCAD * m.SpiritsBasicTaxPct
		shelf := fobCAD + markup + perL + basicTax + federalExcise + m.ContainerDepositCAD

		// On-site retail net for the distiller in jurisdictions that allow it:
		// distiller keeps shelf - basic_tax_CAD - federal_excise (collected,
		// remitted) - container_deposit (returned). Markup component does NOT
		// apply at on-site retail (no monopoly handling).
		onSiteShelf := fobCAD + basicTax + federalExcise + m.ContainerDepositCAD
		onSiteNet := onSiteShelf - basicTax - federalExcise - m.ContainerDepositCAD

		out = append(out, Breakdown{
			Jurisdiction:        m.Jurisdiction,
			Name:                m.Name,
			FOBCAD:              fobCAD,
			MarkupCAD:           markup,
			PerLitreCAD:         perL,
			BasicTaxCAD:         basicTax,
			FederalExciseCAD:    federalExcise,
			ContainerDepositCAD: m.ContainerDepositCAD,
			ShelfBeforeSalesTax: shelf,
			OnSiteRetailNetCAD:  onSiteNet,
			Notes:               m.Notes,
		})
	}
	return out
}
