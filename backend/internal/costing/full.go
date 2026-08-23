package costing

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// Component is one part of a full cost, with the reason it is what it is.
//
// Basis is a column rather than a footnote for the same reason it is on
// the journal's lines: a cost of sales figure that does not say what is
// in it is one an accountant has to ask about, and the answer they get
// depends on who they ask. Available is false when the licensee has not
// set the rate this component needs — which is different from zero, and
// is reported as different.
type Component struct {
	Name      string
	AmountCAD float64
	Basis     string
	Available bool
	// Why the component could not be computed, when Available is false.
	Missing string
}

// FullResult is a run's cost with labour and overhead in it.
//
// Materials is the direct-material figure BottlingRunMaterialCost has
// always produced. TotalCAD is the sum of whatever was available — never
// a figure padded with zeros for the parts that were not.
type FullResult struct {
	BottleCount int32
	Materials   Result
	Labour      Component
	Overhead    Component
	TotalCAD    float64
	// Complete is true only when every component was available. A cost
	// that is missing its overhead is still worth showing; calling it a
	// full cost is not.
	Complete bool
	// LabourHours is what was booked to the run itself. Hours on the
	// mash and the distillation behind it are deliberately excluded —
	// see SumLabourForBottlingRun.
	LabourHours float64
}

// PerBottleCAD is the figure a cost-of-sales line needs.
//
// The bottle count is held in two places — here and on Materials — and
// they are set from one source, so they cannot disagree in practice. The
// fallback is here because "in practice" is doing work in that sentence:
// a caller that builds a FullResult by hand and fills only one of them
// would otherwise get a silent zero where a cost belongs.
func (r FullResult) PerBottleCAD() float64 {
	n := r.BottleCount
	if n <= 0 {
		n = r.Materials.BottleCount
	}
	if n <= 0 {
		return 0
	}
	return r.TotalCAD / float64(n)
}

// BottlingRunFullCost prices a run at direct materials plus labour plus
// absorbed overhead, using the rates in force on the day it was bottled.
//
// The rates are read as at the bottling date rather than as at now, so
// costing a March run in June uses March's rates. Without that, a rate
// change silently restates every batch ever costed — including those
// already carried into an accountant's books.
func BottlingRunFullCost(
	ctx context.Context, q *sqlcgen.Queries, runID uuid.UUID,
) (FullResult, error) {
	out := FullResult{
		Labour:   Component{Name: "Labour"},
		Overhead: Component{Name: "Overhead"},
	}
	materials, err := BottlingRunMaterialCost(ctx, q, runID)
	if err != nil {
		return out, err
	}
	out.Materials = materials
	out.BottleCount = materials.BottleCount
	out.TotalCAD = materials.TotalCAD

	run, err := q.GetBottlingRun(ctx, runID)
	if err != nil {
		return out, err
	}
	rates, err := q.CostRatesInForceOn(ctx, run.BottlingDate)
	haveRates := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}

	hours, err := q.SumLabourForBottlingRun(ctx, uuid.NullUUID{UUID: runID, Valid: true})
	if err != nil {
		return out, err
	}
	out.LabourHours = hours.Hours

	switch {
	case !haveRates:
		out.Labour.Missing = "no cost rates are set for " +
			run.BottlingDate.Time.Format("2006-01-02")
	case !rates.LabourRateCadPerHour.Valid:
		out.Labour.Missing = "no labour rate is set"
	case hours.Entries == 0:
		// Zero hours recorded is not zero labour; it is no record of any.
		// Absorbing nothing here and calling the cost complete is how a
		// bottle ends up costing the price of its barley.
		out.Labour.Missing = "no hours were recorded against this run"
	default:
		rate := numeric(rates.LabourRateCadPerHour)
		out.Labour.Available = true
		out.Labour.AmountCAD = round2(hours.Hours * rate)
		out.Labour.Basis = fmt.Sprintf("%.2f h at $%.2f/h", hours.Hours, rate)
		out.TotalCAD += out.Labour.AmountCAD
	}

	switch {
	case !haveRates:
		out.Overhead.Missing = "no cost rates are set for " +
			run.BottlingDate.Time.Format("2006-01-02")
	case !rates.OverheadBasis.Valid || !rates.OverheadRate.Valid:
		out.Overhead.Missing = "no overhead basis is set"
	default:
		rate := numeric(rates.OverheadRate)
		switch rates.OverheadBasis.OverheadBasis {
		case sqlcgen.OverheadBasisPerMaterialDollar:
			out.Overhead.Available = true
			out.Overhead.AmountCAD = round2(materials.TotalCAD * rate)
			out.Overhead.Basis = fmt.Sprintf("%.1f%% of $%.2f direct materials",
				rate*100, materials.TotalCAD)
		case sqlcgen.OverheadBasisPerLabourHour:
			if hours.Entries == 0 {
				out.Overhead.Missing = "overhead absorbs per labour hour, and no " +
					"hours were recorded against this run"
				break
			}
			out.Overhead.Available = true
			out.Overhead.AmountCAD = round2(hours.Hours * rate)
			out.Overhead.Basis = fmt.Sprintf("%.2f h at $%.2f/h", hours.Hours, rate)
		case sqlcgen.OverheadBasisPerLaa:
			laa := run.TankGaugeLaa
			if laa <= 0 {
				out.Overhead.Missing = "overhead absorbs per LAA, and this run " +
					"recorded no tank gauge"
				break
			}
			out.Overhead.Available = true
			out.Overhead.AmountCAD = round2(laa * rate)
			out.Overhead.Basis = fmt.Sprintf("%.4f LAA at $%.2f/LAA", laa, rate)
		}
		if out.Overhead.Available {
			out.TotalCAD += out.Overhead.AmountCAD
		}
	}

	out.TotalCAD = round2(out.TotalCAD)
	out.Complete = materials.UnpricedLines == 0 &&
		out.Labour.Available && out.Overhead.Available
	return out, nil
}

func numeric(n pgtype.Numeric) float64 {
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

// round2 states a money figure to the cent.
//
// math.Round rather than a hand-rolled truncate-and-add-a-half: the
// hand-rolled version disagrees with it on negatives and on anything
// where x*100 lands a hair under .5, and a cent per bottle across a year
// is a number somebody notices. Half-way cases still follow the float,
// not the decimal — 1.005 is stored as 1.00499999999999989 and rounds
// down, which is a property of the input rather than of this function.
func round2(x float64) float64 {
	return math.Round(x*100) / 100
}
