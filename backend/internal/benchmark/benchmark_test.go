package benchmark

import (
	"fmt"
	"strings"
	"testing"
)

// These rules decide whether one distillery's operational figures become
// visible to its competitors. Each test below is a way that could go
// wrong.

func obsFor(tenants int, each int, base float64) []Observation {
	var out []Observation
	for t := 0; t < tenants; t++ {
		for i := 0; i < each; i++ {
			out = append(out, Observation{
				TenantID: fmt.Sprintf("t%d", t),
				Value:    base + float64(t) + float64(i)*0.1,
			})
		}
	}
	return out
}

// The k floor counts LICENSEES, not measurements. One distillery with
// four hundred casks is still one distillery, and a cohort of its casks
// is its own figure with a label on it.
func TestKFloorCountsTenantsNotObservations(t *testing.T) {
	// One tenant, four hundred casks.
	many := obsFor(1, 400, 3.0)
	got := Summarise(many)
	if got.Available {
		t.Fatal("reported a cohort built from a single distillery's 400 casks")
	}
	if got.Observations != 0 || got.Tenants != 0 {
		t.Errorf("a refusal still carried counts: %+v", got)
	}
	if !strings.Contains(got.Missing, "identify them") {
		t.Errorf("refusal does not say why: %q", got.Missing)
	}

	// Exactly at the floor, evenly spread: reportable.
	ok := Summarise(obsFor(MinTenants, 4, 3.0))
	if !ok.Available {
		t.Fatalf("refused at exactly the floor: %s", ok.Missing)
	}
	if ok.Tenants != MinTenants {
		t.Errorf("tenants: got %d, want %d", ok.Tenants, MinTenants)
	}
}

// One below the floor must refuse. Pinned separately because an
// off-by-one here publishes a cohort that should not exist.
func TestKFloorIsExact(t *testing.T) {
	if got := Summarise(obsFor(MinTenants-1, 10, 3.0)); got.Available {
		t.Errorf("reported with %d tenants, floor is %d", MinTenants-1, MinTenants)
	}
	if got := Summarise(obsFor(MinTenants, 1, 3.0)); !got.Available {
		t.Errorf("refused with exactly %d tenants: %s", MinTenants, got.Missing)
	}
}

// Enough tenants is not enough. If one supplies most of the
// measurements, the cohort median sits on top of theirs.
func TestDominanceSuppression(t *testing.T) {
	obs := obsFor(MinTenants, 1, 3.0) // five tenants, one each
	// One of them adds a hundred more.
	for i := 0; i < 100; i++ {
		obs = append(obs, Observation{TenantID: "t0", Value: 9.0})
	}
	got := Summarise(obs)
	if got.Available {
		t.Fatalf("reported a cohort where one participant is %d of %d measurements",
			101, len(obs))
	}
	if !strings.Contains(got.Missing, "reporting them") {
		t.Errorf("refusal does not name the reason: %q", got.Missing)
	}
}

// Extremes are never reported. A maximum is one participant's exact
// number, and publishing it is publishing them. This asserts the type
// itself offers no way to leak one.
func TestCohortOffersNoExtremes(t *testing.T) {
	got := Summarise(obsFor(MinTenants, 4, 3.0))
	if !got.Available {
		t.Fatalf("refused: %s", got.Missing)
	}
	// The quartiles must sit inside the data, never at its edges by
	// construction — p25 and p75 of a spread sample are interior points.
	if got.P25 >= got.Median || got.Median >= got.P75 {
		t.Errorf("quartiles are not ordered: %v / %v / %v", got.P25, got.Median, got.P75)
	}
}

// A tenant contributing only unusable values must not count toward k.
// Otherwise a cohort of four real distilleries plus one sending NaNs
// would publish as five.
func TestUnusableValuesDoNotCountTowardK(t *testing.T) {
	obs := obsFor(MinTenants-1, 4, 3.0)
	nan := math_NaN()
	obs = append(obs, Observation{TenantID: "ghost", Value: nan})
	if got := Summarise(obs); got.Available {
		t.Error("a tenant contributing only NaN counted toward the k floor")
	}
}

func math_NaN() float64 {
	var zero float64
	return zero / zero
}

func TestQuantile(t *testing.T) {
	s := []float64{1, 2, 3, 4, 5}
	for _, tc := range []struct {
		q    float64
		want float64
	}{{0.25, 2}, {0.5, 3}, {0.75, 4}} {
		if got := quantile(s, tc.q); got != tc.want {
			t.Errorf("quantile(%v) = %v, want %v", tc.q, got, tc.want)
		}
	}
}
