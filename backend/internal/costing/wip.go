package costing

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// WIPGauge is one production gauge and what it carried into work in
// progress.
//
// Value is a Component for the same reason every other cost in this
// package is: an unvalued gauge has to be reported as unvalued, with the
// reason, rather than as a gauge worth nothing. The two look identical in
// a total and mean opposite things.
type WIPGauge struct {
	ID            string
	GaugeDate     time.Time
	LAA           float64
	ContainerName string
	Value         Component
	// How many distillation charges the walk went through to reach this
	// gauge. One is the ordinary case; more means the spirit came from
	// several fermentations and the cost is a blend of their mashes.
	ChargeCount int32
}

// WIPProduction is a period's spirit gauged into bulk, valued.
type WIPProduction struct {
	Basis  string
	Gauges []WIPGauge
	// TotalCAD is the sum of the gauges that could be valued, and
	// ValuedCount says how many that was. A total over eleven gauges of
	// which three are unvalued is not the period's WIP, and the two
	// numbers together are what make that visible.
	TotalCAD    float64
	ValuedCount int32
	TotalLAA    float64
	ValuedLAA   float64
	// Complete is true only when every gauge in the period could be
	// valued. Same meaning as FullResult.Complete and for the same reason.
	Complete bool
	// Why the whole computation was refused, when it was. Set only when
	// the licensee has not stated a charge basis — the one convention
	// Stillhouse will not choose for them.
	Refused string
}

// ErrNoChargeBasis is the refusal that stands in for the convention
// Stillhouse does not have. Valuing spirit gauged into WIP means
// apportioning a fermentation's cost across the stills it was charged to,
// and whether that follows litres charged or alcohol charged is the
// licensee's accounting policy. A low-wines run and a spirit run drawing
// the same litres do not carry the same alcohol.
const ErrNoChargeBasis = "no WIP charge basis has been set, so a fermentation's cost cannot be apportioned across the distillation runs it fed. Set one under Costing — litres charged, or LAA charged. Stillhouse will not pick for you: which is right is your accounting policy, and a figure produced on a convention nobody chose would reconcile and never be questioned."

// ProductionGaugeWIP values every production gauge in the period.
//
// basis is the tenant's stated convention, empty when they have not
// stated one. Empty is a refusal rather than a default: see
// ErrNoChargeBasis, and 000061's header for why this is not treated as a
// special case of the rule the rest of the package follows.
func ProductionGaugeWIP(
	ctx context.Context,
	q *sqlcgen.Queries,
	basis string,
	periodStart, periodEnd time.Time,
) (WIPProduction, error) {
	out := WIPProduction{Basis: basis}
	if basis == "" {
		out.Refused = ErrNoChargeBasis
		return out, nil
	}

	rows, err := q.ProductionGaugeWIPCost(ctx, sqlcgen.ProductionGaugeWIPCostParams{
		Basis:       basis,
		PeriodStart: pgtype.Timestamptz{Valid: true, Time: periodStart},
		PeriodEnd:   pgtype.Timestamptz{Valid: true, Time: periodEnd},
	})
	if err != nil {
		return out, err
	}

	out.Complete = true
	for _, r := range rows {
		g := WIPGauge{
			ID:            r.ID.String(),
			GaugeDate:     r.GaugeDate.Time,
			LAA:           r.Laa.Float64,
			ContainerName: r.ContainerName,
			ChargeCount:   r.ChargeCount,
			Value: Component{
				Name:  "Materials into WIP",
				Basis: wipBasisLabel(basis),
			},
		}
		if why := wipRefusal(r); why != "" {
			g.Value.Missing = why
			out.Complete = false
		} else {
			g.Value.Available = true
			g.Value.AmountCAD = round2(r.CostCad)
			out.TotalCAD += r.CostCad
			out.ValuedCount++
			out.ValuedLAA += r.Laa.Float64
		}
		out.TotalLAA += r.Laa.Float64
		out.Gauges = append(out.Gauges, g)
	}
	out.TotalCAD = round2(out.TotalCAD)
	return out, nil
}

// wipRefusal names why a gauge could not be valued, most actionable
// first. Only one reason is returned: an operator given three reasons for
// one row fixes none of them, and the reasons are ordered so the first is
// the one to go and do.
func wipRefusal(r sqlcgen.ProductionGaugeWIPCostRow) string {
	switch {
	case !r.AllMashesPriced:
		return fmt.Sprintf(
			"%d material line%s behind this gauge has no lot cost, so the mash it came from is unvalued. Record the lot each material came from on the mash.",
			r.UnpricedMaterialLines, plural(int(r.UnpricedMaterialLines)))
	case !r.AllFermentSharesKnown:
		return "a mash behind this gauge fed more than one fermentation and at least one of them has no initial volume recorded, so the mash's cost cannot be split between them. Record the initial volume on each fermentation off that mash."
	case !r.AllChargeSharesKnown:
		return "a fermentation behind this gauge was charged with no measurable quantity on the chosen basis, so its cost cannot be apportioned. Check the charge volumes — and, on the LAA basis, the strengths — recorded against it."
	case !r.CostKnown:
		// Belt and braces: the three flags above should account for every
		// NULL the walk can produce. If one gets through, say so plainly
		// rather than reporting the COALESCE'd zero as a cost.
		return "the walk back to the mashes did not complete for this gauge, and Stillhouse will not report a partial figure as a cost."
	}
	return ""
}

// wipBasisLabel is the convention in the operator's words, for the Basis
// column that travels with every cost in this package.
func wipBasisLabel(basis string) string {
	switch basis {
	case "charged_laa":
		return "mash materials at lot cost, apportioned by LAA charged"
	case "charged_volume":
		return "mash materials at lot cost, apportioned by litres charged"
	default:
		return basis
	}
}
