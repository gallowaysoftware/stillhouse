package importer

// The column set for each kind.
//
// Names are what a person would type, not what the database calls
// things: "supplier lot" rather than supplier_lot_id, "abv" rather than
// target_abv_pct. The file is written in a spreadsheet by somebody who
// has never read the schema, and every column that needs explaining is a
// column that gets left blank.
//
// References between kinds are by name, not by id. Nobody has a UUID for
// their rye when they are typing a mash bill into Excel.

var columnsByKind = map[Kind][]Column{
	KindMaterials: {
		{Name: "name", Required: true, Help: "What you call it. Must be unique."},
		{Name: "kind", Required: true, Help: "grain, malt, yeast, ngs, botanical, water, packaging, or other."},
		{Name: "uom", Required: true, Help: "Unit you buy and use it in — kg, L, ea."},
		{Name: "notes", Help: ""},
	},
	KindMaterialLots: {
		{Name: "material", Required: true, Help: "Name of a material that already exists. Import materials first."},
		{Name: "supplier lot", Help: "The supplier's own lot code, for traceability."},
		{Name: "quantity", Required: true, Help: "How much arrived, in the material's own unit."},
		{Name: "unit cost", Help: "Per unit, in CAD. Leave blank if unknown — a blank is reported, a zero would quietly understate inventory."},
		{Name: "received", Help: "ISO date (YYYY-MM-DD). Defaults to today."},
		{Name: "notes", Help: ""},
	},
	KindProducts: {
		{Name: "name", Required: true, Help: "The SKU as it appears on the label."},
		{Name: "spirit kind", Required: true, Help: "canadian whisky, rye whisky, whisky, gin, vodka, rum, brandy, liqueur, or other."},
		{Name: "bottle size ml", Required: true, Help: "750, 375, 1750…"},
		{Name: "abv", Required: true, Help: "Label strength as a percentage — 40, not 0.40."},
		{Name: "label notes", Help: ""},
	},
	KindCustomers: {
		{Name: "name", Required: true, Help: "Must be unique."},
		{Name: "kind", Required: true, Help: "provincial board, licensee, private retail, spirits licensee, export, on-site retail, or other. This decides whether a removal to them is duty-paid."},
		{Name: "jurisdiction", Help: "CA-ON, CA-BC… Leave blank for an export customer outside Canada."},
		{Name: "licence number", Help: "Their excise licence, if they hold one."},
		{Name: "account reference", Help: "Their vendor number for you, or yours for them."},
		{Name: "contact", Help: ""},
		{Name: "email", Help: ""},
		{Name: "phone", Help: ""},
		{Name: "address", Help: ""},
		{Name: "payment terms days", Help: "Net days. Blank means none recorded, which is not the same as 0."},
		{Name: "notes", Help: ""},
	},
	KindBarrels: {
		{Name: "name", Required: true, Help: "Cask number as you call it. Must be unique among containers."},
		{Name: "capacity l", Required: true, Help: "Nominal cask capacity in litres."},
		// The measurement. A bulk import is not the place to do
		// temperature correction: correcting a hydrometer reading needs
		// an instrument, a temperature and a determination trail, and the
		// adopt-existing-stock path exists for exactly that, one cask at
		// a time. Here the operator supplies figures they already hold,
		// and the import records them as uncorrected unless a
		// temperature is given — stated in the report rather than
		// assumed.
		{Name: "volume l", Required: true, Help: "Current contents in litres. See 'temperature c' about correction."},
		{Name: "abv", Required: true, Help: "Current strength as a percentage."},
		{Name: "temperature c", Help: "If your figures are already at 20 °C, leave blank. Otherwise the import records them as uncorrected and says so — use the per-cask adopt path to correct properly."},
		{Name: "fill date", Help: "ISO date. Drives the maturation clock and Canadian Whisky eligibility, so get it right."},
		{Name: "cooperage", Help: ""},
		{Name: "wood species", Help: ""},
		{Name: "char level", Help: ""},
		{Name: "prior use", Help: "e.g. ex-bourbon, virgin, ex-wine."},
		{Name: "serial burnin", Help: "The number burned into the head."},
		{Name: "rickhouse", Help: ""},
		{Name: "row", Help: ""},
		{Name: "level", Help: ""},
		{Name: "column", Help: ""},
		{Name: "notes", Help: ""},
	},
	KindPackaged: {
		{Name: "product", Required: true, Help: "Name of a product that already exists. Import products first."},
		{Name: "lot code", Required: true, Help: "Your own lot or batch code."},
		{Name: "jurisdiction", Required: true, Help: "Where it is stamped for — CA-ON, CA-BC…"},
		{Name: "bottles on hand", Required: true, Help: "Count actually on the shelf now."},
		{Name: "bottled on", Help: "ISO date. Defaults to today."},
		{Name: "notes", Help: ""},
	},
}

// ColumnsFor returns the schema for a kind, or nil if unknown.
func ColumnsFor(k Kind) []Column { return columnsByKind[k] }

// KindHelp is the one-line description of what a file of this kind is
// for, shown above the upload box.
var KindHelp = map[Kind]string{
	KindMaterials:    "Raw materials you buy: grain, yeast, glass, closures.",
	KindMaterialLots: "Deliveries of those materials, with what they cost. Import materials first.",
	KindProducts:     "Finished SKUs — name, bottle size, label strength.",
	KindCustomers:    "Who you sell to. Their kind decides whether a removal to them is duty-paid.",
	KindBarrels:      "Casks already in the rackhouse, with what's in them now.",
	KindPackaged:     "Bottled stock already on the shelf. Import products first.",
}
