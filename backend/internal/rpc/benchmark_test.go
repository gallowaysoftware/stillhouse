package rpc

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// PLAN J2. This is the one feature in Stillhouse that reads across the
// tenant boundary, which makes it the one place where getting it wrong
// publishes a distillery's operations to its competitors.
//
// The k-anonymity arithmetic is covered in internal/benchmark. These
// cover what only a database can show: that opting out actually stops
// contribution, and that a caller who has not opted in sees nothing.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// Reciprocity. A caller who contributes nothing sees nothing — otherwise
// the feature is a way to read the industry without appearing in it.
func TestBenchmarks_NotOptedInSeesNothing(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBenchmarkService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	resp, err := svc.Benchmarks(f.ctx, connect.NewRequest(&stillhousev1.BenchmarksRequest{}))
	if err != nil {
		t.Fatalf("Benchmarks: %v", err)
	}
	if resp.Msg.GetOptedIn() {
		t.Fatal("a new tenant is opted in by default — consent that has to be withdrawn is not consent")
	}
	if resp.Msg.GetRefused() == "" {
		t.Fatal("returned benchmarks to a tenant that has not opted in")
	}
	if len(resp.Msg.GetMetrics()) != 0 {
		t.Errorf("refused but returned %d metrics", len(resp.Msg.GetMetrics()))
	}
	if !strings.Contains(resp.Msg.GetRefused(), "data tap") {
		t.Errorf("refusal does not explain reciprocity: %q", resp.Msg.GetRefused())
	}
}

// Opting in shows the metrics — and, with nowhere near five participating
// distilleries in a test database, each must be a refusal rather than a
// figure. This is the k floor observed end to end.
func TestBenchmarks_OptedInStillRespectsTheFloor(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBenchmarkService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if _, err := svc.SetBenchmarkOptIn(f.ctx, connect.NewRequest(&stillhousev1.SetBenchmarkOptInRequest{
		OptIn: true,
	})); err != nil {
		t.Fatalf("SetBenchmarkOptIn: %v", err)
	}

	resp, err := svc.Benchmarks(f.ctx, connect.NewRequest(&stillhousev1.BenchmarksRequest{}))
	if err != nil {
		t.Fatalf("Benchmarks: %v", err)
	}
	if resp.Msg.GetRefused() != "" {
		t.Fatalf("refused after opting in: %s", resp.Msg.GetRefused())
	}
	if len(resp.Msg.GetMetrics()) == 0 {
		t.Fatal("no metrics at all")
	}
	for _, m := range resp.Msg.GetMetrics() {
		c := m.GetCohort()
		if c.GetAvailable() {
			// Only legitimate if five real distilleries have opted in,
			// which a test database does not have.
			t.Errorf("%s: reported a cohort with %d contributing tenants — the floor is 5",
				m.GetKey(), c.GetTenants())
		}
		if c.GetMissing() == "" {
			t.Errorf("%s: unavailable with no reason", m.GetKey())
		}
		// A refusal must not leak the shape of the sample it is refusing
		// to describe.
		if c.GetP25() != 0 || c.GetMedian() != 0 || c.GetP75() != 0 {
			t.Errorf("%s: a refused cohort carried quartiles %v/%v/%v",
				m.GetKey(), c.GetP25(), c.GetMedian(), c.GetP75())
		}
		if c.GetTenants() != 0 || c.GetObservations() != 0 {
			t.Errorf("%s: a refused cohort carried counts %d/%d",
				m.GetKey(), c.GetTenants(), c.GetObservations())
		}
	}
	// The note is on the response, not in the documentation.
	if !strings.Contains(resp.Msg.GetPrivacyNote(), "never minimums or maximums") {
		t.Errorf("privacy note does not state the rule: %q", resp.Msg.GetPrivacyNote())
	}
}

// Opting out stops contribution immediately and clears the stamp, so the
// record never says somebody consented when they have withdrawn.
func TestBenchmarks_OptOutTakesEffectAndClearsTheStamp(t *testing.T) {
	f := newDutyFixture(t)
	svc := NewBenchmarkService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	seedQualifyingCask(t, f)

	in, err := svc.SetBenchmarkOptIn(f.ctx, connect.NewRequest(&stillhousev1.SetBenchmarkOptInRequest{
		OptIn: true,
	}))
	if err != nil {
		t.Fatalf("opt in: %v", err)
	}
	if !in.Msg.GetOptedIn() || in.Msg.GetOptedInAt() == "" {
		t.Fatalf("opting in did not stamp: %+v", in.Msg)
	}

	out, err := svc.SetBenchmarkOptIn(f.ctx, connect.NewRequest(&stillhousev1.SetBenchmarkOptInRequest{
		OptIn: false,
	}))
	if err != nil {
		t.Fatalf("opt out: %v", err)
	}
	if out.Msg.GetOptedIn() {
		t.Error("still opted in after opting out")
	}
	if out.Msg.GetOptedInAt() != "" {
		t.Error("the consent stamp survived a withdrawal")
	}

	// And the contribution stops: the keyhole function must not return
	// this tenant's casks any more.
	var n int
	if err := testdb.AdminPool(t).QueryRow(f.ctx,
		`SELECT count(*) FROM bench_angels_share() WHERE tenant_id = $1`, f.tenant.ID).Scan(&n); err != nil {
		t.Fatalf("bench_angels_share: %v", err)
	}
	if n != 0 {
		t.Errorf("an opted-out tenant still contributed %d observations", n)
	}
}

// The keyhole must never return a tenant that has not opted in — the
// filter is inside the SECURITY DEFINER function, where the app role
// cannot get around it.
func TestBenchmarks_KeyholeExcludesNonParticipants(t *testing.T) {
	f := newDutyFixture(t)
	// A cask that WOULD qualify: filled with a recorded strength, well
	// over 90 days in wood, still holding alcohol. Without seeding one
	// the function returns nothing whatever the filter says, and this
	// test would pass for the wrong reason — which it did, until a
	// falsification showed it could not fail.
	seedQualifyingCask(t, f)

	var n int
	if err := testdb.AdminPool(t).QueryRow(f.ctx,
		`SELECT count(*) FROM bench_angels_share() b
		  JOIN tenants t ON t.id = b.tenant_id
		 WHERE NOT t.benchmark_opt_in`).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Errorf("the benchmark keyhole returned %d observations from tenants who never opted in", n)
	}
}

// seedQualifyingCask makes one cask that bench_angels_share() will pick
// up, so the exclusion tests have something to exclude.
func seedQualifyingCask(t *testing.T, f *dutyFixture) {
	t.Helper()
	pool := testdb.AdminPool(t)
	b := f.barrel(t, "Bench cask "+uuid.NewString()[:8], 200)

	if _, err := pool.Exec(f.ctx, `
		INSERT INTO barrel_attributes (container_id, tenant_id, fill_date, rickhouse)
		VALUES ($1,$2, CURRENT_DATE - 400, 'Rackhouse A')
		ON CONFLICT (container_id) DO UPDATE
		  SET fill_date = EXCLUDED.fill_date, rickhouse = EXCLUDED.rickhouse`,
		b.ID, f.tenant.ID); err != nil {
		t.Fatalf("attributes: %v", err)
	}
	if _, err := pool.Exec(f.ctx, `
		INSERT INTO barrel_events (tenant_id, container_id, kind, event_date, volume_l, abv_pct, laa)
		VALUES ($1,$2,'fill', CURRENT_DATE - 400, 200, 70, 140)`,
		f.tenant.ID, b.ID); err != nil {
		t.Fatalf("fill event: %v", err)
	}
	if err := f.db.WithTenantTx(f.ctx, f.tenant.ID, func(ctx context.Context, q *sqlcgen.Queries) error {
		_, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID: b.ID, CurrentVolumeL: 190,
			CurrentAbvPct: pgtype.Float8{Float64: 68, Valid: true}, CurrentLaa: 129.2,
		})
		return e
	}); err != nil {
		t.Fatalf("balance: %v", err)
	}

	// It qualifies while the tenant is opted in...
	var n int
	if _, err := pool.Exec(f.ctx,
		`UPDATE tenants SET benchmark_opt_in = TRUE WHERE id = $1`, f.tenant.ID); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	if err := pool.QueryRow(f.ctx,
		`SELECT count(*) FROM bench_angels_share() WHERE tenant_id = $1`, f.tenant.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n == 0 {
		t.Fatal("the seeded cask does not qualify, so the exclusion tests would pass vacuously")
	}
	// ...and the caller puts it back to not opted in.
	if _, err := pool.Exec(f.ctx,
		`UPDATE tenants SET benchmark_opt_in = FALSE WHERE id = $1`, f.tenant.ID); err != nil {
		t.Fatalf("opt out: %v", err)
	}
}
