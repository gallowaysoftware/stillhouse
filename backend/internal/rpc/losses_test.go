package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN A5. `bulk_losses_laa` was one number. Under EDM3-4-1 the treatment
// diverges sharply: a destruction approved by CRA is relieved, while
// spirits that cannot be accounted for are duty-payable and cost real
// money. Collapsing the two produces a plausible total and the wrong duty.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

var lossWhen = timestamppb.New(time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC))

func recordLoss(t *testing.T, svc *BulkService, f *dutyFixture, containerID string,
	kind stillhousev1.BulkExternalMovementKind, volumeL float64,
	treatment stillhousev1.LossDutyTreatment, authority string,
) *stillhousev1.RecordBulkExternalMovementResponse {
	t.Helper()
	resp, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: containerID, Kind: kind, VolumeL: volumeL, AbvPct: 60,
		OccurredAt: lossWhen, LossDutyTreatment: treatment, LossTreatmentAuthority: authority,
	}))
	if err != nil {
		t.Fatalf("RecordBulkExternalMovement: %v", err)
	}
	return resp.Msg
}

// A loss with no ruling on it is reported as unclassified rather than
// guessed either way. Stillhouse does not know whether a given evaporation
// loss is relieved, and the barrel regauge that wrote it did not ask.
func TestUnclassifiedLossesAreReportedAsSuchAndBlockFiling(t *testing.T) {
	f, svc := newExternalFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Unclassified tank", 1000, 60)

	recordLoss(t, svc, f, tank.ID.String(),
		stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_UNACCOUNTED_LOSS,
		50, stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNSPECIFIED, "")

	rep, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	r := rep.Msg.GetReport()

	if got, want := r.GetBulkLossesUnclassifiedLaa(), 30.0; !near(got, want, 1e-6) {
		t.Errorf("unclassified losses: got %v, want %v", got, want)
	}
	if got := r.GetDutyOnLossesCad(); got != 0 {
		t.Errorf("duty was charged on an unclassified loss: %v — nobody has said it is dutiable", got)
	}
	// And the period says out loud that it cannot be filed.
	if len(r.GetFilingBlockers()) == 0 {
		t.Fatal("a period with unclassified losses reported no filing blockers")
	}
	if !strings.Contains(r.GetFilingBlockers()[0], "duty treatment") {
		t.Errorf("blocker does not explain itself: %q", r.GetFilingBlockers()[0])
	}
}

// The three treatments always sum to the losses total, and the dutiable
// ones — and only those — are charged.
func TestLossesSplitByTreatmentAndOnlyDutiableOnesAreCharged(t *testing.T) {
	f, svc := newExternalFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Split tank", 2000, 60)

	// 100 L (60 LAA) that cannot be accounted for, ruled dutiable.
	recordLoss(t, svc, f, tank.ID.String(),
		stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_UNACCOUNTED_LOSS,
		100, stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_DUTIABLE, "")
	// 50 L (30 LAA) relieved, on a stated authority.
	recordLoss(t, svc, f, tank.ID.String(),
		stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_UNACCOUNTED_LOSS,
		50, stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_RELIEVED, "EDM3-4-1, approved in writing")

	rep, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	r := rep.Msg.GetReport()

	if got, want := r.GetBulkLossesDutiableLaa(), 60.0; !near(got, want, 1e-6) {
		t.Errorf("dutiable losses: got %v, want %v", got, want)
	}
	if got, want := r.GetBulkLossesRelievedLaa(), 30.0; !near(got, want, 1e-6) {
		t.Errorf("relieved losses: got %v, want %v", got, want)
	}
	// The split has to reconcile with the total, or the return contradicts
	// itself in two places on the same page.
	sum := r.GetBulkLossesRelievedLaa() + r.GetBulkLossesDutiableLaa() + r.GetBulkLossesUnclassifiedLaa()
	if !near(sum, r.GetBulkLossesLaa(), 1e-6) {
		t.Errorf("the three treatments sum to %v but the losses line says %v", sum, r.GetBulkLossesLaa())
	}

	band, err := excise.RateOn(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	want := 60 * band.PerLAAOver7Pct
	if got := r.GetDutyOnLossesCad(); !near(got, want, 0.011) {
		t.Errorf("duty on losses: got %v, want %v (the dutiable 60 LAA only)", got, want)
	}
	// And it reaches the total. A return that leaves it out understates
	// what is owed.
	if got := r.GetDutyPayableCad(); !near(got, want, 0.011) {
		t.Errorf("duty payable: got %v, want %v — duty on losses has to reach the total", got, want)
	}
	if len(r.GetFilingBlockers()) != 0 {
		t.Errorf("a fully classified period still reports blockers: %v", r.GetFilingBlockers())
	}
}

// Relief that rests on nothing is not relief.
func TestRelievedLossNeedsAnAuthority(t *testing.T) {
	f, svc := newExternalFixture(t)
	tank := f.tank(t, "Groundless tank", 1000, 60)

	_, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: tank.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DESTRUCTION,
		VolumeL:     50, AbvPct: 60,
		LossDutyTreatment: stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_RELIEVED,
	}))
	if err == nil {
		t.Fatal("a destruction was relieved with no authority on file")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
	}
	if !strings.Contains(err.Error(), "approval reference") {
		t.Errorf("message does not say what is missing: %v", err)
	}
}

// A duty treatment on something that is not a loss would be counted
// nowhere and believed by the operator, so it is refused rather than
// silently dropped.
func TestDutyTreatmentIsRefusedOnAMovementThatIsNotALoss(t *testing.T) {
	f, svc := newExternalFixture(t)
	tank := f.tank(t, "Not-a-loss tank", 1000, 60)

	_, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: tank.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_EXPORT,
		VolumeL:     50, AbvPct: 60, CounterpartyName: "Importer SARL",
		LossDutyTreatment:      stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_RELIEVED,
		LossTreatmentAuthority: "not applicable",
	}))
	if err == nil {
		t.Fatal("an export was given a loss duty treatment")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
	}
}

// The list an operator works through at period end, and the bulk ruling
// that clears it. A dozen evaporation losses are one decision, not twelve.
func TestListAndClassifyLossesInBulk(t *testing.T) {
	f, svc := newExternalFixture(t)
	tank := f.tank(t, "Bulk-classify tank", 3000, 60)

	var ids []string
	for i := 0; i < 3; i++ {
		got := recordLoss(t, svc, f, tank.ID.String(),
			stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_UNACCOUNTED_LOSS,
			10, stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNSPECIFIED, "")
		ids = append(ids, got.GetMovement().GetId())
	}

	outstanding, err := svc.ListLosses(f.ctx, connect.NewRequest(&stillhousev1.ListLossesRequest{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30", UnclassifiedOnly: true,
	}))
	if err != nil {
		t.Fatalf("ListLosses: %v", err)
	}
	if n := len(outstanding.Msg.GetLosses()); n != 3 {
		t.Fatalf("outstanding losses: got %d, want 3", n)
	}
	// The cost of ruling each one dutiable is shown before the decision,
	// not discovered on the return.
	if got := outstanding.Msg.GetLosses()[0].GetDutyIfDutiableCad(); got <= 0 {
		t.Errorf("an unclassified loss shows %v duty if dutiable — the price should be visible", got)
	}

	if _, err := svc.ClassifyLosses(f.ctx, connect.NewRequest(&stillhousev1.ClassifyLossesRequest{
		MovementIds: ids,
		Treatment:   stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_DUTIABLE,
	})); err != nil {
		t.Fatalf("ClassifyLosses: %v", err)
	}

	after, err := svc.ListLosses(f.ctx, connect.NewRequest(&stillhousev1.ListLossesRequest{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30", UnclassifiedOnly: true,
	}))
	if err != nil {
		t.Fatalf("ListLosses: %v", err)
	}
	if n := len(after.Msg.GetLosses()); n != 0 {
		t.Errorf("%d losses still outstanding after a bulk ruling", n)
	}

	all, err := svc.ListLosses(f.ctx, connect.NewRequest(&stillhousev1.ListLossesRequest{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("ListLosses: %v", err)
	}
	if got, want := all.Msg.GetDutiableLaa(), 18.0; !near(got, want, 1e-6) {
		t.Errorf("dutiable total: got %v, want %v", got, want)
	}
	// The ruling is attributable.
	if all.Msg.GetLosses()[0].GetClassifiedBy() != f.user.ID.String() {
		t.Error("the ruling does not name who made it")
	}
}

// Classifying is a ruling on a loss, not a way to attach a duty treatment
// to anything else in the ledger.
func TestClassifyRefusesAMovementThatIsNotALoss(t *testing.T) {
	f, svc := newExternalFixture(t)
	tank := f.tank(t, "Wrong-target tank", 1000, 60)

	moved, err := svc.RecordBulkExternalMovement(f.ctx, connect.NewRequest(&stillhousev1.RecordBulkExternalMovementRequest{
		ContainerId: tank.ID.String(),
		Kind:        stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_EXPORT,
		VolumeL:     50, AbvPct: 60, CounterpartyName: "Importer SARL", OccurredAt: lossWhen,
	}))
	if err != nil {
		t.Fatalf("RecordBulkExternalMovement: %v", err)
	}
	_, err = svc.ClassifyLosses(f.ctx, connect.NewRequest(&stillhousev1.ClassifyLossesRequest{
		MovementIds: []string{moved.Msg.GetMovement().GetId()},
		Treatment:   stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_DUTIABLE,
	}))
	if err == nil {
		t.Fatal("an export was classified as a loss")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
}

// Reclassifying inside a filed period would change the duty on a return
// CRA already has.
func TestClassifyRespectsThePeriodLock(t *testing.T) {
	f, svc := newExternalFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Filed tank", 1000, 60)

	got := recordLoss(t, svc, f, tank.ID.String(),
		stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_UNACCOUNTED_LOSS,
		10, stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_DUTIABLE, "")

	gen, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
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

	_, err = svc.ClassifyLosses(f.ctx, connect.NewRequest(&stillhousev1.ClassifyLossesRequest{
		MovementIds: []string{got.GetMovement().GetId()},
		Treatment:   stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_RELIEVED,
		Authority:   "changed my mind",
	}))
	if err == nil {
		t.Fatal("a loss inside a submitted period was reclassified")
	}
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", code, err)
	}
}

// An approved destruction is relieved; one nobody approved is not. Both
// are reported on the destruction line, which is separate from losses —
// counting a destruction in both places would overstate what left.
func TestDestructionsCarryTheirOwnTreatmentAndLine(t *testing.T) {
	f, svc := newExternalFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Destruction tank", 2000, 60)

	recordLoss(t, svc, f, tank.ID.String(),
		stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DESTRUCTION,
		100, stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_RELIEVED, "CRA approval 2026-114")

	rep, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	r := rep.Msg.GetReport()

	if got, want := r.GetBulkDestroyedLaa(), 60.0; !near(got, want, 1e-6) {
		t.Errorf("destroyed: got %v, want %v", got, want)
	}
	// Not double-counted as a loss.
	if got := r.GetBulkLossesLaa(); !near(got, 0, 1e-6) {
		t.Errorf("losses: got %v, want 0 — a destruction has its own line", got)
	}
	if got := r.GetDutyOnLossesCad(); got != 0 {
		t.Errorf("duty charged on an approved destruction: %v — approved destructions are relieved", got)
	}
	if len(r.GetFilingBlockers()) != 0 {
		t.Errorf("a classified destruction still blocks filing: %v", r.GetFilingBlockers())
	}
}

// An unapproved destruction is not relieved, and the period says so.
func TestUnclassifiedDestructionBlocksFiling(t *testing.T) {
	f, svc := newExternalFixture(t)
	b266 := NewB266Service(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Unapproved tank", 1000, 60)

	recordLoss(t, svc, f, tank.ID.String(),
		stillhousev1.BulkExternalMovementKind_BULK_EXTERNAL_MOVEMENT_KIND_DESTRUCTION,
		100, stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNSPECIFIED, "")

	rep, err := b266.GenerateB266(f.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{
		PeriodStart: "2026-06-01", PeriodEnd: "2026-06-30",
	}))
	if err != nil {
		t.Fatalf("GenerateB266: %v", err)
	}
	blockers := rep.Msg.GetReport().GetFilingBlockers()
	if len(blockers) == 0 {
		t.Fatal("an unclassified destruction did not block filing")
	}
	if !strings.Contains(strings.Join(blockers, " "), "approval") {
		t.Errorf("blocker does not mention the approval: %v", blockers)
	}
}
