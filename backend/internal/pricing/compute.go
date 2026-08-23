package pricing

// Input is one bottle, priced.
type Input struct {
	// FOBCAD is what the distillery charges the board, per bottle.
	FOBCAD       float64
	BottleSizeML int32
	BottleABVPct float64
	// FederalDutyPerLAA is the CRA excise rate in force; see internal/excise.
	FederalDutyPerLAA float64
	// FreightCAD and ImportDutiesCAD are the remaining components of
	// landed cost. Zero for a domestic distillery shipping locally.
	FreightCAD      float64
	ImportDutiesCAD float64
	// Imported drives the cost-of-service differential, which provinces
	// charge on product from outside the province.
	Imported bool
	// OnSiteRetailPriceCAD is the shelf price in the distillery's own
	// shop, which the distillery chooses. Zero means "don't compute the
	// on-site channel".
	OnSiteRetailPriceCAD float64
}

// VolumeL is the bottle in litres.
func (in Input) VolumeL() float64 { return float64(in.BottleSizeML) / 1000 }

// LAA is litres of absolute alcohol in the bottle — what federal excise
// is charged on for spirits above 7% ABV.
func (in Input) LAA() float64 { return in.VolumeL() * in.BottleABVPct / 100 }

// FederalExciseCAD is duty on the bottle. Charged on removal for entry
// into the Canadian market, which is why the export channel below does
// not include it.
func (in Input) FederalExciseCAD() float64 { return in.LAA() * in.FederalDutyPerLAA }

// LandedCostCAD is the base every provincial mark-up is applied to:
// supplier quote plus federal excise, import duties and freight. Ontario's
// cost-plus model names it explicitly; other boards use the same idea.
//
// Note the compounding: an ad valorem provincial mark-up is applied on top
// of federal duty, so the two multiply rather than add.
func (in Input) LandedCostCAD() float64 {
	return in.FOBCAD + in.FederalExciseCAD() + in.ImportDutiesCAD + in.FreightCAD
}

// ChannelResult is one channel priced, or an explanation of why it wasn't.
type ChannelResult struct {
	Channel Channel
	// Computable is false when a rate the channel needs is unknown.
	// Every monetary field below is meaningless then.
	Computable bool
	// Missing lists the rates that blocked the calculation, each naming
	// where to get the number.
	Missing []Missing

	// Components, all per bottle.
	LandedCostCAD       float64
	MarkupCAD           float64
	COSDCAD             float64
	ProvincialTaxCAD    float64
	FederalExciseCAD    float64
	ContainerDepositCAD float64
	SalesTaxCAD         float64
	// PriceToBuyerCAD is what the customer pays at the end of this
	// channel — the board's wholesale price, or the shop shelf price.
	PriceToBuyerCAD float64
	// DistilleryNetCAD is what the distillery keeps after remitting
	// everything that isn't theirs. This is the number that decides
	// which channel is worth pursuing.
	DistilleryNetCAD float64
	// Citations are the rates this figure rests on, each with where it
	// came from and when. What makes the number judgeable rather than
	// merely produced.
	Citations []Citation
	// LowestProvenance is the weakest link among the rates actually used,
	// so a caller can label the whole figure honestly.
	LowestProvenance Provenance
}

// JurisdictionBreakdown is every channel for one province.
type JurisdictionBreakdown struct {
	Code, Name, Notes string
	Wholesale         ChannelResult
	OnSiteRetail      ChannelResult
	Export            ChannelResult
}

// Compute prices a bottle through every channel in every jurisdiction.
func Compute(in Input) []JurisdictionBreakdown {
	out := make([]JurisdictionBreakdown, 0, len(Jurisdictions))
	for _, j := range Jurisdictions {
		out = append(out, JurisdictionBreakdown{
			Code: j.Code, Name: j.Name, Notes: j.Notes,
			Wholesale:    computeWholesale(in, j),
			OnSiteRetail: computeOnSite(in, j),
			Export:       computeExport(in, j),
		})
	}
	return out
}

// weakest tracks what a calculation leaned on: the least trustworthy
// rate among them, and where each one came from.
//
// Every rate a computation uses passes through use(), which makes this
// the one place that can answer "why should I believe this number". The
// provenance discipline was in the domain model from the start — every
// Rate carries a Source and an AsOf — and until stage 166 none of it
// reached the API, which is the only place an operator would ever see
// it. Seventeen hand-maintained citations that nothing read was the
// worst of the available options.
type weakest struct {
	p         Provenance
	citations []Citation
}

// Citation is one rate a figure rests on, with its paperwork.
type Citation struct {
	// What the rate is, in words: "Ontario wholesale mark-up".
	What       string
	Value      float64
	Provenance Provenance
	// Where the value came from — a URL, or the name of the document.
	Source string
	// The ISO date it was published or last confirmed.
	AsOf string
	// Anything a reader needs in order not to misuse it.
	Note string
}

func newWeakest() *weakest { return &weakest{p: Sourced} }

func (w *weakest) use(what string, r Rate) float64 {
	if r.Provenance < w.p {
		w.p = r.Provenance
	}
	w.citations = append(w.citations, Citation{
		What: what, Value: r.Value, Provenance: r.Provenance,
		Source: r.Source, AsOf: r.AsOf, Note: r.Note,
	})
	return r.Value
}

// computeWholesale prices the sale to the provincial board.
//
// A province uses either an ad valorem mark-up on landed cost or a flat
// per-litre one; needing neither would mean the board takes nothing, which
// no board does, so a jurisdiction with both unknown cannot be computed.
func computeWholesale(in Input, j Jurisdiction) ChannelResult {
	r := ChannelResult{Channel: ChannelWholesale}
	w := newWeakest()

	hasAdValorem := j.WholesaleMarkupPctOfLanded.Known()
	hasPerLitre := j.WholesalePerLitreCAD.Known()
	if !hasAdValorem && !hasPerLitre {
		note := j.WholesaleMarkupPctOfLanded.Note
		if note == "" {
			note = j.WholesalePerLitreCAD.Note
		}
		r.Missing = append(r.Missing, Missing{
			What: j.Name + " wholesale mark-up",
			Why:  note,
		})
		return r
	}

	r.LandedCostCAD = in.LandedCostCAD()
	r.FederalExciseCAD = in.FederalExciseCAD()
	if hasAdValorem {
		r.MarkupCAD = r.LandedCostCAD * w.use("wholesale mark-up (% of landed cost)", j.WholesaleMarkupPctOfLanded)
	} else {
		r.MarkupCAD = in.VolumeL() * w.use("wholesale mark-up (per litre)", j.WholesalePerLitreCAD)
	}
	if in.Imported && j.COSDPerLitreCAD.Known() {
		r.COSDCAD = in.VolumeL() * w.use("cost of service differential", j.COSDPerLitreCAD)
	}
	if j.ProvincialSpiritsTaxPct.Known() {
		r.ProvincialTaxCAD = in.FOBCAD * w.use("provincial spirits tax", j.ProvincialSpiritsTaxPct)
	}
	r.ContainerDepositCAD = j.ContainerDepositCAD.Or(0)
	if j.ContainerDepositCAD.Known() {
		w.use("container deposit", j.ContainerDepositCAD)
	}

	beforeTax := r.LandedCostCAD + r.MarkupCAD + r.COSDCAD + r.ProvincialTaxCAD + r.ContainerDepositCAD
	if j.SalesTaxPct.Known() {
		r.SalesTaxCAD = beforeTax * w.use("sales tax", j.SalesTaxPct)
	}
	r.PriceToBuyerCAD = beforeTax + r.SalesTaxCAD
	// The distillery sold at its quote; everything above the quote belongs
	// to the board, the carrier or the Crown.
	r.DistilleryNetCAD = in.FOBCAD
	r.Computable = true
	r.LowestProvenance = w.p
	r.Citations = w.citations
	return r
}

// computeOnSite prices a sale in the distillery's own shop.
//
// The distillery sets the shelf price here, so the interesting number runs
// the other way: from that price, what does the distillery keep? It
// remits federal excise, sales tax and the deposit regardless. Whether the
// province also takes a cut is the open question in every jurisdiction
// researched — so where that is unrecorded, this reports a CEILING and
// says so, rather than a net that quietly assumes the answer is zero.
func computeOnSite(in Input, j Jurisdiction) ChannelResult {
	r := ChannelResult{Channel: ChannelOnSiteRetail}
	if !j.OnSite.Permitted {
		r.Missing = append(r.Missing, Missing{
			What: j.Name + " on-site retail",
			Why:  "not permitted in this jurisdiction",
		})
		return r
	}
	if in.OnSiteRetailPriceCAD <= 0 {
		r.Missing = append(r.Missing, Missing{
			What: "shelf price",
			Why:  "you set this one — enter what you'd charge in your own shop",
		})
		return r
	}
	w := newWeakest()

	r.PriceToBuyerCAD = in.OnSiteRetailPriceCAD
	r.FederalExciseCAD = in.FederalExciseCAD()
	r.ContainerDepositCAD = j.ContainerDepositCAD.Or(0)
	if j.ContainerDepositCAD.Known() {
		w.use("container deposit", j.ContainerDepositCAD)
	}
	// Sales tax is computed on the pre-tax portion of a tax-inclusive
	// shelf price, which is how a shop actually prices.
	if j.SalesTaxPct.Known() {
		rate := w.use("sales tax", j.SalesTaxPct)
		r.SalesTaxCAD = r.PriceToBuyerCAD - r.PriceToBuyerCAD/(1+rate)
	}

	switch j.OnSite.Kind {
	case RemittanceMarkup:
		r.MarkupCAD = r.PriceToBuyerCAD * w.use("on-site retail remittance", j.OnSite.Rate)
	case RemittanceFeePerLitre:
		r.MarkupCAD = in.VolumeL() * w.use("on-site retail remittance", j.OnSite.Rate)
	case RemittanceNone:
		// Confirmed nothing is remitted.
	default:
		// Unrecorded. The arithmetic still runs, but the result is an
		// upper bound and the caller must be told.
		//
		// The unknown rate is cited as well as reported missing, so the
		// citation list explains the figure's provenance rather than
		// contradicting it. Going through use() rather than setting
		// w.p directly is what keeps the two in step — the test that
		// compares the weakest citation against the reported provenance
		// is how the discrepancy was found.
		w.use("on-site retail remittance", j.OnSite.Rate)
		r.Missing = append(r.Missing, Missing{
			What: j.Name + " on-site remittance",
			Why:  j.OnSite.Rate.Note,
		})
	}

	r.DistilleryNetCAD = r.PriceToBuyerCAD - r.FederalExciseCAD - r.SalesTaxCAD -
		r.ContainerDepositCAD - r.MarkupCAD
	r.Computable = true
	r.LowestProvenance = w.p
	r.Citations = w.citations
	return r
}

// computeExport prices a sale leaving the province.
//
// No provincial mark-up applies. Federal excise is charged on entry into
// the Canadian market, so a genuine export is relieved of it — which is
// modelled here, and flagged, because it is the assumption that makes
// export look as good as it does. Confirm the treatment for your
// destination before pricing on it: an out-of-province sale within Canada
// is not an export and the receiving province may apply its own mark-up.
func computeExport(in Input, j Jurisdiction) ChannelResult {
	r := ChannelResult{
		Channel:          ChannelExport,
		Computable:       true,
		LandedCostCAD:    in.FOBCAD + in.FreightCAD,
		PriceToBuyerCAD:  in.FOBCAD,
		DistilleryNetCAD: in.FOBCAD,
		LowestProvenance: Sourced,
	}
	r.Missing = append(r.Missing, Missing{
		What: "export duty relief",
		Why: "modelled as relieved of federal excise and free of provincial mark-up. " +
			"An out-of-province sale within Canada is NOT an export — the receiving " +
			"province may apply its own mark-up and duty is still payable. Confirm " +
			"the treatment for your destination.",
	})
	return r
}
