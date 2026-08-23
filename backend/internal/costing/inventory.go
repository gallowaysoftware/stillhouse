package costing

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// InventoryValue is what the alcohol on hand is worth, in the two places
// it sits: work in progress (made, not packaged) and finished goods
// (packaged, not sold).
//
// Every figure is accompanied by what could not be valued. A valuation
// that quietly omits the casks it could not price reads as a smaller
// inventory rather than an incomplete one, and the direction of that
// error is the one an auditor cares about.
type InventoryValue struct {
	WIP           Bucket
	FinishedGoods Bucket
	TotalCAD      float64
	// Basis is the sentence to put under the figure.
	Basis string
}

type Bucket struct {
	ValueCAD float64
	Lines    []ValueLine
	// Quantities, valued or not, so the operator can see how much of the
	// warehouse the figure actually covers.
	TotalLAA    float64
	ValuedLAA   float64
	Unvalued    int
	UnvaluedWhy []string
}

type ValueLine struct {
	Name     string
	Detail   string
	LAA      float64
	Bottles  int32
	UnitCAD  float64
	ValueCAD float64
	Valued   bool
	Why      string
}

// incompleteWhy names the components a cost is missing, so a valuation
// derived from it carries the caveat rather than the caller having to
// know to look.
func incompleteWhy(c FullResult) string {
	var missing []string
	for _, comp := range []Component{c.MaterialsComponent, c.Labour, c.Overhead} {
		if !comp.Available && comp.Missing != "" {
			missing = append(missing, comp.Missing)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "valued at an incomplete cost — " + strings.Join(missing, "; ")
}

// ValueInventory prices what is on hand.
//
// Work in progress is bulk alcohol, casks included — a maturing cask is
// the largest WIP a whisky distillery has. It is valued at the cost per
// LAA of the runs that produced it, which Stillhouse can only derive
// through a bottling run; bulk that has never been bottled from has no
// costed chain, so it is reported as unvalued rather than valued at the
// price of nothing.
//
// Finished goods are bottles on hand at the full cost of the run they
// came from. A lot with no run behind it — adopted opening stock — is
// unvalued for the same reason.
func ValueInventory(
	ctx context.Context, q *sqlcgen.Queries,
) (InventoryValue, error) {
	var out InventoryValue
	out.Basis = "Finished goods at the full cost of the run each lot came from " +
		"(direct materials, plus labour and overhead where rates are set). " +
		"Work in progress at the same per-LAA cost, carried back from the " +
		"runs that have drawn from each vessel."

	// Finished goods first: the run costs it computes are what WIP is
	// then valued against, so the walk is done once.
	perLAAByContainer := map[uuid.UUID]float64{}
	partialByContainer := map[uuid.UUID]string{}
	runCost := map[uuid.UUID]FullResult{}

	lots, err := q.ValuePackagedForFinishedGoods(ctx)
	if err != nil {
		return out, err
	}
	for _, l := range lots {
		laa := float64(l.BottlesOnHand) * float64(l.BottleSizeMl) / 1000 * l.TargetAbvPct / 100
		line := ValueLine{
			Name:    l.LotCode,
			Detail:  fmt.Sprintf("%s · %s", l.ProductName, l.Jurisdiction),
			Bottles: l.BottlesOnHand,
			LAA:     laa,
		}
		out.FinishedGoods.TotalLAA += laa

		if !l.BottlingRunID.Valid {
			line.Why = "no bottling run behind this lot — adopted stock"
			out.FinishedGoods.Unvalued++
			out.FinishedGoods.UnvaluedWhy = append(out.FinishedGoods.UnvaluedWhy,
				fmt.Sprintf("%s: %s", l.LotCode, line.Why))
			out.FinishedGoods.Lines = append(out.FinishedGoods.Lines, line)
			continue
		}
		cost, ok := runCost[l.BottlingRunID.UUID]
		if !ok {
			cost, err = BottlingRunFullCost(ctx, q, l.BottlingRunID.UUID)
			if err != nil {
				return out, err
			}
			runCost[l.BottlingRunID.UUID] = cost
		}
		per := cost.PerBottleCAD()
		if per <= 0 {
			line.Why = "the run it came from could not be priced"
			out.FinishedGoods.Unvalued++
			out.FinishedGoods.UnvaluedWhy = append(out.FinishedGoods.UnvaluedWhy,
				fmt.Sprintf("%s: %s", l.LotCode, line.Why))
			out.FinishedGoods.Lines = append(out.FinishedGoods.Lines, line)
			continue
		}
		line.Valued = true
		line.UnitCAD = per
		line.ValueCAD = round2(per * float64(l.BottlesOnHand))
		// Valued, but at a cost that is missing a component. Better than
		// no value at all, and worse than it looks unless it says so.
		if !cost.Complete {
			line.Why = incompleteWhy(cost)
		}
		out.FinishedGoods.ValueCAD += line.ValueCAD
		out.FinishedGoods.ValuedLAA += laa
		out.FinishedGoods.Lines = append(out.FinishedGoods.Lines, line)

		// The same run also tells us what a litre of absolute alcohol out
		// of its source vessel cost, which is what the bulk still in that
		// vessel is worth.
		run, rerr := q.GetBottlingRun(ctx, l.BottlingRunID.UUID)
		if rerr == nil && run.TankGaugeLaa > 0 && cost.TotalCAD > 0 {
			perLAA := cost.TotalCAD / run.TankGaugeLaa
			if !cost.Complete {
				partialByContainer[run.SourceContainerID] = incompleteWhy(cost)
			}
			if prev, seen := perLAAByContainer[run.SourceContainerID]; !seen || perLAA > prev {
				// The most recent-and-costly figure rather than an
				// average: a cheap average across a vessel that has been
				// topped up understates what is left in it, and
				// understating inventory is the direction that misleads.
				perLAAByContainer[run.SourceContainerID] = perLAA
			}
		}
	}
	out.FinishedGoods.ValueCAD = round2(out.FinishedGoods.ValueCAD)

	vessels, err := q.ValueBulkForWIP(ctx)
	if err != nil {
		return out, err
	}
	for _, v := range vessels {
		line := ValueLine{
			Name:   v.Name,
			Detail: string(v.Kind),
			LAA:    v.CurrentLaa,
		}
		if v.Possession == sqlcgen.BulkPossessionHeldElsewhere {
			line.Detail += " · held elsewhere"
		}
		out.WIP.TotalLAA += v.CurrentLaa
		per, ok := perLAAByContainer[v.ID]
		if !ok || per <= 0 {
			line.Why = "nothing has been bottled from this vessel, so there is " +
				"no costed chain behind what is in it"
			out.WIP.Unvalued++
			out.WIP.UnvaluedWhy = append(out.WIP.UnvaluedWhy,
				fmt.Sprintf("%s: %s", v.Name, line.Why))
			out.WIP.Lines = append(out.WIP.Lines, line)
			continue
		}
		line.Valued = true
		line.UnitCAD = per
		line.ValueCAD = round2(per * v.CurrentLaa)
		if why, partial := partialByContainer[v.ID]; partial {
			line.Why = why
		}
		out.WIP.ValueCAD += line.ValueCAD
		out.WIP.ValuedLAA += v.CurrentLaa
		out.WIP.Lines = append(out.WIP.Lines, line)
	}
	out.WIP.ValueCAD = round2(out.WIP.ValueCAD)
	out.TotalCAD = round2(out.WIP.ValueCAD + out.FinishedGoods.ValueCAD)
	return out, nil
}
