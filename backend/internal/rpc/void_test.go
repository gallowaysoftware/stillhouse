package rpc

import (
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

type stillFixture struct {
	*ledgerFixture
	dist *DistillationService
	b266 *B266Service
	tank sqlcgen.BulkContainer
}

func newStillFixture(t *testing.T) *stillFixture {
	f := newLedgerFixture(t)
	log := slog.New(slog.DiscardHandler)
	return &stillFixture{ledgerFixture: f, dist: NewDistillationService(f.db, log), b266: NewB266Service(f.db, log), tank: f.tank(t, "T", 0, 0)}
}

func (s *stillFixture) run(t *testing.T) string {
	t.Helper()
	r, err := s.dist.CreateDistillationRun(s.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{StillLabel: "S"}))
	must(t, err)
	return r.Msg.GetRun().GetId()
}

func (s *stillFixture) gauge(run string, vol, abv float64, at time.Time) error {
	_, err := s.dist.RecordProductionGauge(s.ctx, connect.NewRequest(&stillhousev1.RecordProductionGaugeRequest{
		DistillationRunId: run, DestinationContainerId: s.tank.ID.String(), VolumeL: vol, AbvPct: abv, GaugeDate: timestamppb.New(at)}))
	return err
}

func (s *stillFixture) void(run string) error {
	_, err := s.dist.VoidDistillationRun(s.ctx, connect.NewRequest(&stillhousev1.VoidDistillationRunRequest{Id: run, Reason: "test"}))
	return err
}

func (s *stillFixture) period(t *testing.T, start, end string) *stillhousev1.B266Report {
	t.Helper()
	r, err := s.b266.GenerateB266(s.ctx, connect.NewRequest(&stillhousev1.GenerateB266Request{PeriodStart: start, PeriodEnd: end}))
	must(t, err)
	return r.Msg.GetReport()
}

// file generates the period and marks it submitted.
func (s *stillFixture) file(t *testing.T, start, end string) {
	t.Helper()
	s.period(t, start, end)
	_, err := s.pool.Exec(s.ctx, `UPDATE b266_periods SET status = 'submitted', submitted_at = NOW(), snapshot = '{}',
		filing_acknowledged_at = NOW(), filing_acknowledgement = 'x' WHERE tenant_id = $1 AND period_start = $2::date`,
		s.tenant.ID, start)
	must(t, err)
}

func TestAVoidedRunIsClosed(t *testing.T) {
	const precondition = connect.CodeFailedPrecondition
	s := newStillFixture(t)
	now := time.Now()
	voided := s.run(t)
	must(t, s.void(voided))
	refusedWith(t, "gauging it", s.gauge(voided, 50, 60, now), precondition)
	_, err := s.dist.AddDistillationCut(s.ctx, connect.NewRequest(&stillhousev1.AddDistillationCutRequest{
		DistillationRunId: voided, Kind: stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEARTS, VolumeL: 1, AbvPct: 60}))
	refusedWith(t, "a cut on it", err, precondition)
	status := func(id string, st stillhousev1.DistillationStatus) error {
		_, err := s.dist.UpdateDistillationStatus(s.ctx, connect.NewRequest(&stillhousev1.UpdateDistillationStatusRequest{Id: id, Status: st}))
		return err
	}
	refusedWith(t, "moving it", status(voided, stillhousev1.DistillationStatus_DISTILLATION_STATUS_DISTILLING), precondition)

	cancelled := s.run(t)
	must(t, status(cancelled, stillhousev1.DistillationStatus_DISTILLATION_STATUS_CANCELLED))
	refusedWith(t, "gauging a cancelled run", s.gauge(cancelled, 50, 60, now), precondition)

	live := s.run(t)
	refusedWith(t, "gauged without a gauge", status(live, stillhousev1.DistillationStatus_DISTILLATION_STATUS_GAUGED), precondition)
	must(t, s.gauge(live, 50, 60, now))
	refusedWith(t, "un-gauging", status(live, stillhousev1.DistillationStatus_DISTILLATION_STATUS_DISTILLING), precondition)
	s.reconcile(t, s.tank)
}

// A gauge voided in a later month leaves both months as if it never was.
func TestAVoidLeavesNoTraceInEitherMonth(t *testing.T) {
	s := newStillFixture(t)
	sep := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	must(t, s.gauge(s.run(t), 100, 60, sep))
	wrong := s.run(t)
	must(t, s.gauge(wrong, 100, 50, sep))
	must(t, s.void(wrong))
	for _, p := range []struct {
		start, end           string
		opening, made, close float64
	}{
		{"2026-09-01", "2026-09-30", 0, 60, 60},
		{"2026-10-01", "2026-10-31", 60, 0, 60},
	} {
		r := s.period(t, p.start, p.end)
		if !near(r.GetBulkOpeningLaa(), p.opening, 1e-4) || !near(r.GetBulkProductionLaa(), p.made, 1e-4) || !near(r.GetBulkClosingLaa(), p.close, 1e-4) {
			t.Errorf("%s: opening %v, production %v, closing %v; want %v, %v, %v", p.start,
				r.GetBulkOpeningLaa(), r.GetBulkProductionLaa(), r.GetBulkClosingLaa(), p.opening, p.made, p.close)
		}
	}
}

// The period a void touches is the one the gauge is counted in, which is not
// always the one the run started in.
func TestTheVoidLockReadsTheGaugeDate(t *testing.T) {
	s := newStillFixture(t)
	at := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	gaugedInSep, ungauged := s.run(t), s.run(t)
	must(t, s.gauge(gaugedInSep, 100, 60, at))
	s.file(t, "2026-09-01", "2026-09-30")
	// The run itself is dated today, outside the filed period.
	refusedWith(t, "voiding a gauge in a filed month", s.void(gaugedInSep), connect.CodeFailedPrecondition)
	must(t, s.void(ungauged))
	oct := s.run(t)
	must(t, s.gauge(oct, 10, 60, time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)))
	_, err := s.pool.Exec(s.ctx, "UPDATE distillation_runs SET run_date = '2026-09-30' WHERE id = $1", oct)
	must(t, err)
	must(t, s.void(oct))
}

func TestAClosedBillStaysClosed(t *testing.T) {
	const precondition = connect.CodeFailedPrecondition
	b := newFloorBatch(t)
	tank := b.tank(t, "T", 0, 0)
	mats, err := b.mat.ListMaterials(b.ctx, connect.NewRequest(&stillhousev1.ListMaterialsRequest{}))
	must(t, err)
	add := func(mash string) error {
		_, err := b.mash.AddMashIngredient(b.ctx, connect.NewRequest(&stillhousev1.AddMashIngredientRequest{
			MashRunId: mash, MaterialId: mats.Msg.GetMaterials()[0].GetId(), MaterialLotId: b.lotID, QuantityUsed: 1, Uom: "kg"}))
		return err
	}
	charge := func() error {
		_, err := b.dist.AddDistillationCharge(b.ctx, connect.NewRequest(&stillhousev1.AddDistillationChargeRequest{
			DistillationRunId: b.runID, FermentationRunId: b.fermID, VolumeChargedL: 5, AbvPct: 8}))
		return err
	}
	_, err = b.dist.RecordProductionGauge(b.ctx, connect.NewRequest(&stillhousev1.RecordProductionGaugeRequest{
		DistillationRunId: b.runID, DestinationContainerId: tank.ID.String(), VolumeL: 2, AbvPct: 60}))
	must(t, err)
	refusedWith(t, "grain into a gauged mash", add(b.mashID), precondition)
	refusedWith(t, "wash into a gauged run", charge(), precondition)
	if got := b.onHand(t); got != 19 {
		t.Errorf("the refused grain came off the lot: %v on hand, want 19", got)
	}

	_, err = b.mash.UpdateMashStatus(b.ctx, connect.NewRequest(&stillhousev1.UpdateMashStatusRequest{
		Id: b.mashID, Status: stillhousev1.MashStatus_MASH_STATUS_CANCELLED}))
	must(t, err)
	_, err = b.ferm.CreateFermentationRun(b.ctx, connect.NewRequest(&stillhousev1.CreateFermentationRunRequest{
		MashRunId: b.mashID, FermenterLabel: "FV9"}))
	refusedWith(t, "pitching a cancelled mash", err, precondition)
}
