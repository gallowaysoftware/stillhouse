// Package costing walks the chain from a bottling run back to the grain
// it came from, and prices it.
//
// It exists as its own package because two callers need the same answer
// and must not compute it twice: the cost screen an operator reads, and
// the accounting journal's cost-of-sales line. Two implementations of
// "what did this run cost" would eventually disagree, and the version
// that reached the accountant would be the one nobody had been looking
// at.
//
// The walk is: bottling run → the movements that fed its source
// container → the production gauges among them → the distillation
// charges behind each → the mashes behind those → the ingredient lines,
// priced at the lot each came from. Mashes are deduplicated, because a
// mash feeding a blend twice is one mash.
package costing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// Line is one priced ingredient behind a run.
type Line struct {
	MaterialName string
	SupplierLot  string
	QuantityUsed float64
	UOM          string
	UnitCostCAD  float64
	LineCostCAD  float64
	// Priced is false when the lot has no recorded unit cost. The line is
	// still returned — the material was used, and hiding it would make a
	// short total look complete — but it contributes nothing to the sum.
	Priced bool
}

// Result is a run's direct material cost.
type Result struct {
	BottleCount   int32
	TotalCAD      float64
	Lines         []Line
	UnpricedLines int
}

// PerBottleCAD is the figure a cost-of-sales line needs. Zero when the
// run bottled nothing or nothing could be priced — the caller decides
// what to do about that, because "no cost recorded" and "cost of zero"
// are different statements and only the caller knows which matters.
func (r Result) PerBottleCAD() float64 {
	if r.BottleCount <= 0 {
		return 0
	}
	return r.TotalCAD / float64(r.BottleCount)
}

// BottlingRunMaterialCost prices one run. Must be called inside a
// tenant-scoped transaction; every query it runs is RLS-protected.
func BottlingRunMaterialCost(
	ctx context.Context, q *sqlcgen.Queries, runID uuid.UUID,
) (Result, error) {
	var out Result
	run, err := q.GetBottlingRun(ctx, runID)
	if err != nil {
		return out, err
	}
	out.BottleCount = run.BottleCount

	// A day's grace on the cutoff: a gauge recorded the same evening as
	// the bottling it fed is part of that chain, and a strict comparison
	// against the bottling date would drop it.
	feedCutoff := run.BottlingDate.Time.Add(24 * time.Hour)
	feeds, err := q.BottlingRunChainFeeds(ctx, sqlcgen.BottlingRunChainFeedsParams{
		DestinationContainerID: uuid.NullUUID{UUID: run.SourceContainerID, Valid: true},
		OccurredAt:             pgtype.Timestamptz{Time: feedCutoff, Valid: true},
	})
	if err != nil {
		return out, err
	}

	seen := make(map[uuid.UUID]bool)
	for _, fd := range feeds {
		if fd.Reason != sqlcgen.BulkMovementReasonProductionGauge {
			continue
		}
		charges, err := q.DistillationChainFromGauge(ctx, fd.ID)
		if errors.Is(err, pgx.ErrNoRows) || len(charges) == 0 {
			continue
		}
		if err != nil {
			return out, err
		}
		for _, ch := range charges {
			if !ch.MashRunID.Valid || seen[ch.MashRunID.UUID] {
				continue
			}
			seen[ch.MashRunID.UUID] = true

			ings, err := q.ListMashIngredients(ctx, ch.MashRunID.UUID)
			if err != nil {
				return out, err
			}
			for _, ing := range ings {
				if !ing.MaterialLotID.Valid {
					continue
				}
				lot, err := q.GetMaterialLot(ctx, ing.MaterialLotID.UUID)
				if err != nil {
					return out, err
				}
				line := Line{
					MaterialName: ing.MaterialName,
					SupplierLot:  ing.SupplierLot.String,
					QuantityUsed: ing.QuantityUsed,
					UOM:          ing.Uom,
				}
				// The landed cost, not the supplier's price. Freight,
				// duty and handling are part of what the grain cost;
				// leaving them out understates every figure built on
				// this by exactly what it cost to get the grain here.
				// It is a generated column, so it falls back to the
				// unit cost when there are no charges.
				if lot.LandedUnitCostCad.Valid {
					line.Priced = true
					line.UnitCostCAD = lot.LandedUnitCostCad.Float64
					line.LineCostCAD = ing.QuantityUsed * lot.LandedUnitCostCad.Float64
					out.TotalCAD += line.LineCostCAD
				} else {
					out.UnpricedLines++
				}
				out.Lines = append(out.Lines, line)
			}
		}
	}
	return out, nil
}
