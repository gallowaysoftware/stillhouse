package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The pure tests in b266_continuity_test.go prove the comparison is right
// given its inputs. They cannot prove the inputs are found: that the query
// picks the last *filed* period rather than the last draft, and that
// "recorded after it was filed" means created_at against submitted_at
// rather than any of the other three date pairs available. Those are SQL
// facts and need a database.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

// The whole feature, end to end: file April, then record something into
// April, then generate May and watch it refuse to look continuous.
func TestB266Continuity_LateEntryIntoAFiledPeriodBreaksTheNextReturn(t *testing.T) {
	f := newDutyFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	tank := f.tank(t, "Continuity tank", 0, 0)
	april := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)

	// 1000 L at 40% inside April: 400 LAA.
	if err := f.movement(t, tank.ID, 1000, 40, sqlcgen.BulkMovementReasonProductionGauge, april); err != nil {
		t.Fatalf("seed april production: %v", err)
	}

	// Generate and file April.
	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 april: %v", err)
	}
	if got := gen.Msg.GetReport().GetBulkClosingLaa(); !near(got, 400, 1e-6) {
		t.Fatalf("april closing: got %v, want 400", got)
	}
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        gen.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err != nil {
		t.Fatalf("SubmitB266 april: %v", err)
	}

	// May, with nothing in it, must be continuous with the filed April.
	may, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-01", PeriodEnd: "2026-05-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 may: %v", err)
	}
	c := may.Msg.GetReport().GetContinuity()
	if !c.GetChecked() {
		t.Fatal("continuity not checked against the filed April period")
	}
	if !near(c.GetBulkDiscrepancyLaa(), 0, 1e-6) {
		t.Fatalf("clean books: discrepancy %v, prior closing %v, opening %v",
			c.GetBulkDiscrepancyLaa(), c.GetPriorBulkClosingLaa(), c.GetBulkOpeningLaa())
	}
	if c.GetPriorPeriodEnd() != "2026-04-30" {
		t.Errorf("prior period end: got %q, want 2026-04-30", c.GetPriorPeriodEnd())
	}

	// Now the thing this exists to catch: 250 L at 40% (100 LAA) recorded
	// NOW but dated into the already-filed April.
	if err := f.movement(t, tank.ID, 250, 40, sqlcgen.BulkMovementReasonProductionGauge,
		time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed backdated production: %v", err)
	}

	may2, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-01", PeriodEnd: "2026-05-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 may again: %v", err)
	}
	c2 := may2.Msg.GetReport().GetContinuity()

	if !near(c2.GetBulkDiscrepancyLaa(), 100, 1e-6) {
		t.Errorf("discrepancy: got %v, want 100 (prior closing %v, opening %v)",
			c2.GetBulkDiscrepancyLaa(), c2.GetPriorBulkClosingLaa(), c2.GetBulkOpeningLaa())
	}
	if !near(c2.GetPriorBulkClosingLaa(), 400, 1e-6) {
		t.Errorf("prior closing moved: got %v, want the filed 400", c2.GetPriorBulkClosingLaa())
	}
	if !near(c2.GetBackdatedNetLaa(), 100, 1e-6) {
		t.Errorf("backdated net: got %v, want 100", c2.GetBackdatedNetLaa())
	}
	if len(c2.GetBackdated()) != 1 {
		t.Fatalf("backdated entries: got %d, want 1: %+v", len(c2.GetBackdated()), c2.GetBackdated())
	}
	e := c2.GetBackdated()[0]
	if e.GetOccurredAt() != "2026-04-20" {
		t.Errorf("entry occurred_at: got %q, want 2026-04-20", e.GetOccurredAt())
	}
	if e.GetContainer() != "Continuity tank" {
		t.Errorf("entry container: got %q", e.GetContainer())
	}
	if !near(e.GetLaa(), 100, 1e-6) {
		t.Errorf("entry laa: got %v, want 100", e.GetLaa())
	}
	joined := strings.Join(may2.Msg.GetReport().GetFilingBlockers(), " | ")
	if !strings.Contains(joined, "accounts for the bulk difference exactly") {
		t.Errorf("blockers do not tie the entry to the break: %s", joined)
	}
}

// A draft is recomputed every time it is generated, so its closing balance
// tracks the ledger and comparing against it would always agree — a check
// that cannot fail. Only a submitted period is a fixed point.
func TestB266Continuity_DraftsAreNotComparedAgainst(t *testing.T) {
	f := newDutyFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	tank := f.tank(t, "Draft tank", 0, 0)
	if err := f.movement(t, tank.ID, 1000, 40, sqlcgen.BulkMovementReasonProductionGauge,
		time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Generate April but do NOT submit it.
	if _, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	})); err != nil {
		t.Fatalf("GenerateB266 april: %v", err)
	}

	may, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-01", PeriodEnd: "2026-05-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 may: %v", err)
	}
	if may.Msg.GetReport().GetContinuity().GetChecked() {
		t.Error("compared against a draft period — a draft's closing balance moves with the ledger")
	}
}

// A draft must not merely be rejected once selected — it must not be
// selected at all, or it shadows the filed period behind it and the
// comparison is skipped when a perfectly good one was available.
//
// This is the case the first draft test misses: there, the draft is
// discarded for carrying no snapshot and the result (unchecked) is right
// by accident. Put a submitted period behind the draft and the two
// behaviours separate.
func TestB266Continuity_ADraftDoesNotShadowTheFiledPeriodBehindIt(t *testing.T) {
	f := newDutyFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	tank := f.tank(t, "Shadow tank", 0, 0)
	// 500 LAA in March, filed.
	if err := f.movement(t, tank.ID, 1250, 40, sqlcgen.BulkMovementReasonProductionGauge,
		time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	filed, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 april: %v", err)
	}
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        filed.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err != nil {
		t.Fatalf("SubmitB266 april: %v", err)
	}

	// May exists only as a draft.
	if _, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-01", PeriodEnd: "2026-05-31",
	})); err != nil {
		t.Fatalf("GenerateB266 may draft: %v", err)
	}

	// June must reach past the May draft to the filed April.
	june, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 june: %v", err)
	}
	c := june.Msg.GetReport().GetContinuity()
	if !c.GetChecked() {
		t.Fatal("the May draft shadowed the filed April period and the check was skipped")
	}
	if c.GetPriorPeriodEnd() != "2026-04-30" {
		t.Errorf("prior period end: got %q, want the filed 2026-04-30", c.GetPriorPeriodEnd())
	}
	if !near(c.GetPriorBulkClosingLaa(), 500, 1e-6) {
		t.Errorf("prior closing: got %v, want 500", c.GetPriorBulkClosingLaa())
	}
	// April to June leaves May uncovered by any filed return, and the
	// comparison spans it, so that has to be said.
	if !c.GetGap() {
		t.Error("gap: got false for a filed April compared against a June return")
	}
}

// An entry dated into the filed period but recorded BEFORE it was filed is
// not late: the filed return counted it. Reporting those would flag every
// ordinary movement in the period and make the check noise.
func TestB266Continuity_EntriesMadeBeforeFilingAreNotLate(t *testing.T) {
	f := newDutyFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	tank := f.tank(t, "Timely tank", 0, 0)
	// Two ordinary April movements, both recorded before April is filed.
	if err := f.movement(t, tank.ID, 500, 40, sqlcgen.BulkMovementReasonProductionGauge,
		time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := f.movement(t, tank.ID, 500, 40, sqlcgen.BulkMovementReasonProductionGauge,
		time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-04-01", PeriodEnd: "2026-04-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 april: %v", err)
	}
	if _, err := b266.SubmitB266(f.ctx, connect.NewRequest(&stillhousev1.SubmitB266Request{
		PeriodId:        gen.Msg.GetPeriod().GetId(),
		Acknowledgement: filingAcknowledgementText(),
	})); err != nil {
		t.Fatalf("SubmitB266: %v", err)
	}

	may, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-05-01", PeriodEnd: "2026-05-31",
	}))
	if err != nil {
		t.Fatalf("GenerateB266 may: %v", err)
	}
	c := may.Msg.GetReport().GetContinuity()
	if n := len(c.GetBackdated()); n != 0 {
		t.Errorf("movements recorded before filing were called late: %d, %+v", n, c.GetBackdated())
	}
	if !near(c.GetBulkDiscrepancyLaa(), 0, 1e-6) {
		t.Errorf("discrepancy on continuous books: %v", c.GetBulkDiscrepancyLaa())
	}
}
