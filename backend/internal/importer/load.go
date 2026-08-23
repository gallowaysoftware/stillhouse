package importer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// Result is what an import did, or would have done.
type Result struct {
	Kind Kind
	// Rows read from the file, excluding the header and blank lines.
	RowsRead int
	// Rows that would be (or were) created.
	RowsAccepted int
	Problems     []Problem
	// Notes are things worth saying that are not failures — a strength
	// recorded uncorrected, a lot with no price. The import proceeds; the
	// operator should still know.
	Notes []string
	// Committed is false for a dry run, and false for a commit that was
	// abandoned because a row failed.
	Committed bool
}

// Load validates and writes every row. Always writes — the caller
// decides whether to keep the transaction.
//
// That is the whole design. A dry run that only validates cannot see the
// case that matters most: a perfectly well-formed file whose names
// collide with rows already in the database. Nothing short of attempting
// the INSERT knows that. So the dry run does attempt it, inside a
// transaction the caller then abandons, and a dry run and a commit see
// identical problems by construction rather than by two code paths
// agreeing.
//
// It also means a failure part-way leaves nothing behind: there is no
// half-imported state and no rollback step for anyone to remember.
func Load(
	ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID,
	kind Kind, rows []Row, commit bool,
) (*Result, error) {
	res := &Result{Kind: kind, RowsRead: len(rows)}
	var load func(context.Context, *sqlcgen.Queries, uuid.UUID, []Row, *Result) error
	switch kind {
	case KindMaterials:
		load = loadMaterials
	case KindMaterialLots:
		load = loadMaterialLots
	case KindProducts:
		load = loadProducts
	case KindCustomers:
		load = loadCustomers
	case KindBarrels:
		load = loadBarrels
	case KindPackaged:
		load = loadPackaged
	default:
		return nil, fmt.Errorf("unknown import kind %q", kind)
	}
	if err := load(ctx, q, tenantID, rows, res); err != nil {
		return nil, err
	}
	// A file with any problem in it does not import. Partial imports are
	// the thing that makes people distrust importers: half the casks
	// arrive, nobody knows which half, and the second attempt duplicates
	// the first.
	if len(res.Problems) > 0 {
		res.Committed = false
		res.RowsAccepted = 0
		return res, nil
	}
	res.Committed = commit
	return res, nil
}

func loadMaterials(
	ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID,
	rows []Row, res *Result,
) error {
	seen := map[string]int{}
	for _, r := range rows {
		name := r.Get("name")
		if prev, dup := seen[strings.ToLower(name)]; dup {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "name",
				Detail: fmt.Sprintf("%q also appears on row %d", name, prev)})
			continue
		}
		seen[strings.ToLower(name)] = r.Line

		kind, err := materialKind(r.Get("kind"))
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "kind", Detail: err.Error()})
			continue
		}
		if _, err := q.CreateMaterial(ctx, sqlcgen.CreateMaterialParams{
			TenantID: tenantID, Name: name, Kind: kind,
			Uom: r.Get("uom"), Notes: r.Get("notes"),
		}); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "material")})
			continue
		}
		res.RowsAccepted++
	}
	return nil
}

func loadMaterialLots(
	ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID,
	rows []Row, res *Result,
) error {
	byName, err := materialsByName(ctx, q)
	if err != nil {
		return err
	}
	var unpriced int
	for _, r := range rows {
		mat, ok := byName[strings.ToLower(r.Get("material"))]
		if !ok {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "material",
				Detail: fmt.Sprintf("no material called %q — import materials first", r.Get("material"))})
			continue
		}
		qty, present, err := r.Float("quantity")
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "quantity", Detail: err.Error()})
			continue
		}
		if !present || qty <= 0 {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "quantity",
				Detail: "must be greater than zero"})
			continue
		}
		var cost pgtype.Float8
		if v, present, err := r.Float("unit cost"); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "unit cost", Detail: err.Error()})
			continue
		} else if present {
			if v < 0 {
				res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "unit cost",
					Detail: "cannot be negative"})
				continue
			}
			cost = pgtype.Float8{Float64: v, Valid: true}
		} else {
			unpriced++
		}
		received, err := optionalDate(r.Get("received"))
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "received", Detail: err.Error()})
			continue
		}
		if _, err := q.CreateMaterialLot(ctx, sqlcgen.CreateMaterialLotParams{
			TenantID: tenantID, MaterialID: mat, SupplierLot: r.Get("supplier lot"),
			QuantityReceived: qty,
			ReceivedAt:       pgtype.Timestamptz{Valid: true, Time: received},
			Notes:            r.Get("notes"), UnitCostCad: cost,
		}); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "material lot")})
			continue
		}
		res.RowsAccepted++
	}
	if unpriced > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"%d lot%s have no unit cost. They import fine, but they contribute nothing to "+
				"inventory value or cost of sales until a price is recorded — the accounting "+
				"journal reports them as unpriced rather than as zero.",
			unpriced, plural(unpriced)))
	}
	return nil
}

func loadProducts(
	ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID,
	rows []Row, res *Result,
) error {
	for _, r := range rows {
		kind, err := spiritKind(r.Get("spirit kind"))
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "spirit kind", Detail: err.Error()})
			continue
		}
		size, present, err := r.Int("bottle size ml")
		if err != nil || !present || size <= 0 {
			detail := "must be a whole number of millilitres greater than zero"
			if err != nil {
				detail = err.Error()
			}
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "bottle size ml", Detail: detail})
			continue
		}
		abv, present, err := r.Float("abv")
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "abv", Detail: err.Error()})
			continue
		}
		if !present || abv <= 0 || abv > 100 {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "abv",
				Detail: "must be a percentage between 0 and 100 — write 40, not 0.40"})
			continue
		}
		gtin := r.Get("gtin")
		if gtin != "" {
			if err := ValidateGTIN(gtin); err != nil {
				res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "gtin", Detail: err.Error()})
				continue
			}
		}
		perCase, _, err := r.Int("bottles per case")
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "bottles per case", Detail: err.Error()})
			continue
		}
		perLayer, _, err := r.Int("cases per layer")
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "cases per layer", Detail: err.Error()})
			continue
		}
		layers, _, err := r.Int("layers per pallet")
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "layers per pallet", Detail: err.Error()})
			continue
		}
		caseWeight, _, err := r.Float("case weight kg")
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "case weight kg", Detail: err.Error()})
			continue
		}

		created, err := q.CreateProduct(ctx, sqlcgen.CreateProductParams{
			TenantID: tenantID, Name: r.Get("name"), SpiritKind: kind,
			BottleSizeMl: int32(size), TargetAbvPct: abv, LabelNotes: r.Get("label notes"),
		})
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "product")})
			continue
		}
		if _, err := q.UpdateProductSKU(ctx, sqlcgen.UpdateProductSKUParams{
			ID: created.ID, Gtin: gtin, CspcCode: r.Get("cspc code"),
			BottlesPerCase:    positiveInt4(perCase),
			CasesPerLayer:     positiveInt4(perLayer),
			LayersPerPallet:   positiveInt4(layers),
			CaseGrossWeightKg: positiveFloat8(caseWeight),
			CommonName:        r.Get("common name"),
			AgeStatement:      r.Get("age statement"),
			ContainerMarking:  r.Get("container marking"),
			AllergenStatement: r.Get("allergen statement"),
			CountryOfOrigin:   r.Get("country of origin"),
		}); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "product's trade details")})
			continue
		}
		res.RowsAccepted++
	}
	return nil
}

func loadCustomers(
	ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID,
	rows []Row, res *Result,
) error {
	for _, r := range rows {
		kind, dest, err := customerKind(r.Get("kind"))
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "kind", Detail: err.Error()})
			continue
		}
		var terms pgtype.Int4
		if v, present, err := r.Int("payment terms days"); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "payment terms days", Detail: err.Error()})
			continue
		} else if present {
			if v < 0 {
				res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "payment terms days",
					Detail: "cannot be negative"})
				continue
			}
			terms = pgtype.Int4{Int32: int32(v), Valid: true}
		}
		if _, err := q.CreateCustomer(ctx, sqlcgen.CreateCustomerParams{
			TenantID: tenantID, Name: r.Get("name"), Kind: kind,
			Jurisdiction: r.Get("jurisdiction"), DefaultDestinationKind: dest,
			LicenceNumber: r.Get("licence number"), AccountReference: r.Get("account reference"),
			ContactName: r.Get("contact"), Email: r.Get("email"), Phone: r.Get("phone"),
			Address: r.Get("address"), PaymentTermsDays: terms, Notes: r.Get("notes"),
		}); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "customer")})
			continue
		}
		res.RowsAccepted++
	}
	return nil
}

// loadBarrels creates the cask, its attributes and its opening balance.
//
// The strength is recorded as uncorrected unless a temperature is given,
// and the report says so. Correcting a hydrometer reading properly needs
// an instrument and a determination trail, which is what the per-cask
// adopt path is for; a spreadsheet of four hundred casks is somebody
// transcribing figures they already hold, and pretending those went
// through the tables would be a worse lie than saying they did not.
func loadBarrels(
	ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID,
	rows []Row, res *Result,
) error {
	var uncorrected int
	for _, r := range rows {
		capacity, present, err := r.Float("capacity l")
		if err != nil || !present || capacity <= 0 {
			detail := "must be a number of litres greater than zero"
			if err != nil {
				detail = err.Error()
			}
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "capacity l", Detail: detail})
			continue
		}
		volume, present, err := r.Float("volume l")
		if err != nil || !present || volume < 0 {
			detail := "must be a number of litres, zero or more"
			if err != nil {
				detail = err.Error()
			}
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "volume l", Detail: detail})
			continue
		}
		if volume > capacity {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "volume l",
				Detail: fmt.Sprintf("%.1f L is more than the cask's %.1f L capacity", volume, capacity)})
			continue
		}
		abv, present, err := r.Float("abv")
		if err != nil || !present || abv < 0 || abv > 100 {
			detail := "must be a percentage between 0 and 100"
			if err != nil {
				detail = err.Error()
			}
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "abv", Detail: detail})
			continue
		}
		var charLevel pgtype.Int4
		if v, present, err := r.Int("char level"); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "char level", Detail: err.Error()})
			continue
		} else if present {
			if v < 1 || v > 4 {
				res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "char level",
					Detail: "must be 1 to 4"})
				continue
			}
			charLevel = pgtype.Int4{Int32: int32(v), Valid: true}
		}
		if _, present, err := r.Float("temperature c"); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "temperature c", Detail: err.Error()})
			continue
		} else if !present {
			uncorrected++
		}
		fillDate, err := optionalDateOrZero(r.Get("fill date"))
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "fill date", Detail: err.Error()})
			continue
		}

		container, err := q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
			TenantID: tenantID, Name: r.Get("name"), Kind: sqlcgen.BulkContainerKindBarrel,
			CapacityL: pgtype.Float8{Float64: capacity, Valid: true},
			Location:  r.Get("rickhouse"), Notes: r.Get("notes"),
		})
		if err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "cask")})
			continue
		}
		if _, err := q.CreateBarrelAttributes(ctx, sqlcgen.CreateBarrelAttributesParams{
			ContainerID: container.ID, TenantID: tenantID,
			CooperageSupplier: r.Get("cooperage"), CharLevel: charLevel,
			WoodSpecies: r.Get("wood species"), PriorUse: r.Get("prior use"),
			SerialBurnin: r.Get("serial burnin"), Rickhouse: r.Get("rickhouse"),
			RowPosition: r.Get("row"), LevelPosition: r.Get("level"),
			ColumnPosition: r.Get("column"),
		}); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "cask attributes")})
			continue
		}
		if fillDate.Valid {
			if err := q.SetBarrelFillDate(ctx, sqlcgen.SetBarrelFillDateParams{
				ContainerID: container.ID, FillDate: fillDate,
			}); err != nil {
				res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "fill date",
					Detail: friendlyWriteErr(err, "fill date")})
				continue
			}
		}
		if volume > 0 {
			if _, err := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
				ID: container.ID, CurrentVolumeL: volume,
				CurrentAbvPct: pgtype.Float8{Float64: abv, Valid: true},
				CurrentLaa:    volume * abv / 100,
			}); err != nil {
				res.Problems = append(res.Problems, Problem{Row: r.Line,
					Detail: friendlyWriteErr(err, "opening balance")})
				continue
			}
		}
		res.RowsAccepted++
	}
	if uncorrected > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"%d cask%s gave no temperature, so their strengths are recorded as supplied and "+
				"flagged uncorrected. If they were not already at 20 °C, regauge them through "+
				"the cask screen, which records the instrument and the correction.",
			uncorrected, plural(uncorrected)))
	}
	return nil
}

func loadPackaged(
	ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID,
	rows []Row, res *Result,
) error {
	byName, err := productsByName(ctx, q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		prod, ok := byName[strings.ToLower(r.Get("product"))]
		if !ok {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "product",
				Detail: fmt.Sprintf("no product called %q — import products first", r.Get("product"))})
			continue
		}
		bottles, present, err := r.Int("bottles on hand")
		if err != nil || !present || bottles < 0 {
			detail := "must be a whole number, zero or more"
			if err != nil {
				detail = err.Error()
			}
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "bottles on hand", Detail: detail})
			continue
		}
		// Validated but not stored: packaged_inventory takes its date
		// from the bottling run behind it, and adopted stock has none.
		// Rejecting a malformed date is still worth doing — a file with
		// "12/03/26" in it has other problems.
		if _, err := optionalDate(r.Get("bottled on")); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line, Column: "bottled on", Detail: err.Error()})
			continue
		}
		if _, err := q.CreatePackagedInventoryAdopted(ctx, sqlcgen.CreatePackagedInventoryAdoptedParams{
			TenantID: tenantID, ProductID: prod, LotCode: r.Get("lot code"),
			Jurisdiction: r.Get("jurisdiction"), BottlesOnHand: int32(bottles),
		}); err != nil {
			res.Problems = append(res.Problems, Problem{Row: r.Line,
				Detail: friendlyWriteErr(err, "packaged lot")})
			continue
		}
		res.RowsAccepted++
	}
	res.Notes = append(res.Notes, "Imported packaged stock has no bottling run behind it, so it "+
		"carries no material cost. Removals from these lots appear in the accounting journal "+
		"as unvalued rather than at a made-up figure.")
	return nil
}

// --- lookups and parsing ---------------------------------------------------

func materialsByName(ctx context.Context, q *sqlcgen.Queries) (map[string]uuid.UUID, error) {
	rows, err := q.ListMaterials(ctx, sqlcgen.ListMaterialsParams{IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(rows))
	for _, m := range rows {
		out[strings.ToLower(m.Name)] = m.ID
	}
	return out, nil
}

func productsByName(ctx context.Context, q *sqlcgen.Queries) (map[string]uuid.UUID, error) {
	rows, err := q.ListProducts(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(rows))
	for _, p := range rows {
		out[strings.ToLower(p.Name)] = p.ID
	}
	return out, nil
}

func optionalDate(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Now().UTC(), nil
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not an ISO date (YYYY-MM-DD)", s)
	}
	return t, nil
}

func optionalDateOrZero(s string) (pgtype.Date, error) {
	if strings.TrimSpace(s) == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("%q is not an ISO date (YYYY-MM-DD)", s)
	}
	if t.After(time.Now()) {
		return pgtype.Date{}, fmt.Errorf("%s is in the future", s)
	}
	return pgtype.Date{Valid: true, Time: t}, nil
}

// friendlyWriteErr turns a database error into something an operator can
// act on. A UNIQUE violation on an import almost always means the row is
// already there, which is a different problem from a broken file.
func friendlyWriteErr(err error, what string) string {
	msg := err.Error()
	if strings.Contains(msg, "duplicate key") || strings.Contains(msg, "23505") {
		return fmt.Sprintf("a %s with that name already exists — remove it from the file, "+
			"or rename it", what)
	}
	return fmt.Sprintf("could not create the %s: %s", what, msg)
}

func positiveInt4(v int64) pgtype.Int4 {
	if v <= 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(v), Valid: true}
}

func positiveFloat8(v float64) pgtype.Float8 {
	if v <= 0 {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: v, Valid: true}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
