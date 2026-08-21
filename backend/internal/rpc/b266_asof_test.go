package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN A10, pinned. The closing balances used to be `SUM(current_laa)`
// with no date: generating May's return in August reported August's
// balance as May's closing figure, and because the opening balance is
// reverse-walked from the closing one, both ends moved together and the
// arithmetic still tied out. A return that is internally consistent and
// factually wrong is the worst shape for an error — nothing looks off.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// A period's closing balance must not move when alcohol moves after the
// period has closed. This is the whole of A10 in one assertion.
func TestB266ClosingBalanceIsAsOfPeriodEnd(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	b266 := NewB266Service(f.db, log)

	// A closed month, months behind the current one. April rather than
	// January because the seeded excise band starts 2026-04-01 and the
	// rate lookup refuses outside what it can source (stage 142).
	const periodStart, periodEnd = "2026-04-01", "2026-04-30"
	inPeriod := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)

	tank := f.tank(t, "As-of tank", 0, 0)

	// 1000 L at 70% distilled inside the period: 700 LAA.
	if err := f.movement(t, tank.ID, 1000, 70, sqlcgen.BulkMovementReasonProductionGauge, inPeriod); err != nil {
		t.Fatalf("seed production: %v", err)
	}

	first, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: periodStart, PeriodEnd: periodEnd,
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	before := first.Msg.GetReport()
	if got, want := before.GetBulkClosingLaa(), 700.0; !near(got, want, 1e-6) {
		t.Fatalf("closing LAA at period end: got %v, want %v", got, want)
	}

	// Now move alcohol AFTER the period closed — a June production run and
	// a July loss. Neither belongs on January's return, at either end.
	if err := f.movement(t, tank.ID, 500, 70, sqlcgen.BulkMovementReasonProductionGauge,
		time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed later production: %v", err)
	}
	if err := f.movementOut(t, tank.ID, 100, 70, sqlcgen.BulkMovementReasonLossEvaporation,
		time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed later loss: %v", err)
	}

	second, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: periodStart, PeriodEnd: periodEnd,
	}))
	if err != nil {
		t.Fatalf("GenerateB266 (regenerate): %v", err)
	}
	after := second.Msg.GetReport()

	if got, want := after.GetBulkClosingLaa(), before.GetBulkClosingLaa(); !near(got, want, 1e-6) {
		t.Errorf("April's closing balance moved to %v after June and July activity (was %v) — "+
			"the return reports today's balance, not the period's", got, want)
	}
	if got, want := after.GetBulkOpeningLaa(), before.GetBulkOpeningLaa(); !near(got, want, 1e-6) {
		t.Errorf("April's opening balance moved to %v (was %v)", got, want)
	}
	// And the period's own lines are unchanged: nothing after the period
	// leaked into a receipt or a withdrawal either.
	if got, want := after.GetBulkProductionLaa(), 700.0; !near(got, want, 1e-6) {
		t.Errorf("production in April: got %v, want %v — June's run was counted", got, want)
	}
	if got := after.GetBulkLossesLaa(); !near(got, 0, 1e-6) {
		t.Errorf("losses in April: got %v, want 0 — July's loss was counted", got)
	}
	// The books still close on the as-of figures.
	receipts := after.GetBulkProductionLaa() + after.GetBulkReceivedInBondLaa()
	withdrawals := after.GetBulkTransferredToPackagingLaa() + after.GetBulkTransferredOutInBondLaa() +
		after.GetBulkLossesLaa() + after.GetBulkDestroyedLaa()
	if got := after.GetBulkOpeningLaa() + receipts - withdrawals; !near(got, after.GetBulkClosingLaa(), 1e-6) {
		t.Errorf("bulk books don't close: %v vs closing %v", got, after.GetBulkClosingLaa())
	}
}

// The packaged side of the same property. Bottling and removals after the
// period must not move what the period reports as on hand.
func TestB266PackagedClosingIsAsOfPeriodEnd(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	b266 := NewB266Service(f.db, log)

	f.stamps(t, "CA-ON", 2000)
	tank := f.tank(t, "Packaged as-of tank", 2000, 70)
	prod := f.product(t, "As-of Vodka", 750, 40)
	lot := "ASOF-" + uuid.NewString()[:8]

	// 400 bottles inside the period.
	if _, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 400, LotCode: lot,
		BottlingDate: "2026-05-10",
	})); err != nil {
		t.Fatalf("CreateBottlingRun (in period): %v", err)
	}

	const periodStart, periodEnd = "2026-05-01", "2026-05-31"
	first, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: periodStart, PeriodEnd: periodEnd,
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	before := first.Msg.GetReport()
	if got, want := before.GetPackagedClosingBottles(), int32(400); got != want {
		t.Fatalf("closing bottles: got %d, want %d", got, want)
	}

	// A later run and a later removal, both outside the period.
	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		// A distinct lot: bottling_runs.lot_code is UNIQUE per tenant, so a
		// lot is bottled exactly once.
		DestinationJurisdiction: "CA-ON", BottleCount: 300,
		LotCode:      "ASOF2-" + uuid.NewString()[:8],
		BottlingDate: "2026-06-05",
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun (after period): %v", err)
	}
	if _, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: bottled.Msg.GetPackaged().GetId(), BottlesRemoved: 250,
		DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
		RemovalDate:     "2026-07-01",
	})); err != nil {
		t.Fatalf("CreateRemoval (after period): %v", err)
	}

	second, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: periodStart, PeriodEnd: periodEnd,
	}))
	if err != nil {
		t.Fatalf("GenerateB266 (regenerate): %v", err)
	}
	after := second.Msg.GetReport()

	if got, want := after.GetPackagedClosingBottles(), before.GetPackagedClosingBottles(); got != want {
		t.Errorf("May's closing bottles moved to %d after June and July activity (was %d)", got, want)
	}
	if got, want := after.GetPackagedClosingLaa(), before.GetPackagedClosingLaa(); !near(got, want, 1e-6) {
		t.Errorf("May's closing LAA moved to %v (was %v)", got, want)
	}
	if got, want := after.GetPackagedOpeningLaa(), before.GetPackagedOpeningLaa(); !near(got, want, 1e-6) {
		t.Errorf("May's opening LAA moved to %v (was %v)", got, want)
	}
	if got := after.GetPackagedRemovedDutyPaidBottles(); got != 0 {
		t.Errorf("July's removal landed on May's return: %d bottles", got)
	}
	if got := after.GetDutyPayableCad(); !near(got, 0, 1e-9) {
		t.Errorf("July's duty landed on May's return: %v", got)
	}
}

// A return generated the moment the period closes must be identical to the
// old behaviour — the walk is backwards from the running total precisely so
// that nothing already filed moves.
func TestB266AsOfNowMatchesRunningTotals(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	b266 := NewB266Service(f.db, log)

	f.stamps(t, "CA-ON", 1000)
	tank := f.tank(t, "Prompt tank", 1000, 70)
	prod := f.product(t, "Prompt Gin", 750, 40)
	today := time.Now().UTC()

	if _, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 200,
		LotCode:      "PROMPT-" + uuid.NewString()[:8],
		BottlingDate: today.Format("2006-01-02"),
	})); err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}

	// A period ending today: the "after" set is empty, so the as-of walk
	// must return exactly the running totals.
	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: today.AddDate(0, 0, -7).Format("2006-01-02"),
		PeriodEnd:   today.Format("2006-01-02"),
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	rep := resp.Msg.GetReport()

	runningBulk, err := f.q.SumBulkOnHandAsOf(f.ctx, pgtype.Timestamptz{
		Valid: true, Time: today.AddDate(0, 0, 30),
	})
	if err != nil {
		t.Fatalf("running bulk: %v", err)
	}
	if got, want := rep.GetBulkClosingLaa(), round4(runningBulk); !near(got, want, 1e-6) {
		t.Errorf("bulk closing on a period ending today: got %v, want the running total %v", got, want)
	}
	if got, want := rep.GetPackagedClosingBottles(), int32(200); got != want {
		t.Errorf("packaged closing bottles: got %d, want %d", got, want)
	}
}

// The as-of walk subtracts what the ledger says moved after the period
// from the running balance. That is only sound if the ledger explains the
// running balance in the first place — if some path could change
// bulk_containers.current_laa without writing a bulk_movement, the two
// would drift and every backdated return would be wrong by the difference.
//
// Stated as an invariant: running balance minus the net of every movement
// ever recorded is zero. Built here only through real operations — a
// production gauge, a bottling run — because that is the state the
// application can actually produce.
func TestLedgerExplainsTheRunningBalance(t *testing.T) {
	f := newDutyFixture(t)
	f.stamps(t, "CA-ON", 1000)
	tank := f.tank(t, "Invariant tank", 0, 0)
	prod := f.product(t, "Invariant Rye", 750, 45)

	if err := f.movement(t, tank.ID, 1000, 70, sqlcgen.BulkMovementReasonProductionGauge,
		time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("production: %v", err)
	}
	bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
		ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
		DestinationJurisdiction: "CA-ON", BottleCount: 400, BottlingLossL: 2.5,
		LotCode: "INV-" + uuid.NewString()[:8], BottlingDate: "2026-05-11",
	}))
	if err != nil {
		t.Fatalf("CreateBottlingRun: %v", err)
	}
	if _, err := f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
		PackagedInventoryId: bottled.Msg.GetPackaged().GetId(), BottlesRemoved: 150,
		DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
		RemovalDate:     "2026-05-20",
	})); err != nil {
		t.Fatalf("CreateRemoval: %v", err)
	}
	if err := f.movementOut(t, tank.ID, 20, 70, sqlcgen.BulkMovementReasonLossEvaporation,
		time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("loss: %v", err)
	}

	// Tenant-scoped deliberately: the fixture pool is the admin DSN, which
	// bypasses RLS, so an unscoped sum would fold in every other test's
	// tenant.
	var residual float64
	if err := f.pool.QueryRow(f.ctx, `
		SELECT COALESCE((SELECT SUM(current_laa) FROM bulk_containers
		                  WHERE tenant_id = $1 AND NOT archived), 0)
		     - COALESCE((SELECT SUM(CASE WHEN destination_container_id IS NOT NULL THEN laa ELSE 0 END
		                              - CASE WHEN source_container_id      IS NOT NULL THEN laa ELSE 0 END)
		                   FROM bulk_movements WHERE tenant_id = $1), 0)`,
		f.tenant.ID).Scan(&residual); err != nil {
		t.Fatalf("residual query: %v", err)
	}
	if !near(residual, 0, 1e-9) {
		t.Errorf("running balance and ledger disagree by %v LAA — "+
			"something moved a container balance without recording a movement, "+
			"and every backdated return is wrong by that much", residual)
	}
}

// A date the rate table cannot source must refuse at the handler, not be
// priced at whatever band happens to be compiled in. Duty computed at
// today's rate against last year's quantities is wrong on a filed return
// and nothing about it looks wrong.
func TestB266AndRemovalRefuseDatesWithNoRate(t *testing.T) {
	f := newDutyFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	b266 := NewB266Service(f.db, log)

	from, to := excise.Coverage()
	before := from.AddDate(0, -1, 0).Format("2006-01-02")

	t.Run("a return for a period before the table", func(t *testing.T) {
		_, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
			PeriodStart: before, PeriodEnd: from.AddDate(0, 0, -1).Format("2006-01-02"),
		}))
		if err == nil {
			t.Fatal("a return was generated for a period with no rate on file")
		}
		if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
			t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
		}
	})

	t.Run("a period straddling a rate change", func(t *testing.T) {
		// The last band's KnownUntil is the next indexation date. A period
		// crossing it would need two sets of rates on a form that has one
		// line for each.
		_, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
			PeriodStart: to.AddDate(0, 0, -5).Format("2006-01-02"),
			PeriodEnd:   to.AddDate(0, 0, 5).Format("2006-01-02"),
		}))
		if err == nil {
			t.Fatal("a return was generated across a rate boundary")
		}
		if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
			t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
		}
	})

	t.Run("a removal dated before the table", func(t *testing.T) {
		// An at-removal tenant, so the removal is the duty event and has
		// to resolve a rate for its own date.
		f.warehouseLicensed(t)
		f.stamps(t, "CA-ON", 200)
		tank := f.tank(t, "No-rate tank", 1000, 70)
		prod := f.product(t, "No-rate Gin", 750, 40)
		bottled, err := f.bottling.CreateBottlingRun(f.ctx, connect.NewRequest(&stillhousev1.CreateBottlingRunRequest{
			ProductId: prod.ID.String(), SourceContainerId: tank.ID.String(),
			DestinationJurisdiction: "CA-ON", BottleCount: 100,
			LotCode: "NORATE-" + uuid.NewString()[:8],
		}))
		if err != nil {
			t.Fatalf("CreateBottlingRun: %v", err)
		}
		_, err = f.removal.CreateRemoval(f.ctx, connect.NewRequest(&stillhousev1.CreateRemovalRequest{
			PackagedInventoryId: bottled.Msg.GetPackaged().GetId(), BottlesRemoved: 10,
			DestinationKind: stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER,
			RemovalDate:     before,
		}))
		if err == nil {
			t.Fatal("a removal was dutied on a date with no rate on file")
		}
		if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
			t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
		}
		// The operator has to be told what to do about it, not handed a
		// bare code.
		if !strings.Contains(err.Error(), "will not extrapolate") {
			t.Errorf("message doesn't explain the refusal: %v", err)
		}
	})
}
