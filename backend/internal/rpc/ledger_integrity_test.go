package rpc

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// The ledger's one non-negotiable invariant: alcohol is neither created nor
// destroyed by moving it. Every test here was written against a real defect
// found by driving the live system, and fails without its fix.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN, same as the B266
// and full-path tests.

type ledgerFixture struct {
	pool   *pgxpool.Pool
	db     *tenantdb.DB
	q      *sqlcgen.Queries
	tenant sqlcgen.Tenant
	user   sqlcgen.User
	ctx    context.Context
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	ctx := context.Background()
	// Fixtures are seeded through the admin pool (crossing the tenant
	// boundary on purpose); the handlers under test are driven through a
	// pool that row-level security applies to. See internal/testdb.
	pool := testdb.AdminPool(t)
	q := sqlcgen.New(pool)

	tenant, err := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                         "Ledger " + uuid.NewString(),
		CraSpiritsLicenceNumber:      "LEDGER-" + uuid.NewString(),
		ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
		DefaultJurisdiction:          "CA-ON",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	// Every tenant has one, the way signup and cmd/seed make one.
	if _, err := q.CreateDefaultLocation(ctx, sqlcgen.CreateDefaultLocationParams{
		TenantID: tenant.ID, Name: tenant.Name,
	}); err != nil {
		t.Fatalf("create default location: %v", err)
	}

	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID: tenant.ID, Email: "ledger-" + uuid.NewString() + "@example.com",
		PasswordHash: "x", DisplayName: "Ledger Test", Role: sqlcgen.UserRoleOwner,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &ledgerFixture{
		pool: pool, db: tenantdb.New(testdb.AppPool(t)), q: q,
		tenant: tenant, user: user, ctx: WithUser(ctx, user),
	}
}

// tank creates a container already holding volume L at abv %.
func (f *ledgerFixture) tank(t *testing.T, name string, volume, abv float64) sqlcgen.BulkContainer {
	t.Helper()
	c, err := f.q.CreateBulkContainer(f.ctx, sqlcgen.CreateBulkContainerParams{
		TenantID: f.tenant.ID, Name: name, Kind: sqlcgen.BulkContainerKindTank,
		CapacityL: pgtype.Float8{Float64: 100000, Valid: true},
	})
	if err != nil {
		t.Fatalf("create tank: %v", err)
	}
	if _, err := f.q.UpdateBulkContainerBalance(f.ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID: c.ID, CurrentVolumeL: volume,
		CurrentAbvPct: pgtype.Float8{Float64: abv, Valid: true},
		CurrentLaa:    volume * abv / 100,
	}); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	c.CurrentVolumeL, c.CurrentAbvPct, c.CurrentLaa = volume,
		pgtype.Float8{Float64: abv, Valid: true}, volume*abv/100
	return c
}

func (f *ledgerFixture) barrel(t *testing.T, name string, capacity float64) sqlcgen.BulkContainer {
	t.Helper()
	c, err := f.q.CreateBulkContainer(f.ctx, sqlcgen.CreateBulkContainerParams{
		TenantID: f.tenant.ID, Name: name, Kind: sqlcgen.BulkContainerKindBarrel,
		CapacityL: pgtype.Float8{Float64: capacity, Valid: true},
	})
	if err != nil {
		t.Fatalf("create barrel: %v", err)
	}
	// A barrel is a container plus its attributes row; the barrel RPCs read
	// both, so a fixture that skips this looks like a missing barrel.
	if _, err := f.q.CreateBarrelAttributes(f.ctx, sqlcgen.CreateBarrelAttributesParams{
		ContainerID: c.ID, TenantID: f.tenant.ID,
	}); err != nil {
		t.Fatalf("create barrel attributes: %v", err)
	}
	return c
}

func (f *ledgerFixture) balance(t *testing.T, id uuid.UUID) (volume, laa float64) {
	t.Helper()
	c, err := f.q.GetBulkContainer(f.ctx, id)
	if err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return c.CurrentVolumeL, c.CurrentLaa
}

// TestFillBarrelConservesLAA: found by QA. Filling at a strength that
// differs from the source's credited the barrel at the fill strength while
// debiting the source at its own — 100 L "at 80%" out of a 60% tank put 80
// LAA in the barrel and took only 60 out, creating 20 LAA from nothing. The
// movement row said 80 while the balance moved 60, so the journal and the
// balances disagreed too.
func TestFillBarrelConservesLAA(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	tank := f.tank(t, "Conservation tank", 1000, 60)
	barrel := f.barrel(t, "Conservation barrel", 250)
	beforeVol, beforeLAA := f.balance(t, tank.ID)

	// Declared 61%: within measurement noise of the tank's 60%, so the fill
	// is allowed — and must balance to the litre of alcohol.
	_, err := svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 100, AbvPct: 61,
	}))
	if err != nil {
		t.Fatalf("FillBarrel: %v", err)
	}

	afterVol, afterLAA := f.balance(t, tank.ID)
	_, barrelLAA := f.balance(t, barrel.ID)

	if got, want := beforeVol-afterVol, 100.0; !near(got, want, 1e-6) {
		t.Errorf("source volume fell by %.4f L, want %.4f", got, want)
	}
	if drained, gained := beforeLAA-afterLAA, barrelLAA; !near(drained, gained, 1e-6) {
		t.Errorf("source lost %.4f LAA but barrel gained %.4f — %.4f LAA created from nothing",
			drained, gained, gained-drained)
	}
}

// TestFillBarrelRefusesImpossibleStrength: you cannot draw 80% spirit out of
// a tank holding 60%. One of the two figures is wrong, and silently
// rewriting the tank's strength to absorb the difference would hide it.
func TestFillBarrelRefusesImpossibleStrength(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	tank := f.tank(t, "Mismatch tank", 1000, 60)
	barrel := f.barrel(t, "Mismatch barrel", 250)

	_, err := svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 100, AbvPct: 80,
	}))
	if err == nil {
		t.Fatal("filling at 80% from a 60% tank was accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
}

// TestConcurrentFillsDoNotLoseWithdrawals: found by QA. Eight simultaneous
// 100 L fills from one tank moved 800 L into eight barrels while the tank
// fell by 200 — every balance write was an absolute value computed from a
// stale read, so concurrent transactions clobbered each other. Two people
// on two tablets is the ordinary case at any distillery with staff.
func TestConcurrentFillsDoNotLoseWithdrawals(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	const fills, each = 8, 100.0
	tank := f.tank(t, "Race tank", 2000, 60)
	barrels := make([]sqlcgen.BulkContainer, fills)
	for i := range barrels {
		barrels[i] = f.barrel(t, "Race barrel "+uuid.NewString()[:8], 250)
	}
	beforeVol, beforeLAA := f.balance(t, tank.ID)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, fills)
	for i := range barrels {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them together to maximise overlap
			_, errs[i] = svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
				BarrelId: barrels[i].ID.String(), SourceContainerId: tank.ID.String(),
				VolumeL: each, AbvPct: 60,
			}))
		}()
	}
	close(start)
	wg.Wait()

	moved := 0.0
	succeeded := 0
	for i, err := range errs {
		if err != nil {
			// A refusal is acceptable (serialization failure surfaces as
			// one); silently succeeding while the tank keeps its alcohol
			// is not.
			t.Logf("fill %d refused: %v", i, err)
			continue
		}
		moved += each
		succeeded++
	}
	// Without this floor the test passes vacuously: if every fill failed,
	// nothing moved, the tank didn't change, and both assertions below
	// hold. A regression that made FillBarrel always error would show
	// green.
	if succeeded == 0 {
		t.Fatal("no fill succeeded — the test proves nothing about concurrency")
	}
	afterVol, afterLAA := f.balance(t, tank.ID)

	if got, want := beforeVol-afterVol, moved; !near(got, want, 1e-6) {
		t.Errorf("%d fills moved %.0f L out of the tank but it fell by %.0f L — %.0f L unaccounted",
			fills, moved, got, want-got)
	}
	if got, want := beforeLAA-afterLAA, moved*60/100; !near(got, want, 1e-6) {
		t.Errorf("tank lost %.4f LAA, want %.4f", got, want)
	}
}

// TestDumpBarrelRecordsResidualAsLoss: found by QA. A barrel holding 80 LAA
// dumped as 70.2 credited the destination 70.2 and zeroed the barrel — the
// residual 9.8 LAA vanished with no loss movement, so it never reached the
// B266's loss line. Alcohol leaving the ledger has to be accounted for.
func TestDumpBarrelRecordsResidualAsLoss(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	dest := f.tank(t, "Dump destination", 0, 0)
	barrel := f.barrel(t, "Dump barrel", 250)
	if _, err := f.q.UpdateBulkContainerBalance(f.ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID: barrel.ID, CurrentVolumeL: 100,
		CurrentAbvPct: pgtype.Float8{Float64: 80, Valid: true}, CurrentLaa: 80,
	}); err != nil {
		t.Fatalf("seed barrel: %v", err)
	}

	if _, err := svc.DumpBarrel(f.ctx, connect.NewRequest(&stillhousev1.DumpBarrelRequest{
		BarrelId: barrel.ID.String(), DestinationContainerId: dest.ID.String(),
		VolumeL: 90, AbvPct: 78,
	})); err != nil {
		t.Fatalf("DumpBarrel: %v", err)
	}

	_, destLAA := f.balance(t, dest.ID)
	if !near(destLAA, 70.2, 1e-6) {
		t.Errorf("destination holds %.4f LAA, want 70.2", destLAA)
	}

	// The 9.8 LAA the cask kept has to appear as a recorded loss.
	var lossLAA float64
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COALESCE(SUM(laa), 0) FROM bulk_movements
		  WHERE tenant_id = $1 AND source_container_id = $2 AND reason IN ('loss_evaporation', 'loss_unaccounted')`,
		f.tenant.ID, barrel.ID).Scan(&lossLAA); err != nil {
		t.Fatalf("read losses: %v", err)
	}
	if !near(lossLAA, 9.8, 1e-6) {
		t.Errorf("recorded loss = %.4f LAA, want 9.8 — the residual vanished from the ledger", lossLAA)
	}
}

// near reports whether a and b agree to within tol.
func near(a, b, tol float64) bool { return a-b < tol && b-a < tol }

// TestBarrelWritesRefusedInSubmittedPeriod: found by QA. After submitting
// the August B266, an operator back-dated a barrel fill into August and it
// was accepted — silently changing the numbers behind a filed return.
// assertDateNotInLockedPeriod was applied in adopt / distillation /
// bottling / removal but not in the barrel or blend paths.
func TestBarrelWritesRefusedInSubmittedPeriod(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	tank := f.tank(t, "Locked tank", 1000, 60)
	barrel := f.barrel(t, "Locked barrel", 250)

	// A submitted return covering May 2026.
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	period, err := f.q.UpsertB266PeriodDraft(f.ctx, sqlcgen.UpsertB266PeriodDraftParams{
		TenantID:    f.tenant.ID,
		PeriodStart: pgtype.Date{Valid: true, Time: start},
		PeriodEnd:   pgtype.Date{Valid: true, Time: end},
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}
	if _, err := f.q.SubmitB266Period(f.ctx, sqlcgen.SubmitB266PeriodParams{
		ID: period.ID, Snapshot: []byte("{}"),
		SubmittedBy: uuid.NullUUID{UUID: f.user.ID, Valid: true},
		// A submitted period must carry its acknowledgement — the table
		// CHECK holds that for every path, this fixture included.
		FilingAcknowledgement: filingAcknowledgementText(),
	}); err != nil {
		t.Fatalf("submit period: %v", err)
	}

	inPeriod := timestamppb.New(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))
	_, err = svc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 10, AbvPct: 60, EventDate: inPeriod,
	}))
	if err == nil {
		t.Fatal("a fill back-dated into a submitted B266 period was accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
}

// TestB266RefusesOverlappingPeriods: found by QA. Periods could be
// generated freely on top of each other — October 1–31 (already submitted),
// October 1–15, October 15–November 15 and a 400-day range all coexisted.
// Two returns covering the same day report the same alcohol twice.
func TestB266RefusesOverlappingPeriods(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	gen := func(from, to string) error {
		_, err := svc.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
			PeriodStart: from, PeriodEnd: to,
		}))
		return err
	}

	if err := gen("2026-10-01", "2026-10-31"); err != nil {
		t.Fatalf("first period: %v", err)
	}
	// Re-generating the identical range is how a draft gets refreshed.
	if err := gen("2026-10-01", "2026-10-31"); err != nil {
		t.Errorf("re-generating the same range should be allowed: %v", err)
	}
	// An abutting month must still be fine — periods tile.
	if err := gen("2026-11-01", "2026-11-30"); err != nil {
		t.Errorf("the next month should be allowed: %v", err)
	}

	for _, tc := range []struct{ name, from, to string }{
		{"contained by an existing period", "2026-10-05", "2026-10-20"},
		{"straddling the end", "2026-10-15", "2026-11-15"},
		{"swallowing several", "2025-01-01", "2026-12-31"},
		{"sharing a single day", "2026-10-31", "2026-11-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := gen(tc.from, tc.to)
			if err == nil {
				t.Fatalf("%s → %s was accepted alongside October", tc.from, tc.to)
			}
			if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
				t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
			}
		})
	}
}

// TestB266DutyReconcilesAcrossRateBands: found by QA. Spirits above 7% ABV
// are charged per litre of absolute alcohol; at or below 7% they are
// charged per litre of product. The return quoted a single "rate per LAA"
// of $14.117 beside a blended LAA total, so a period holding both bands
// failed its own arithmetic — 7.775 LAA at the stated rate is $109.76,
// while the duty actually owed was $97.41. The total was right; the return
// simply could not be checked against the figures printed on it.
func TestB266DutyReconcilesAcrossRateBands(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	day := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	// One removal in each band, booked straight into the removals table:
	// 20 x 750 mL at 40% = 6 LAA, and 100 x 355 mL at 5% = 35.5 L.
	for _, r := range []struct {
		no                     int32
		bottles                int32
		sizeML                 int32
		abv, litres, laa, duty float64
		ratePerLAA             float64
	}{
		{1, 20, 750, 40, 15, 6, 6 * 14.117, 14.117},
		{2, 100, 355, 5, 35.5, 1.775, 35.5 * 0.358, 0},
	} {
		if _, err := f.q.CreateRemoval(f.ctx, sqlcgen.CreateRemovalParams{
			TenantID: f.tenant.ID, RemovalNo: r.no,
			PackagedInventoryID: f.packagedLot(t, r.sizeML, r.abv, r.bottles),
			RemovalDate:         pgtype.Date{Valid: true, Time: day},
			BottlesRemoved:      r.bottles,
			DestinationKind:     sqlcgen.RemovalDestinationKindDutyPaidCustomer,
			BottleSizeMl:        r.sizeML,
			BottleAbvPct:        r.abv,
			TotalLitres:         r.litres,
			TotalLaa:            r.laa,
			DutyRatePerLaa:      r.ratePerLAA,
			DutyAmountCad:       r.duty,
		}); err != nil {
			t.Fatalf("removal %d: %v", r.no, err)
		}
	}

	res, err := svc.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-01", PeriodEnd: "2026-05-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	rep := res.Msg.GetReport()

	if got, want := rep.GetPackagedRemovedOver7Laa(), 6.0; !near(got, want, 1e-6) {
		t.Errorf("over-7%% LAA = %v, want %v", got, want)
	}
	if got, want := rep.GetPackagedRemovedUnder7Litres(), 35.5; !near(got, want, 1e-6) {
		t.Errorf("under-7%% litres = %v, want %v", got, want)
	}
	// Each band must reconcile against the quantity it is charged on.
	if got, want := rep.GetPackagedRemovedOver7Laa()*rep.GetDutyRatePerLaa(),
		rep.GetPackagedRemovedOver7DutyCad(); !near(got, want, 0.01) {
		t.Errorf("over-7%% line doesn't reconcile: %v LAA x %v = %v, reported %v",
			rep.GetPackagedRemovedOver7Laa(), rep.GetDutyRatePerLaa(), got, want)
	}
	if got, want := rep.GetPackagedRemovedUnder7Litres()*rep.GetDutyRatePerLitreUnder7(),
		rep.GetPackagedRemovedUnder7DutyCad(); !near(got, want, 0.01) {
		t.Errorf("under-7%% line doesn't reconcile: %v L x %v = %v, reported %v",
			rep.GetPackagedRemovedUnder7Litres(), rep.GetDutyRatePerLitreUnder7(), got, want)
	}
	// And the bands must add up to the total payable.
	if got, want := rep.GetPackagedRemovedOver7DutyCad()+rep.GetPackagedRemovedUnder7DutyCad(),
		rep.GetDutyPayableCad(); !near(got, want, 0.01) {
		t.Errorf("bands sum to %v but duty payable says %v", got, want)
	}
}

// packagedLot creates a product + bottling run + packaged inventory row so a
// removal has something to point at.
func (f *ledgerFixture) packagedLot(t *testing.T, sizeML int32, abv float64, bottles int32) uuid.UUID {
	t.Helper()
	product, err := f.q.CreateProduct(f.ctx, sqlcgen.CreateProductParams{
		TenantID: f.tenant.ID, Name: "Band " + uuid.NewString()[:8],
		SpiritKind: sqlcgen.SpiritKindVodka, BottleSizeMl: sizeML, TargetAbvPct: abv,
	})
	if err != nil {
		t.Fatalf("product: %v", err)
	}
	pkg, err := f.q.UpsertPackagedInventory(f.ctx, sqlcgen.UpsertPackagedInventoryParams{
		TenantID: f.tenant.ID, ProductID: product.ID,
		LotCode: "LOT-" + uuid.NewString()[:8], Jurisdiction: "CA-ON",
		BottlesOnHand: bottles,
	})
	if err != nil {
		t.Fatalf("packaged inventory: %v", err)
	}
	return pkg.ID
}
