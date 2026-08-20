package pricing

// Jurisdictions is the working set of provincial pricing models.
//
// Read the Provenance on each rate before trusting a number out of this
// file. Most of these markup percentages were carried for years as bare
// constants with no source; they are marked Indicative and are planning
// estimates, not quotes. Where a rate genuinely isn't known, it says
// Unknown and the calculation refuses rather than inventing one.
//
// Adding a jurisdiction: find the board's own published schedule, record
// the URL and the date, and mark it Sourced. If you can only find a trade
// article, mark it Indicative and say so. If you can't find either, add
// the jurisdiction with Unknown rates — a province that shows "we don't
// have this rate" is more useful than one that is silently absent.
var Jurisdictions = []Jurisdiction{
	{
		Code: "CA-ON", Name: "Ontario (LCBO)",
		// Ontario replaced "retail minus discount" with a cost-plus model
		// on 2026-04-01. The spirits mark-up percentage and its ABV tier
		// boundaries are NOT published anywhere generally accessible —
		// they are in LCBO's supplier-facing rate schedule.
		//
		// The old 70% figure this file used to carry described the
		// superseded regime and is therefore not merely unsourced but
		// wrong. Removed rather than retained: a stale number on a
		// pricing page is how a distillery mis-prices a listing.
		WholesaleMarkupPctOfLanded: unknownRate(
			"LCBO spirits mark-up is a fixed percentage of landed cost by ABV tier under the " +
				"cost-plus model in force 2026-04-01. The rate is in LCBO's supplier-facing " +
				"schedule; request it from pricing@lcbo.com or a doingbusinesswithlcbo.com " +
				"account. Do not infer it from the published wine rate — the categories differ."),
		COSDPerLitreCAD: unknownRate(
			"Cost of Service Differential applies volumetrically to imported product only, " +
				"after the mark-up. Not applicable to spirits distilled in Ontario."),
		ProvincialSpiritsTaxPct: unknownRate(
			"Ontario's spirits basic tax is set by the Liquor Tax Act 2019, a separate statute " +
				"from the Liquor Licence and Control Act 2019 (which sets no rates and is not " +
				"yet in force)."),
		ContainerDepositCAD: Rate{
			Value: 0.20, Provenance: Indicative,
			Source: "Ontario Deposit Return Program, standard rate for containers over 630 mL",
			AsOf:   "2026-08-20",
			Note:   "Confirm the rate for your bottle size; smaller containers differ.",
		},
		SalesTaxPct: Rate{
			Value: 0.13, Provenance: Sourced,
			Source: "Ontario HST", AsOf: "2026-08-20",
		},
		OnSite: OnSiteRules{
			Permitted: true,
			Kind:      RemittanceUnrecorded,
			Rate: unknownRate(
				"What Ontario takes on a distillery's own retail sales is board policy. " +
					"Establish it with LCBO/AGCO before relying on an on-site margin."),
			Note: "Minimum Retail Price for all spirits was removed 2025-04-01, so there is no " +
				"longer a statutory floor on what a distillery may charge in its own shop.",
		},
		Notes: "Cost-plus wholesale model in force 2026-04-01: landed cost + mark-up + COSD + " +
			"deposit + HST. From 2026-07-01 an open listing process widens access to " +
			"LCBO-retail-exclusive products, with spirits explicitly carved out. LCBO runs an " +
			"Ontario Small Distillers Direct to LCBO Stores Program.",
	},
	{
		Code: "CA-PE", Name: "Prince Edward Island (PEILCC)",
		WholesaleMarkupPctOfLanded: unknownRate(
			"PEILCC's mark-up varies by category, container size and ABV, and is applied to " +
				"duty-paid landed cost alongside a minimum profit per litre and a minimum " +
				"retail price per category. None of those figures is reliably published; " +
				"Liquor Control Act Regulations s.89 delegates pricing to Commission price " +
				"lists. Ask PEILCC directly."),
		COSDPerLitreCAD: unknownRate("PEILCC applies a cost-of-service charge to imported product."),
		ContainerDepositCAD: Rate{
			Value: 0.10, Provenance: Indicative,
			Source: "PEI beverage container deposit", AsOf: "2026-08-20",
		},
		SalesTaxPct: Rate{
			Value: 0.15, Provenance: Sourced, Source: "PEI HST", AsOf: "2026-08-20",
		},
		OnSite: OnSiteRules{
			Permitted: true,
			Kind:      RemittanceUnrecorded,
			Rate: unknownRate(
				"Distiller's retail outlets operate 'subject to the policies established by " +
					"the Commission' (reg. 50.5(5)). Whether a mark-up is remitted on those " +
					"sales is therefore policy, not regulation."),
			OffSiteOutletFeeCAD: Rate{
				Value: 100, Provenance: Sourced,
				Source: "PEI Liquor Control Act Regulations s.50.5(6)", AsOf: "2024-06-22",
				Note: "Annual fee per off-site retail outlet.",
			},
			Note: "On-premises retail permitted under s.50.5(5); off-site outlets under s.50.5(6).",
		},
		Notes: "Distiller's licence: $300 application, $400 licence, $400 annual renewal " +
			"(reg. s.50.5). Up to 50% of the distillery's spirits may be contract-produced " +
			"elsewhere (s.50.5(4.1)). A farm-winery markup revenue-sharing mechanism exists " +
			"with no spirits equivalent found.",
	},
	{
		Code: "CA-QC", Name: "Québec (SAQ)",
		WholesaleMarkupPctOfLanded: Rate{
			Value: 1.05, Provenance: Indicative,
			Source: "carried in this file since the feature was written; no published schedule located",
			AsOf:   "unknown",
			Note:   "SAQ spirits mark-up commonly exceeds 100% of supplier price. Verify before use.",
		},
		ContainerDepositCAD: Rate{Value: 0.20, Provenance: Indicative, Source: "QC deposit", AsOf: "2026-08-20"},
		SalesTaxPct: Rate{
			Value: 0.14975, Provenance: Sourced, Source: "GST 5% + QST 9.975%", AsOf: "2026-08-20",
		},
		OnSite: OnSiteRules{Permitted: true, Kind: RemittanceUnrecorded,
			Rate: unknownRate("Confirm with SAQ."),
			Note: "On-site distillery sales permitted since 2017. DTC to QC consumers prohibited."},
		Notes: "Every rate here is unverified.",
	},
	{
		Code: "CA-BC", Name: "British Columbia (BCLDB)",
		WholesaleMarkupPctOfLanded: Rate{
			Value: 1.03, Provenance: Indicative,
			Source: "carried in this file since the feature was written; no published schedule located",
			AsOf:   "unknown",
			Note: "BC Craft Distillery designation gets graduated mark-up relief on the first " +
				"100k L if 100% BC inputs — this flat figure does not model that.",
		},
		ContainerDepositCAD: Rate{Value: 0.10, Provenance: Indicative, Source: "BC deposit", AsOf: "2026-08-20"},
		SalesTaxPct: Rate{
			Value: 0.12, Provenance: Sourced, Source: "GST 5% + PST 7%", AsOf: "2026-08-20",
			Note: "BC applies a 10% PST rate to liquor in some channels; confirm which applies.",
		},
		OnSite: OnSiteRules{Permitted: true, Kind: RemittanceUnrecorded,
			Rate: unknownRate("Confirm with BCLDB."),
			Note: "Direct delivery to private retail allowed."},
		Notes: "Every mark-up figure here is unverified.",
	},
	{
		Code: "CA-AB", Name: "Alberta (AGLC)",
		WholesalePerLitreCAD: Rate{
			Value: 13.76, Provenance: Indicative,
			Source: "carried in this file since the feature was written; no published schedule located",
			AsOf:   "unknown",
			Note:   "Approximate weighted average for spirits 22-60% ABV. AGLC publishes a real schedule.",
		},
		ContainerDepositCAD: Rate{Value: 0.25, Provenance: Indicative, Source: "AB deposit", AsOf: "2026-08-20"},
		SalesTaxPct:         Rate{Value: 0.05, Provenance: Sourced, Source: "GST only; no PST", AsOf: "2026-08-20"},
		OnSite: OnSiteRules{Permitted: true, Kind: RemittanceUnrecorded,
			Rate: unknownRate("Confirm with AGLC.")},
		Notes: "Alberta uses a flat per-litre mark-up, not a percentage, so its wholesale " +
			"price does not move with the supplier quote. Privatized retail; Connect Logistics " +
			"distributes.",
	},
	{
		Code: "CA-NS", Name: "Nova Scotia (NSLC)",
		WholesaleMarkupPctOfLanded: Rate{
			Value: 0.85, Provenance: Indicative,
			Source: "carried in this file since the feature was written; no published schedule located",
			AsOf:   "unknown",
		},
		ContainerDepositCAD: Rate{Value: 0.10, Provenance: Indicative, Source: "NS deposit", AsOf: "2026-08-20"},
		SalesTaxPct:         Rate{Value: 0.15, Provenance: Sourced, Source: "NS HST", AsOf: "2026-08-20"},
		OnSite: OnSiteRules{Permitted: true, Kind: RemittanceUnrecorded,
			Rate: unknownRate("Confirm with NSLC.")},
		Notes: "On-site and offsite retail permits available.",
	},
}
