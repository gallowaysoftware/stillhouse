package pricing

// A distillery sells the same bottle through channels that price
// completely differently, and modelling them with one formula — as this
// package used to — produces a number that is right for none of them.
type Channel int

const (
	// ChannelWholesale — sale to the provincial liquor board, which
	// applies its own mark-up on top of what it pays. The default route
	// and usually the lowest net to the distiller.
	ChannelWholesale Channel = iota
	// ChannelOnSiteRetail — sale direct to a consumer at the distillery.
	// Permitted in most provinces subject to board policy, and the
	// province frequently still takes a cut.
	ChannelOnSiteRetail
	// ChannelExport — sale leaving the province. No provincial mark-up
	// applies.
	ChannelExport
)

func (c Channel) String() string {
	switch c {
	case ChannelOnSiteRetail:
		return "on-site retail"
	case ChannelExport:
		return "export"
	default:
		return "wholesale"
	}
}

// RemittanceKind describes what, if anything, the province takes from a
// sale the distillery makes in its own shop. This is the question that
// decides whether on-site retail is worth doing, and in both jurisdictions
// researched it is answered by board policy rather than by regulation —
// so Unrecorded is the honest default, not zero.
type RemittanceKind int

const (
	// RemittanceUnrecorded — nobody has established what the province
	// takes. Do not assume it is nothing.
	RemittanceUnrecorded RemittanceKind = iota
	// RemittanceNone — confirmed that the province takes no cut.
	RemittanceNone
	// RemittanceMarkup — the board's wholesale mark-up, or some part of
	// it, is remitted on the distillery's own sales.
	RemittanceMarkup
	// RemittanceFeePerLitre — a flat charge per litre sold.
	RemittanceFeePerLitre
)

func (r RemittanceKind) String() string {
	switch r {
	case RemittanceNone:
		return "none"
	case RemittanceMarkup:
		return "markup remitted"
	case RemittanceFeePerLitre:
		return "per-litre fee"
	default:
		return "unrecorded"
	}
}

// OnSiteRules is what a jurisdiction allows and takes at the distillery door.
type OnSiteRules struct {
	Permitted bool
	Kind      RemittanceKind
	// Rate is a fraction of the retail price for RemittanceMarkup, or CAD
	// per litre for RemittanceFeePerLitre.
	Rate Rate
	// OffSiteOutletFeeCAD is an annual per-outlet licence fee where the
	// province charges one for retail away from the distillery.
	OffSiteOutletFeeCAD Rate
	Note                string
}

// Jurisdiction is one province's rules. Every rate carries its own
// provenance; see rate.go for why.
type Jurisdiction struct {
	Code string // ISO 3166-2, e.g. "CA-ON"
	Name string

	// WholesaleMarkupPctOfLanded is an ad valorem mark-up applied to
	// landed cost (supplier quote + federal excise + duties + freight).
	WholesaleMarkupPctOfLanded Rate
	// WholesalePerLitreCAD is a flat volumetric mark-up instead of an ad
	// valorem one — Alberta's model.
	WholesalePerLitreCAD Rate
	// COSDPerLitreCAD is a cost-of-service differential charged on
	// imported product only, applied after the mark-up.
	COSDPerLitreCAD Rate
	// ProvincialSpiritsTaxPct is a provincial tax distinct from the
	// board's mark-up, where one exists.
	ProvincialSpiritsTaxPct Rate
	// ContainerDeposit is the refundable deposit, banded by container
	// size. Refundable is the operative word: the distillery collects it
	// on the way out and the programme returns it to whoever brings the
	// bottle back, so it passes through the price rather than being a
	// cost of doing business.
	ContainerDeposit DepositSchedule
	// ContainerRecyclingFeeCAD is the stewardship fee — what the
	// programme charges the producer to run the recycling system. Unlike
	// the deposit it is never refunded to anybody, so it is a real per
	// bottle cost and belongs in a landed price. Programmes publish it
	// beside the deposit and by container material, which Stillhouse
	// does not record; see each jurisdiction's Note.
	ContainerRecyclingFeeCAD Rate
	// SalesTaxPct is HST, or GST plus PST combined.
	SalesTaxPct Rate

	OnSite OnSiteRules
	Notes  string
}
