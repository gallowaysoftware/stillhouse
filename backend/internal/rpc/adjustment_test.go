package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN A4. Line D on B266 page 3 is a real, reason-coded entry that
// reconciles book inventory to physical, and Stillhouse had no concept of
// one. The gap showed up three ways, each pinned below:
//
//   - RegaugeBarrel refuses any upward variance ("regauges record losses
//     only"), so a cask gauging higher than the book had no path at all.
//   - Tanks could not be reconciled: regauge is barrel-only.
//   - A downward variance on a barrel was booked as loss_evaporation
//     whatever caused it, so a counting error and the angels' share landed
//     on the same line — and under EDM3-4-1 they do not carry the same
//     duty treatment.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func newAdjustFixture(t *testing.T) (*dutyFixture, *BulkService) {
	t.Helper()
	f := newDutyFixture(t)
	return f, NewBulkService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func adjust(t *testing.T, svc *BulkService, f *dutyFixture, containerID string,
	countedL, abv float64, reason stillhousev1.InventoryAdjustmentReason, why string,
) *stillhousev1.RecordInventoryAdjustmentResponse {
	t.Helper()
	resp, err := svc.RecordInventoryAdjustment(f.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
		ContainerId: containerID, Reason: reason, Explanation: why,
		CountedVolumeL: countedL, AbvPct: abv,
	}))
	if err != nil {
		t.Fatalf("RecordInventoryAdjustment: %v", err)
	}
	return resp.Msg
}

// The upward variance RegaugeBarrel refuses outright. A cask gauging
// higher than the book is an ordinary event — a mis-keyed fill, an
// instrument error — and before this it had no path.
func TestAdjustmentRecordsAnUpwardVariance(t *testing.T) {
	f, svc := newAdjustFixture(t)
	tank := f.tank(t, "Upward tank", 1000, 60) // book: 600 LAA
	barrelSvc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	barrel := f.barrel(t, "Upward barrel", 250)

	// What regauge does with an increase, for contrast.
	if _, err := barrelSvc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 200, AbvPct: 60,
	})); err != nil {
		t.Fatalf("FillBarrel: %v", err)
	}
	_, err := barrelSvc.RegaugeBarrel(f.ctx, connect.NewRequest(&stillhousev1.RegaugeBarrelRequest{
		BarrelId: barrel.ID.String(), NewVolumeL: 210, NewAbvPct: 60,
	}))
	if err == nil {
		t.Fatal("RegaugeBarrel accepted an increase; this test's premise is stale")
	}

	// The adjustment path takes it, and says why.
	got := adjust(t, svc, f, barrel.ID.String(), 210, 60,
		stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_DATA_ENTRY_ERROR,
		"fill was keyed as 200 L; the cask holds 210 by dip")
	a := got.GetAdjustment()

	if got, want := a.GetBookLaa(), 120.0; !near(got, want, 1e-6) {
		t.Errorf("book LAA: got %v, want %v", got, want)
	}
	if got, want := a.GetCountedLaa(), 126.0; !near(got, want, 1e-6) {
		t.Errorf("counted LAA: got %v, want %v", got, want)
	}
	if got, want := a.GetDeltaLaa(), 6.0; !near(got, want, 1e-6) {
		t.Errorf("delta: got %v, want %v", got, want)
	}
	// And the container now holds what was counted.
	if _, laa := f.balance(t, barrel.ID); !near(laa, 126, 1e-6) {
		t.Errorf("barrel LAA after adjustment: got %v, want 126", laa)
	}
}

// Tanks had no reconciliation path at all — regauge is barrel-only.
func TestAdjustmentReconcilesATank(t *testing.T) {
	f, svc := newAdjustFixture(t)
	tank := f.tank(t, "Reconciled tank", 1000, 60) // book: 600 LAA

	got := adjust(t, svc, f, tank.ID.String(), 985, 60,
		stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
		"annual count: dip reads 985 L")
	a := got.GetAdjustment()

	if got, want := a.GetDeltaLaa(), -9.0; !near(got, want, 1e-6) {
		t.Errorf("delta: got %v, want %v", got, want)
	}
	if got, want := a.GetDeltaVolumeL(), -15.0; !near(got, want, 1e-6) {
		t.Errorf("delta volume: got %v, want %v", got, want)
	}
	if vol, laa := f.balance(t, tank.ID); !near(vol, 985, 1e-6) || !near(laa, 591, 1e-6) {
		t.Errorf("tank after adjustment: %v L / %v LAA, want 985 / 591", vol, laa)
	}
}

// The reason code is the point of line D, and so is the explanation: a
// reconciliation entry is read by an auditor asking why the numbers moved,
// and the code alone does not answer that.
func TestAdjustmentRequiresAReasonAndAnExplanation(t *testing.T) {
	f, svc := newAdjustFixture(t)
	tank := f.tank(t, "Unexplained tank", 1000, 60)

	for _, tc := range []struct {
		name   string
		reason stillhousev1.InventoryAdjustmentReason
		why    string
	}{
		{"no reason code", stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_UNSPECIFIED, "counted"},
		{"no explanation", stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT, ""},
		{"whitespace explanation", stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT, "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.RecordInventoryAdjustment(f.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
				ContainerId: tank.ID.String(), Reason: tc.reason, Explanation: tc.why,
				CountedVolumeL: 900, AbvPct: 60,
			}))
			if err == nil {
				t.Fatal("an unexplained adjustment was accepted")
			}
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
			}
		})
	}
}

// An adjustment that confirms the book moves no alcohol and writes no
// ledger row — but it is still recorded, because a count that found no
// variance is evidence the count was done.
func TestAnAdjustmentThatConfirmsTheBookIsStillRecorded(t *testing.T) {
	f, svc := newAdjustFixture(t)
	tank := f.tank(t, "Confirmed tank", 1000, 60)

	got := adjust(t, svc, f, tank.ID.String(), 1000, 60,
		stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
		"quarterly count agrees with the book")
	a := got.GetAdjustment()

	if a.GetDeltaLaa() != 0 {
		t.Errorf("delta on a confirming count: got %v, want 0", a.GetDeltaLaa())
	}
	if a.GetBulkMovementId() != "" {
		t.Errorf("a confirming count wrote a ledger movement (%s) — nothing moved",
			a.GetBulkMovementId())
	}
	if a.GetId() == "" {
		t.Error("the adjustment itself was not recorded — the count is the evidence")
	}
}

// The count is corrected to 20 °C before it is compared with anything. A
// warm tank gauged against a book figure at 20 °C would otherwise show a
// variance that is entirely the thermometer's.
func TestAdjustmentCorrectsTheCountBeforeComparing(t *testing.T) {
	if os.Getenv("ALC_TAB") == "" {
		t.Skip("set ALC_TAB to run the temperature-correction path")
	}
	f, svc := newAdjustFixture(t)
	tank := f.tank(t, "Warm tank", 1000, 60)

	resp, err := svc.RecordInventoryAdjustment(f.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
		ContainerId: tank.ID.String(),
		Reason:      stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
		Explanation: "counted warm", CountedVolumeL: 1000, AbvPct: 60,
		TemperatureC: 28, TemperatureCSet: true,
	}))
	if err != nil {
		t.Fatalf("RecordInventoryAdjustment: %v", err)
	}
	a := resp.Msg.GetAdjustment()
	// 1000 L at 28 °C is less than 1000 L at 20 °C, so a count that
	// matched the book's litres is really a small shortfall.
	if a.GetCountedVolumeL() >= 1000 {
		t.Errorf("counted volume was not corrected: %v L at 28 °C came out as %v at 20 °C",
			1000.0, a.GetCountedVolumeL())
	}
	if a.GetDeltaLaa() >= 0 {
		t.Errorf("delta: got %v, want a shortfall — 1000 warm litres are fewer than 1000 at 20 °C",
			a.GetDeltaLaa())
	}
	if a.GetVolumeFactorC() >= 1 {
		t.Errorf("volume factor at 28 °C: got %v, want < 1", a.GetVolumeFactorC())
	}
}

// Line D on the return: signed net, with each direction reported, and the
// walk still closing.
func TestB266ReportsAdjustmentsOnTheirOwnLine(t *testing.T) {
	f, svc := newAdjustFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	up := f.tank(t, "Line D up", 1000, 60)     // 600 LAA
	down := f.tank(t, "Line D down", 1000, 60) // 600 LAA
	when := timestamppb.New(time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC))

	for _, tc := range []struct {
		id      string
		counted float64
		why     string
	}{
		{up.ID.String(), 1010, "found 10 L more on the dip"},
		{down.ID.String(), 990, "found 10 L less on the dip"},
	} {
		if _, err := svc.RecordInventoryAdjustment(f.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
			ContainerId: tc.id,
			Reason:      stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
			Explanation: tc.why, CountedVolumeL: tc.counted, AbvPct: 60,
			OccurredAt: when,
		})); err != nil {
			t.Fatalf("RecordInventoryAdjustment: %v", err)
		}
	}

	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	rep := resp.Msg.GetReport()

	// The two cancel out, which is exactly why both directions are
	// reported: a net of zero would say nothing happened.
	if got := rep.GetBulkAdjustmentsLaa(); !near(got, 0, 1e-6) {
		t.Errorf("net adjustments: got %v, want 0", got)
	}
	if got, want := rep.GetBulkAdjustmentsIncreaseLaa(), 6.0; !near(got, want, 1e-6) {
		t.Errorf("increases: got %v, want %v", got, want)
	}
	if got, want := rep.GetBulkAdjustmentsDecreaseLaa(), 6.0; !near(got, want, 1e-6) {
		t.Errorf("decreases: got %v, want %v", got, want)
	}
	if got, want := rep.GetBulkAdjustmentsCount(), int32(2); got != want {
		t.Errorf("adjustment count: got %d, want %d", got, want)
	}
	// Not folded into losses — that is the whole complaint.
	if got := rep.GetBulkLossesLaa(); !near(got, 0, 1e-6) {
		t.Errorf("losses: got %v, want 0 — an adjustment is not a loss", got)
	}
	// And the books still close with adjustments in the walk.
	receipts := rep.GetBulkProductionLaa() + rep.GetBulkReceivedInBondLaa() +
		rep.GetBulkAdjustmentsIncreaseLaa()
	withdrawals := rep.GetBulkTransferredToPackagingLaa() + rep.GetBulkTransferredOutInBondLaa() +
		rep.GetBulkLossesLaa() + rep.GetBulkDestroyedLaa() + rep.GetBulkAdjustmentsDecreaseLaa()
	if got := rep.GetBulkOpeningLaa() + receipts - withdrawals; !near(got, rep.GetBulkClosingLaa(), 1e-6) {
		t.Errorf("bulk books don't close with adjustments: %v vs closing %v",
			got, rep.GetBulkClosingLaa())
	}
}

// The one-directional walk check: an adjustment DOWN must move the
// withdrawal side, not silently reduce the reverse-walked opening balance
// the way an unreported movement does.
func TestAdjustmentsAreInTheWalkNotAbsorbedByOpening(t *testing.T) {
	f, svc := newAdjustFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Walk tank", 1000, 60) // 600 LAA
	when := timestamppb.New(time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC))

	if _, err := svc.RecordInventoryAdjustment(f.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
		ContainerId: tank.ID.String(),
		Reason:      stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
		Explanation: "50 L short on the count", CountedVolumeL: 950, AbvPct: 60,
		OccurredAt: when,
	})); err != nil {
		t.Fatalf("RecordInventoryAdjustment: %v", err)
	}

	resp, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	rep := resp.Msg.GetReport()

	// Closing is 570; the 30 LAA shortfall is a reported withdrawal, so
	// opening walks back to the 600 that was actually on hand.
	if got, want := rep.GetBulkClosingLaa(), 570.0; !near(got, want, 1e-6) {
		t.Errorf("closing: got %v, want %v", got, want)
	}
	if got, want := rep.GetBulkOpeningLaa(), 600.0; !near(got, want, 1e-6) {
		t.Errorf("opening: got %v, want %v — an adjustment must be a reported "+
			"withdrawal, not absorbed into the opening balance", got, want)
	}
	if got, want := rep.GetBulkAdjustmentsDecreaseLaa(), 30.0; !near(got, want, 1e-6) {
		t.Errorf("decrease line: got %v, want %v", got, want)
	}
}

// An adjustment is an attributable act. The row names who made it, and the
// listing carries the reason and the explanation an auditor reads.
func TestAdjustmentsAreAttributableAndListable(t *testing.T) {
	f, svc := newAdjustFixture(t)
	tank := f.tank(t, "Attributed tank", 1000, 60)

	adjust(t, svc, f, tank.ID.String(), 995, 60,
		stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_MEASUREMENT_CORRECTION,
		"previous dip read against the wrong strapping chart")

	got, err := svc.ListInventoryAdjustments(f.ctx, connect.NewRequest(&stillhousev1.ListInventoryAdjustmentsRequest{
		ContainerId: tank.ID.String(),
	}))
	if err != nil {
		t.Fatalf("ListInventoryAdjustments: %v", err)
	}
	rows := got.Msg.GetAdjustments()
	if len(rows) != 1 {
		t.Fatalf("listed %d adjustments, want 1", len(rows))
	}
	a := rows[0]
	if got, want := a.GetAdjustedBy(), f.user.ID.String(); got != want {
		t.Errorf("adjusted_by: got %q, want %q", got, want)
	}
	if a.GetAdjustedByName() == "" {
		t.Error("the listing does not name who made the adjustment")
	}
	if got, want := a.GetReason(),
		stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_MEASUREMENT_CORRECTION; got != want {
		t.Errorf("reason: got %v, want %v", got, want)
	}
	if !strings.Contains(a.GetExplanation(), "strapping chart") {
		t.Errorf("explanation not carried: %q", a.GetExplanation())
	}
	if a.GetContainerName() == "" {
		t.Error("the listing does not name the container")
	}
}

// A filed period is closed. An adjustment backdated into one would change
// a figure CRA has already been given.
func TestAdjustmentRespectsThePeriodLock(t *testing.T) {
	f, svc := newAdjustFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Locked tank", 1000, 60)

	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-01", PeriodEnd: "2026-05-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        gen.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err != nil {
		t.Fatalf("SubmitB266: %v", err)
	}

	_, err = svc.RecordInventoryAdjustment(f.ctx, connect.NewRequest(&stillhousev1.RecordInventoryAdjustmentRequest{
		ContainerId: tank.ID.String(),
		Reason:      stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT,
		Explanation: "backdated count", CountedVolumeL: 900, AbvPct: 60,
		OccurredAt: timestamppb.New(time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)),
	}))
	if err == nil {
		t.Fatal("an adjustment landed inside a submitted period")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
}
