// Package benchmark turns cross-tenant observations into a cohort
// statistic, or refuses to.
//
// Everything here is pure, and that is deliberate: these are the rules
// that decide whether one distillery's operational figures become visible
// to its competitors, and rules like that should be readable and testable
// without a database in the way.
package benchmark

import (
	"fmt"
	"math"
	"sort"
)

// MinTenants is the k-anonymity floor: how many DISTINCT contributing
// licensees a cohort needs before any statistic about it is reported.
//
// Counted in tenants rather than observations, because one distillery
// with four hundred casks is still one distillery, and a "cohort" of one
// tenant's casks is that tenant's own figure with a label on it.
const MinTenants = 5

// MaxShare is the dominance ceiling. Even at MinTenants, if one
// contributor supplies most of the observations then the cohort median
// sits on top of theirs and the statistic is effectively about them.
const MaxShare = 0.5

// Observation is one measurement and who it came from. The tenant is
// carried only so the rules below can count and weigh; it never leaves
// this package.
type Observation struct {
	TenantID string
	Value    float64
}

// Cohort is a reportable statistic, or the reason there is not one.
//
// Extremes are deliberately absent. A maximum IS one participant's exact
// number, and publishing it is publishing them — so the shape of the
// distribution is given as quartiles and nothing else.
type Cohort struct {
	Available bool
	// Why not, when not. Phrased for the operator, who is entitled to
	// know that the answer is being withheld rather than that there is
	// no answer.
	Missing string

	P25    float64
	Median float64
	P75    float64
	// How many licensees contributed. Reported because it is what makes
	// the figure worth anything, and safe because it is never below
	// MinTenants when Available is true.
	Tenants      int
	Observations int
}

// Summarise applies the rules and computes the quartiles.
//
// The order matters: the k-count runs before the dominance check, so a
// cohort that is too small is told so rather than being told about
// dominance in a sample nobody should see the shape of anyway.
func Summarise(obs []Observation) Cohort {
	var c Cohort

	// Drop values arithmetic cannot use. Done before counting so a tenant
	// contributing only NaNs does not count toward k.
	clean := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if math.IsNaN(o.Value) || math.IsInf(o.Value, 0) {
			continue
		}
		clean = append(clean, o)
	}

	byTenant := map[string]int{}
	for _, o := range clean {
		byTenant[o.TenantID]++
	}
	c.Tenants = len(byTenant)
	c.Observations = len(clean)

	if c.Tenants < MinTenants {
		c.Missing = fmt.Sprintf(
			"only %d distiller%s have opted in with enough data for this figure, and Stillhouse will not report one below %d. Below that a cohort statistic is close enough to somebody's own number to identify them.",
			c.Tenants, plural(c.Tenants), MinTenants)
		// Deliberately zeroed. A refusal that still carried the
		// quartiles would be a refusal in name only.
		c.Tenants, c.Observations = 0, 0
		return c
	}

	for _, n := range byTenant {
		if float64(n)/float64(len(clean)) > MaxShare {
			c.Missing = fmt.Sprintf(
				"one participant supplies more than %.0f%% of the measurements behind this figure, so reporting it would be reporting them. It will appear once the sample is more evenly spread.",
				MaxShare*100)
			c.Tenants, c.Observations = 0, 0
			return c
		}
	}

	vals := make([]float64, 0, len(clean))
	for _, o := range clean {
		vals = append(vals, o.Value)
	}
	sort.Float64s(vals)
	c.P25 = quantile(vals, 0.25)
	c.Median = quantile(vals, 0.50)
	c.P75 = quantile(vals, 0.75)
	c.Available = true
	return c
}

// quantile is the linear-interpolation definition, on a sorted slice.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
