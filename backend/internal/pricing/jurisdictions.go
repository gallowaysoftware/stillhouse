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
		// Banded at 630 mL, which sits just under the 750 mL bottle most
		// distilleries fill: a standard bottle is in the upper band.
		ContainerDeposit: DepositSchedule{
			{MaxML: 630, Rate: Rate{
				Value: 0.10, Provenance: Sourced,
				Source: "Ontario Deposit Return Program (The Beer Store), spirits containers 630 mL and under",
				AsOf:   "2026-08-24",
			}},
			{Rate: Rate{
				Value: 0.20, Provenance: Sourced,
				Source: "Ontario Deposit Return Program (The Beer Store), spirits containers 631 mL and over",
				AsOf:   "2026-08-24",
			}},
		},
		ContainerRecyclingFeeCAD: unknownRate(
			"Ontario runs deposit return and producer stewardship as separate programmes: the " +
				"deposit is ODRP, while the recycling fee is a Circular Materials obligation " +
				"under the Blue Box regulation and is billed on reported supply rather than at a " +
				"published per-container rate. Get yours from your Circular Materials account."),
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
		ContainerDeposit: flatDeposit(Rate{
			Value: 0.10, Provenance: Indicative,
			Source: "PEI beverage container deposit", AsOf: "2026-08-20",
			Note: "Not confirmed against PEI's own schedule; the province charges a " +
				"non-refundable environment fee beside the deposit that this figure does not include.",
		}),
		ContainerRecyclingFeeCAD: unknownRate(
			"PEI charges a non-refundable environmental fee per container in addition to the " +
				"refundable deposit. Confirm the current amount with the PEI Department of " +
				"Environment before pricing a listing there."),
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
		// Quebec is the reason this file grades its rates. The 20¢ carried
		// here before was wrong in the amount and wrong in the direction:
		// glass spirits bottles are not under deposit in Quebec at all
		// yet. The expansion that would have covered them on 2025-03-01
		// was postponed to 2027, while the plastic half went ahead — so
		// the answer depends on what the bottle is made of, which
		// Stillhouse does not record. It refuses rather than guessing,
		// because both possible answers are defensible and one of them is
		// a deposit the distillery never owed.
		ContainerDeposit: flatDeposit(unknownRate(
			"Quebec's answer depends on the container material, which Stillhouse does not " +
				"record. Glass spirits bottles are NOT under deposit: the expansion covering " +
				"them was postponed from 2025-03-01 to 2027 (Consignaction, " +
				"consignaction.ca/en/consignaction-prend-acte-de-la-decision-du-gouvernement-de-reporter-partiellement-lelargissement-de-la-consigne/, " +
				"read 2026-08-24). Plastic spirits containers from 100 mL to 2 L have been " +
				"under deposit at 10¢ since 2025-03-01. If you bottle in glass, your Quebec " +
				"deposit liability today is nil; if in plastic, it is 10¢ a container.")),
		ContainerRecyclingFeeCAD: unknownRate(
			"Quebec's producer obligation runs through Éco Entreprises Québec on reported " +
				"tonnage, not a published per-container rate."),
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
		// One deposit, every size, since 2020-10-12 — BC abolished its
		// bands, so the schedule genuinely has one entry rather than
		// having been left unfinished.
		ContainerDeposit: flatDeposit(Rate{
			Value: 0.10, Provenance: Sourced,
			Source: "Encorp Pacific, return-it.ca/beverage/products/ — \"Liquor Glass, All Sizes\"",
			AsOf:   "2026-08-24",
			Note: "Liquor plastic is also 10¢. Refillable beer bottles and some aluminum " +
				"alcohol containers are BRCCC's rather than Encorp's and are not this rate.",
		}),
		ContainerRecyclingFeeCAD: Rate{
			Value: 0.13, Provenance: Sourced,
			Source: "Encorp Pacific, return-it.ca/beverage/products/ — Container Recycling Fee, Liquor Glass, all sizes",
			AsOf:   "2026-08-24",
			Note: "The glass rate. Liquor plastic is 7¢ and alcohol bag-in-box over 1 L is 30¢; " +
				"Stillhouse does not record container material, so this assumes glass. " +
				"The CRF is not refundable — unlike the deposit it is a real cost per bottle.",
		},
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
		// Banded at 1 L. The flat 25¢ carried here before was the
		// over-1-L rate applied to every size, so a 750 mL bottle — the
		// commonest thing a distillery fills — was reported at two and a
		// half times its actual deposit.
		ContainerDeposit: DepositSchedule{
			{MaxML: 1000, Rate: Rate{
				Value: 0.10, Provenance: Sourced,
				Source: "Beverage Container Management Board, bcmb.ab.ca — regulated minimum refund, containers 1 L and under",
				AsOf:   "2026-08-24",
			}},
			{Rate: Rate{
				Value: 0.25, Provenance: Sourced,
				Source: "Beverage Container Management Board, bcmb.ab.ca — regulated minimum refund, containers over 1 L",
				AsOf:   "2026-08-24",
			}},
		},
		ContainerRecyclingFeeCAD: unknownRate(
			"Alberta charges a Container Recycling Fee set per container type by the ABCRC. " +
				"It is not the deposit and is not refunded. Get your rate from abcrc.com."),
		SalesTaxPct: Rate{Value: 0.05, Provenance: Sourced, Source: "GST only; no PST", AsOf: "2026-08-20"},
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
		ContainerDeposit: flatDeposit(Rate{
			Value: 0.10, Provenance: Indicative,
			Source: "NS beverage container deposit", AsOf: "2026-08-20",
			Note: "Not confirmed against Divert NS's own schedule. Nova Scotia runs a " +
				"half-back system — the consumer is refunded half what was paid — so the " +
				"deposit collected and the deposit refunded are not the same number.",
		}),
		ContainerRecyclingFeeCAD: unknownRate(
			"Confirm Divert NS's producer obligation; not modelled."),
		SalesTaxPct: Rate{Value: 0.15, Provenance: Sourced, Source: "NS HST", AsOf: "2026-08-20"},
		OnSite: OnSiteRules{Permitted: true, Kind: RemittanceUnrecorded,
			Rate: unknownRate("Confirm with NSLC.")},
		Notes: "On-site and offsite retail permits available.",
	},
}

// Known reports whether code names a jurisdiction this package carries
// rates for. Used to keep an unrecognised province out of a customer or
// a price list at the point it is typed, rather than discovering it when
// a pricing calculation quietly has nothing to work with.
func Known(code string) bool {
	for _, j := range Jurisdictions {
		if j.Code == code {
			return true
		}
	}
	return false
}

// Find returns the jurisdiction for a code, or nil.
//
// A pointer rather than a value-and-bool because the caller's next move
// on nil is always to report that it has no rates rather than to
// substitute defaults, and a zero Jurisdiction would look like one whose
// every rate is Unknown — which is nearly the same thing but reads as a
// jurisdiction we carry.
func Find(code string) *Jurisdiction {
	for i := range Jurisdictions {
		if Jurisdictions[i].Code == code {
			return &Jurisdictions[i]
		}
	}
	return nil
}
