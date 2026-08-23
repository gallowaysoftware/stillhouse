// Package journal turns a period's movements into double-entry lines an
// accountant can import.
//
// This is the seam that lets Stillhouse not become an accounting
// package. It knows what happened and what it was worth — duty
// crystallised on a run, grain received at a lot cost, materials
// consumed into a mash, stock removed. What it does not know, and must
// not guess, is which account each belongs in: that is the licensee's
// chart of accounts, and every distillery's is different.
//
// So the mapping is data the operator supplies, and an event with no
// mapping comes back as unmapped rather than posted to an invented
// account. The same discipline as an excise rate the table cannot cite —
// refuse to produce the number rather than produce a plausible one. A
// journal line in the wrong account is worse than a missing one, because
// it reconciles and nobody looks again.
package journal

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/costing"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// Line is one side-pair of a journal entry: a single event, its amount,
// and the accounts it was mapped to. Debit and credit are equal by
// construction — every event here moves one amount between two accounts,
// which is why a Line carries one amount rather than two.
type Line struct {
	Date        time.Time
	Kind        sqlcgen.JournalEventKind
	Description string
	Reference   string
	AmountCAD   float64
	Debit       string
	DebitName   string
	Credit      string
	CreditName  string
	Memo        string
	// Basis names how the amount was arrived at, in words, on every line.
	// A cost that came from a weighted average is not the same number as
	// one that came from an invoice, and an accountant importing this
	// needs to know which they are looking at without asking.
	Basis string
}

// Warning is something the export could not do, said out loud. These are
// not errors — the export still runs — but every one of them means a
// figure somebody will reconcile against is incomplete, so they travel
// with the data rather than in a log.
type Warning struct {
	Kind   string
	Detail string
}

// Journal is a period's worth of lines plus everything that could not be
// turned into one.
type Journal struct {
	PeriodStart time.Time
	PeriodEnd   time.Time
	Lines       []Line
	Warnings    []Warning
	// Notes are true statements about how the figures were arrived at,
	// as against Warnings, which are things that could not be done.
	Notes []string
	// TotalsByKind is what each kind contributed, for the summary at the
	// top of the export and for a quick eyeball against the B266.
	TotalsByKind map[sqlcgen.JournalEventKind]float64
}

// Build computes the journal for a period.
func Build(
	ctx context.Context, q *sqlcgen.Queries, start, end time.Time,
) (*Journal, error) {
	accounts, err := q.ListJournalAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read account mapping: %w", err)
	}
	mapping := make(map[sqlcgen.JournalEventKind]sqlcgen.JournalAccount, len(accounts))
	for _, a := range accounts {
		mapping[a.Kind] = a
	}

	j := &Journal{
		PeriodStart:  start,
		PeriodEnd:    end,
		TotalsByKind: map[sqlcgen.JournalEventKind]float64{},
	}
	startDate := pgtype.Date{Valid: true, Time: start}
	endDate := pgtype.Date{Valid: true, Time: end}

	if err := j.addDuty(ctx, q, mapping, startDate, endDate); err != nil {
		return nil, err
	}
	if err := j.addMaterialReceipts(ctx, q, mapping, start, end); err != nil {
		return nil, err
	}
	if err := j.addMaterialConsumption(ctx, q, mapping, startDate, endDate); err != nil {
		return nil, err
	}
	if err := j.addCOGS(ctx, q, mapping, startDate, endDate); err != nil {
		return nil, err
	}
	if err := j.addWIPTransfer(ctx, q, mapping, startDate, endDate); err != nil {
		return nil, err
	}

	// One warning per kind that produced lines but has no mapping, so the
	// operator is told once rather than per row.
	for kind := range j.TotalsByKind {
		if m, ok := mapping[kind]; !ok || m.DebitAccount == "" || m.CreditAccount == "" {
			j.Warnings = append(j.Warnings, Warning{
				Kind: string(kind),
				Detail: "no account mapping, so these lines carry no accounts. " +
					"Set them under Settings → Accounting before importing.",
			})
		}
	}
	return j, nil
}

func (j *Journal) add(l Line, m sqlcgen.JournalAccount) {
	l.Debit, l.DebitName = m.DebitAccount, m.DebitName
	l.Credit, l.CreditName = m.CreditAccount, m.CreditName
	if m.MemoPrefix != "" {
		l.Memo = m.MemoPrefix + " " + l.Memo
	}
	j.Lines = append(j.Lines, l)
	j.TotalsByKind[l.Kind] += l.AmountCAD
}

// addDuty is the line that most justifies the whole export: an exact
// amount Stillhouse calculated itself, against a real liability, on a
// date the return also uses.
func (j *Journal) addDuty(
	ctx context.Context, q *sqlcgen.Queries,
	mapping map[sqlcgen.JournalEventKind]sqlcgen.JournalAccount,
	start, end pgtype.Date,
) error {
	rows, err := q.JournalDutyEvents(ctx, sqlcgen.JournalDutyEventsParams{
		PeriodStart: start, PeriodEnd: end,
	})
	if err != nil {
		return fmt.Errorf("duty events: %w", err)
	}
	m := mapping[sqlcgen.JournalEventKindDutyPayable]
	for _, r := range rows {
		amount := 0.0
		if r.AmountCad.Valid {
			amount = r.AmountCad.Float64
		}
		if amount == 0 {
			continue
		}
		basis := "duty crystallised at packaging"
		if r.Source == "removal" {
			basis = "duty crystallised on removal"
		}
		j.add(Line{
			Date:        r.EventDate.Time,
			Kind:        sqlcgen.JournalEventKindDutyPayable,
			Description: r.Description,
			Reference:   r.Reference,
			AmountCAD:   round2(amount),
			Memo:        r.Description + " " + r.Reference,
			Basis:       basis,
		}, m)
	}
	return nil
}

func (j *Journal) addMaterialReceipts(
	ctx context.Context, q *sqlcgen.Queries,
	mapping map[sqlcgen.JournalEventKind]sqlcgen.JournalAccount,
	start, end time.Time,
) error {
	rows, err := q.JournalMaterialReceipts(ctx, sqlcgen.JournalMaterialReceiptsParams{
		PeriodStart: pgtype.Timestamptz{Valid: true, Time: start},
		// Exclusive upper bound on a timestamp column, so a receipt at
		// 14:00 on the last day is inside the period rather than lost to
		// a date-versus-timestamp comparison.
		PeriodEnd: pgtype.Timestamptz{Valid: true, Time: end.AddDate(0, 0, 1)},
	})
	if err != nil {
		return fmt.Errorf("material receipts: %w", err)
	}
	m := mapping[sqlcgen.JournalEventKindMaterialReceipt]
	var unpriced, landedCount int
	for _, r := range rows {
		if !r.UnitCostCad.Valid {
			// Not zero. A lot with no recorded cost contributes no line,
			// and is counted into a warning — posting it at zero would
			// silently understate inventory and balance perfectly.
			unpriced++
			continue
		}
		// Landed cost, not the supplier's price: freight, duty and
		// handling belong in the value of the inventory rather than in
		// an expense account. The basis says which, because posting
		// 0.80/kg when the invoice says 0.55/kg is correct and needs to
		// be explicable to whoever reconciles the freight bill.
		unit := r.UnitCostCad.Float64
		basis := "recorded lot cost × quantity received"
		if r.LandedUnitCostCad.Valid {
			unit = r.LandedUnitCostCad.Float64
			if unit != r.UnitCostCad.Float64 {
				landedCount++
				basis = "landed cost (supplier price plus freight, duty and handling, " +
					"spread over the quantity) × quantity received"
			}
		}
		j.add(Line{
			Date:        r.ReceivedAt.Time,
			Kind:        sqlcgen.JournalEventKindMaterialReceipt,
			Description: r.MaterialName,
			Reference:   r.SupplierLot,
			AmountCAD:   round2(r.QuantityReceived * unit),
			Memo: fmt.Sprintf("%s %.3f %s @ %.4f",
				r.MaterialName, r.QuantityReceived, r.Uom, unit),
			Basis: basis,
		}, m)
	}
	if unpriced > 0 {
		j.Warnings = append(j.Warnings, Warning{
			Kind: string(sqlcgen.JournalEventKindMaterialReceipt),
			Detail: fmt.Sprintf(
				"%d material lot%s received in this period have no unit cost recorded and "+
					"produced no line. Inventory is understated by whatever they cost.",
				unpriced, plural(unpriced)),
		})
	}
	if landedCount > 0 {
		// Not a warning — this is the correct treatment — but somebody
		// reconciling against supplier invoices will find the totals do
		// not match, and should know why before they go looking.
		j.Notes = append(j.Notes, fmt.Sprintf(
			"%d receipt%s are posted at landed cost rather than the supplier's price, "+
				"because freight, duty and handling belong in the value of the inventory. "+
				"These lines will not tie to the supplier invoices on their own.",
			landedCount, plural(landedCount)))
	}
	return nil
}

func (j *Journal) addMaterialConsumption(
	ctx context.Context, q *sqlcgen.Queries,
	mapping map[sqlcgen.JournalEventKind]sqlcgen.JournalAccount,
	start, end pgtype.Date,
) error {
	rows, err := q.JournalMaterialConsumption(ctx, sqlcgen.JournalMaterialConsumptionParams{
		PeriodStart: start, PeriodEnd: end,
	})
	if err != nil {
		return fmt.Errorf("material consumption: %w", err)
	}
	m := mapping[sqlcgen.JournalEventKindMaterialConsumption]
	var unpriced int
	for _, r := range rows {
		if !r.UnitCostCad.Valid {
			unpriced++
			continue
		}
		j.add(Line{
			Date:        r.MashDate.Time,
			Kind:        sqlcgen.JournalEventKindMaterialConsumption,
			Description: r.MaterialName,
			Reference:   fmt.Sprintf("mash %d", r.MashNo),
			AmountCAD:   round2(r.QuantityUsed * r.UnitCostCad.Float64),
			Memo: fmt.Sprintf("%s %.3f %s into mash %d",
				r.MaterialName, r.QuantityUsed, r.Uom, r.MashNo),
			Basis: "the lot's own cost × quantity used",
		}, m)
	}
	if unpriced > 0 {
		j.Warnings = append(j.Warnings, Warning{
			Kind: string(sqlcgen.JournalEventKindMaterialConsumption),
			Detail: fmt.Sprintf(
				"%d mash ingredient line%s could not be valued — either no lot was linked "+
					"or the lot has no recorded cost.", unpriced, plural(unpriced)),
		})
	}
	return nil
}

// addCOGS values stock leaving at the average material cost of the run
// it came from.
//
// The basis is stated on every line because it is a real accounting
// choice, not a detail: this is direct materials only, at the run's own
// cost, with no labour or overhead in it — because Stillhouse has none
// (see E4). An accountant importing this needs to know that without
// having to ask, which is why Basis is a column rather than a footnote.
func (j *Journal) addCOGS(
	ctx context.Context, q *sqlcgen.Queries,
	mapping map[sqlcgen.JournalEventKind]sqlcgen.JournalAccount,
	start, end pgtype.Date,
) error {
	rows, err := q.JournalRemovalsForCOGS(ctx, sqlcgen.JournalRemovalsForCOGSParams{
		PeriodStart: start, PeriodEnd: end,
	})
	if err != nil {
		return fmt.Errorf("removals: %w", err)
	}
	m := mapping[sqlcgen.JournalEventKindCogsOnRemoval]

	// Cost of sales presumes the goods were ours to sell. Spirits owned by
	// a customer and merely matured, packaged and shipped by the licensee
	// were never on the licensee's balance sheet — the revenue is a
	// service fee and there is no cost of sales at all.
	//
	// The chain from a removal back to whoever owned the bulk it came from
	// exists (removal → lot → bottling run → source container), but the
	// container carries only its *current* owner, and a cask sold in place
	// last quarter would restate a period already closed. Stillhouse
	// therefore does not guess. It says so instead, once, when the tenant
	// has any third-party spirits at all — because an accountant importing
	// this needs to know the figure may include somebody else's goods, and
	// a silently overstated cost of sales is exactly the kind of error
	// that survives an audit by looking reasonable. See PLAN D8.
	if n, ce := q.CountThirdPartyBulkContainers(ctx); ce == nil && n > 0 {
		j.Warnings = append(j.Warnings, Warning{
			Kind: string(sqlcgen.JournalEventKindCogsOnRemoval),
			Detail: fmt.Sprintf(
				"%d bulk container%s %s owned by a customer. Cost of sales below values "+
					"every removal as if the goods were yours; ownership of packaged "+
					"stock is not modelled yet, so any contract-packaged removal in "+
					"this period is overstated here and its revenue is a service fee, "+
					"not a sale.", n, plural(int(n)), wasWere(int(n))),
		})
	} else if ce != nil {
		return fmt.Errorf("third-party containers: %w", ce)
	}

	// Cost per run is looked up once and reused, because several removals
	// commonly come off one run.
	costPerBottle := map[uuid.UUID]float64{}
	var unvalued int
	for _, r := range rows {
		if !r.BottlingRunID.Valid || !r.RunBottleCount.Valid || r.RunBottleCount.Int32 <= 0 {
			// Adopted stock, or a backfilled lot with no run behind it.
			// There is no cost to attribute and inventing one would be
			// worse than the gap.
			unvalued++
			continue
		}
		runID := r.BottlingRunID.UUID
		per, ok := costPerBottle[runID]
		if !ok {
			// Same walk the cost screen uses — see internal/costing.
			cost, err := costing.BottlingRunMaterialCost(ctx, q, runID)
			if err != nil {
				return fmt.Errorf("run cost: %w", err)
			}
			per = cost.PerBottleCAD()
			costPerBottle[runID] = per
		}
		if per <= 0 {
			unvalued++
			continue
		}
		j.add(Line{
			Date:        r.RemovalDate.Time,
			Kind:        sqlcgen.JournalEventKindCogsOnRemoval,
			Description: r.ProductName,
			Reference:   r.DestinationName,
			AmountCAD:   round2(per * float64(r.BottlesRemoved)),
			Memo: fmt.Sprintf("%d × %s to %s",
				r.BottlesRemoved, r.ProductName, r.DestinationName),
			Basis: "direct materials only, at the bottling run's own cost; no labour or overhead",
		}, m)
	}
	if unvalued > 0 {
		j.Warnings = append(j.Warnings, Warning{
			Kind: string(sqlcgen.JournalEventKindCogsOnRemoval),
			Detail: fmt.Sprintf(
				"%d removal%s could not be valued: no bottling run behind the lot, or the "+
					"run's materials have no recorded cost. Cost of sales is understated.",
				unvalued, plural(unvalued)),
		})
	}
	return nil
}

// round2 keeps amounts at cents. Journal lines are money and a
// fifteen-decimal amount in a CSV is a support ticket.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func wasWere(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// addWIPTransfer books the movement out of work in progress and into
// finished goods when a run is bottled.
//
// Migration 000040 left this kind unemitted on purpose and wrote down
// why: valuing it needs labour, overhead absorption and a WIP convention
// Stillhouse did not have, and "an export that posted a made-up WIP
// figure would reconcile, and nobody would look at it again." Stage 178
// gave it the first two. The convention is the third, and it is stated
// rather than assumed: WIP is carried at the full cost of the run that
// drew it, so bottling moves exactly what that run cost — no more, and
// nothing manufactured to make the two sides meet.
//
// A run whose cost is incomplete produces no line at all. A partial
// figure posted as if it were whole is the failure 000040 was avoiding;
// the warning says which runs and why.
func (j *Journal) addWIPTransfer(
	ctx context.Context, q *sqlcgen.Queries,
	mapping map[sqlcgen.JournalEventKind]sqlcgen.JournalAccount,
	start, end pgtype.Date,
) error {
	runs, err := q.BottlingRunsInPeriodForWIP(ctx, sqlcgen.BottlingRunsInPeriodForWIPParams{
		PeriodStart: start, PeriodEnd: end,
	})
	if err != nil {
		return fmt.Errorf("bottling runs: %w", err)
	}
	m := mapping[sqlcgen.JournalEventKindWipToFinishedGoods]

	skipped := 0
	var firstWhy string
	for _, r := range runs {
		cost, err := costing.BottlingRunFullCost(ctx, q, r.ID)
		if err != nil {
			return fmt.Errorf("cost run %s: %w", r.LotCode, err)
		}
		if cost.TotalCAD <= 0 {
			skipped++
			if firstWhy == "" {
				firstWhy = "nothing about the run could be priced"
			}
			continue
		}
		basis := "direct materials only"
		switch {
		case cost.Labour.Available && cost.Overhead.Available:
			basis = "direct materials, labour and absorbed overhead"
		case cost.Labour.Available:
			basis = "direct materials and labour; no overhead rate is set"
		case cost.Overhead.Available:
			basis = "direct materials and absorbed overhead; no hours were recorded"
		}
		j.add(Line{
			Date:        r.BottlingDate.Time,
			Kind:        sqlcgen.JournalEventKindWipToFinishedGoods,
			Description: r.ProductName,
			Reference:   r.LotCode,
			AmountCAD:   round2(cost.TotalCAD),
			Memo: fmt.Sprintf("%s: %d bottles from %.4f LAA",
				r.LotCode, r.BottleCount, r.TankGaugeLaa),
			Basis: basis,
		}, m)
	}
	if skipped > 0 {
		j.Warnings = append(j.Warnings, Warning{
			Kind: string(sqlcgen.JournalEventKindWipToFinishedGoods),
			Detail: fmt.Sprintf(
				"%d bottling run%s produced no transfer out of work in progress — %s. "+
					"Finished goods will be short by whatever those runs were worth.",
				skipped, plural(skipped), firstWhy),
		})
	}
	return nil
}
