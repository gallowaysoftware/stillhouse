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
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// PLAN C1. EDM3-1-1 ¶24 and EDM1-1-5: volume and absolute alcohol content
// must be determined using CRA-approved instruments, and each individual
// instrument must itself be approved — approval attaches to the serial
// number in front of the operator, not to the model on the box.
//
// Stillhouse recorded how a figure was determined and nothing about what
// determined it, so the audit chain ran quantity → movement →
// determination → nothing.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

type instrumentFixture struct {
	*dutyFixture
	svc *InstrumentService
}

func newInstrumentFixture(t *testing.T) *instrumentFixture {
	t.Helper()
	f := newDutyFixture(t)
	return &instrumentFixture{
		dutyFixture: f,
		svc:         NewInstrumentService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil))),
	}
}

// register creates an instrument. An empty approval reference is the state
// a newly registered instrument starts in, and the state that makes a
// determination refuse.
func (f *instrumentFixture) register(t *testing.T, kind stillhousev1.InstrumentKind,
	label, approval string) *stillhousev1.Instrument {
	t.Helper()
	resp, err := f.svc.CreateInstrument(f.ctx, connect.NewRequest(&stillhousev1.CreateInstrumentRequest{
		Kind: kind, Label: label,
		SerialNo:          "SN-" + uuid.NewString()[:8],
		ApprovalReference: approval,
	}))
	if err != nil {
		t.Fatalf("CreateInstrument(%s): %v", label, err)
	}
	return resp.Msg.GetInstrument()
}

// An instrument register entry without a serial cannot be matched to an
// approval, because the approval is granted against the serial.
func TestCreateInstrumentRequiresASerial(t *testing.T) {
	f := newInstrumentFixture(t)
	_, err := f.svc.CreateInstrument(f.ctx, connect.NewRequest(&stillhousev1.CreateInstrumentRequest{
		Kind:  stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
		Label: "Nameless hydrometer",
	}))
	if err == nil {
		t.Fatal("an instrument with no serial was registered")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
	}
}

// Two rows cannot claim the same physical instrument.
func TestInstrumentSerialsAreUniquePerTenant(t *testing.T) {
	f := newInstrumentFixture(t)
	serial := "DUP-" + uuid.NewString()[:8]
	mk := func() error {
		_, err := f.svc.CreateInstrument(f.ctx, connect.NewRequest(&stillhousev1.CreateInstrumentRequest{
			Kind:  stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
			Label: "Hydro", SerialNo: serial, ApprovalReference: "CRA-1",
		}))
		return err
	}
	if err := mk(); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	err := mk()
	if err == nil {
		t.Fatal("the same serial was registered twice")
	}
	if got := connect.CodeOf(err); got != connect.CodeAlreadyExists {
		t.Errorf("code = %v, want already_exists (err: %v)", got, err)
	}
}

// The rule that makes the register worth having: a determination that
// names an instrument with no CRA approval on file is refused. Naming an
// unapproved instrument documents a compliance gap, which is worse than
// naming none.
func TestGaugeRefusesAnUnapprovedInstrument(t *testing.T) {
	f := newInstrumentFixture(t)
	unapproved := f.register(t, stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
		"Uncertified hydrometer", "")

	barrelSvc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Instrument tank", 1000, 60)
	barrel := f.barrel(t, "Instrument barrel", 250)

	_, err := barrelSvc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 100, AbvPct: 60,
		Instruments: &stillhousev1.InstrumentRefs{StrengthInstrumentId: unapproved.GetId()},
	}))
	if err == nil {
		t.Fatal("a determination was accepted against an instrument with no CRA approval")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
	}
	// The operator has to be told which instrument and why.
	if msg := err.Error(); !strings.Contains(msg, "no CRA approval reference") || !strings.Contains(msg, unapproved.GetSerialNo()) {
		t.Errorf("message doesn't name the instrument or the reason: %v", err)
	}
}

// Naming nothing stays allowed. The register starts empty, and refusing
// every gauge until it is populated would take the distillery off the air
// to fix a paperwork gap.
func TestGaugeWithNoInstrumentNamedIsAllowed(t *testing.T) {
	f := newInstrumentFixture(t)
	barrelSvc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Bare tank", 1000, 60)
	barrel := f.barrel(t, "Bare barrel", 250)

	resp, err := barrelSvc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 100, AbvPct: 60,
	}))
	if err != nil {
		t.Fatalf("FillBarrel with no instruments named: %v", err)
	}
	// And it says so rather than implying one: a row that names no
	// instrument comes back with no instrument block.
	if got := resp.Msg.GetEvent().GetInstruments(); got != nil {
		t.Errorf("an event naming no instrument reported %v", got)
	}
}

// An approved instrument is accepted, stored, and resolves back on the
// read — the whole chain, end to end.
func TestApprovedInstrumentsAreRecordedAndResolveBack(t *testing.T) {
	f := newInstrumentFixture(t)
	hydro := f.register(t, stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
		"Still house hydro #2", "CRA-APP-11923")
	thermo := f.register(t, stillhousev1.InstrumentKind_INSTRUMENT_KIND_THERMOMETER,
		"Bench thermometer", "CRA-APP-40021")

	barrelSvc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Chain tank", 1000, 60)
	barrel := f.barrel(t, "Chain barrel", 250)

	if _, err := barrelSvc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 100, AbvPct: 60,
		Instruments: &stillhousev1.InstrumentRefs{
			StrengthInstrumentId:    hydro.GetId(),
			TemperatureInstrumentId: thermo.GetId(),
		},
	})); err != nil {
		t.Fatalf("FillBarrel: %v", err)
	}

	got, err := barrelSvc.GetBarrel(f.ctx, connect.NewRequest(&stillhousev1.GetBarrelRequest{
		Id: barrel.ID.String(),
	}))
	if err != nil {
		t.Fatalf("GetBarrel: %v", err)
	}
	events := got.Msg.GetEvents()
	if len(events) == 0 {
		t.Fatal("no events on the barrel")
	}
	instr := events[0].GetInstruments()
	if instr == nil {
		t.Fatal("the determination came back with no instruments — the audit chain is still broken at the last link")
	}
	if got, want := instr.GetStrength().GetSerialNo(), hydro.GetSerialNo(); got != want {
		t.Errorf("strength instrument: got %q, want %q", got, want)
	}
	if got, want := instr.GetTemperature().GetSerialNo(), thermo.GetSerialNo(); got != want {
		t.Errorf("temperature instrument: got %q, want %q", got, want)
	}
	if !instr.GetStrength().GetUsable() {
		t.Errorf("an approved active instrument reported unusable: %q", instr.GetStrength().GetUnusableReason())
	}
	// The volume role was not named, and must not be invented.
	if instr.GetVolume() != nil {
		t.Errorf("a volume instrument appeared that was never named: %v", instr.GetVolume())
	}
}

// Retiring or suspending an instrument stops it backing future
// determinations. It is never deleted, so what it already determined
// still resolves.
func TestRetiredAndSuspendedInstrumentsAreRefused(t *testing.T) {
	f := newInstrumentFixture(t)
	barrelSvc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Status tank", 5000, 60)

	for _, tc := range []struct {
		name   string
		status stillhousev1.InstrumentStatus
		want   string
	}{
		{"retired", stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_RETIRED, "is retired"},
		{"suspended", stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_SUSPENDED, "is suspended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := f.register(t, stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
				"Withdrawn "+tc.name, "CRA-APP-5")
			if _, err := f.svc.SetInstrumentStatus(f.ctx, connect.NewRequest(&stillhousev1.SetInstrumentStatusRequest{
				Id: inst.GetId(), Status: tc.status, Reason: "dropped on the floor",
			})); err != nil {
				t.Fatalf("SetInstrumentStatus: %v", err)
			}
			barrel := f.barrel(t, "Status barrel "+tc.name, 250)
			_, err := barrelSvc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
				BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
				VolumeL: 100, AbvPct: 60,
				Instruments: &stillhousev1.InstrumentRefs{StrengthInstrumentId: inst.GetId()},
			}))
			if err == nil {
				t.Fatalf("a %s instrument backed a determination", tc.name)
			}
			if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
				t.Errorf("code = %v, want failed_precondition (err: %v)", got, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message doesn't say the instrument is %s: %v", tc.name, err)
			}
		})
	}
}

// Withdrawing an instrument is the entry an auditor reads, so it needs a
// reason. Putting one back in service does not.
func TestWithdrawingAnInstrumentNeedsAReason(t *testing.T) {
	f := newInstrumentFixture(t)
	inst := f.register(t, stillhousev1.InstrumentKind_INSTRUMENT_KIND_SCALE, "Platform scale", "CRA-APP-9")

	_, err := f.svc.SetInstrumentStatus(f.ctx, connect.NewRequest(&stillhousev1.SetInstrumentStatusRequest{
		Id: inst.GetId(), Status: stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_RETIRED,
	}))
	if err == nil {
		t.Fatal("an instrument was retired with no reason recorded")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
	}
	if _, err := f.svc.SetInstrumentStatus(f.ctx, connect.NewRequest(&stillhousev1.SetInstrumentStatusRequest{
		Id: inst.GetId(), Status: stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
	})); err != nil {
		t.Errorf("returning an instrument to service needed a reason: %v", err)
	}
}

// An approval that has lapsed is judged against the date of the
// determination, not against today: an instrument whose approval has since
// expired was still approved when the reading was taken, and a backdated
// correction must not be judged against today's register.
func TestApprovalExpiryIsJudgedAtTheDateOfTheDetermination(t *testing.T) {
	row := sqlcgen.Instrument{
		Label: "Lapsing hydro", SerialNo: "LAPSE-1",
		ApprovalReference: "CRA-APP-77",
		Status:            sqlcgen.InstrumentStatusActive,
		ApprovalExpiresOn: pgtype.Date{Valid: true, Time: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range []struct {
		name       string
		on         time.Time
		wantUsable bool
	}{
		{"a determination before the approval lapsed", time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC), true},
		{"on the day it lapsed", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), false},
		{"after it lapsed", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usable, reason := instrumentUsability(row, tc.on)
			if usable != tc.wantUsable {
				t.Errorf("usable = %v, want %v (reason: %q)", usable, tc.wantUsable, reason)
			}
		})
	}
}

// Calibration is a warning, not a refusal: an instrument still approved
// but overdue for a check is a compliance risk rather than a false
// reading. An interval nobody set is not a deadline anybody missed.
func TestCalibrationDue(t *testing.T) {
	asOf := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	withInterval := func(days int32) sqlcgen.Instrument {
		return sqlcgen.Instrument{CalibrationIntervalDays: pgtype.Int4{Int32: days, Valid: days > 0}}
	}
	date := func(y int, m time.Month, d int) pgtype.Date {
		return pgtype.Date{Valid: true, Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
	}

	for _, tc := range []struct {
		name        string
		inst        sqlcgen.Instrument
		last        pgtype.Date
		wantOverdue bool
		wantDue     bool
	}{
		{"no interval set is never overdue", withInterval(0), pgtype.Date{}, false, false},
		{"no interval, even with a stale calibration", withInterval(0), date(2020, 1, 1), false, false},
		{"an interval with no calibration on file is overdue from the start",
			withInterval(365), pgtype.Date{}, true, false},
		{"calibrated inside the interval", withInterval(365), date(2026, 6, 1), false, true},
		{"calibrated outside the interval", withInterval(30), date(2026, 6, 1), true, true},
		{"exactly on the due date is not yet overdue", withInterval(30), date(2026, 7, 22), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			due, overdue := calibrationDue(tc.inst, tc.last, asOf)
			if overdue != tc.wantOverdue {
				t.Errorf("overdue = %v, want %v", overdue, tc.wantOverdue)
			}
			if due.Valid != tc.wantDue {
				t.Errorf("due date set = %v, want %v", due.Valid, tc.wantDue)
			}
		})
	}
}

// An overdue instrument still works — and says so, loudly, in the
// response rather than only in a report nobody opens on a Tuesday.
func TestOverdueCalibrationWarnsButDoesNotRefuse(t *testing.T) {
	f := newInstrumentFixture(t)
	resp, err := f.svc.CreateInstrument(f.ctx, connect.NewRequest(&stillhousev1.CreateInstrumentRequest{
		Kind:  stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
		Label: "Overdue hydro", SerialNo: "OD-" + uuid.NewString()[:8],
		ApprovalReference:       "CRA-APP-31",
		CalibrationIntervalDays: 30,
	}))
	if err != nil {
		t.Fatalf("CreateInstrument: %v", err)
	}
	inst := resp.Msg.GetInstrument()
	// Calibrated well outside its own interval.
	if _, err := f.svc.RecordCalibration(f.ctx, connect.NewRequest(&stillhousev1.RecordCalibrationRequest{
		InstrumentId: inst.GetId(),
		CalibratedOn: time.Now().AddDate(0, 0, -400).Format("2006-01-02"),
		PerformedBy:  "Measurement Canada",
		Passed:       true,
	})); err != nil {
		t.Fatalf("RecordCalibration: %v", err)
	}

	barrelSvc := NewBarrelService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	tank := f.tank(t, "Overdue tank", 1000, 60)
	barrel := f.barrel(t, "Overdue barrel", 250)
	filled, err := barrelSvc.FillBarrel(f.ctx, connect.NewRequest(&stillhousev1.FillBarrelRequest{
		BarrelId: barrel.ID.String(), SourceContainerId: tank.ID.String(),
		VolumeL: 100, AbvPct: 60,
		Instruments: &stillhousev1.InstrumentRefs{StrengthInstrumentId: inst.GetId()},
	}))
	if err != nil {
		t.Fatalf("an overdue instrument was refused, not warned: %v", err)
	}
	if len(filled.Msg.GetWarnings()) == 0 {
		t.Error("an overdue instrument produced no warning — it must be said out loud")
	}
	for _, w := range filled.Msg.GetWarnings() {
		if strings.Contains(w, "past due for calibration") {
			return
		}
	}
	t.Errorf("warnings don't mention calibration: %v", filled.Msg.GetWarnings())
}

// A calibration dated in the future would push the next due date out on
// the strength of a check that has not happened.
func TestCalibrationCannotBeDatedInTheFuture(t *testing.T) {
	f := newInstrumentFixture(t)
	inst := f.register(t, stillhousev1.InstrumentKind_INSTRUMENT_KIND_THERMOMETER, "Future thermo", "CRA-APP-2")
	_, err := f.svc.RecordCalibration(f.ctx, connect.NewRequest(&stillhousev1.RecordCalibrationRequest{
		InstrumentId: inst.GetId(),
		CalibratedOn: time.Now().AddDate(0, 0, 30).Format("2006-01-02"),
		Passed:       true,
	}))
	if err == nil {
		t.Fatal("a calibration was recorded for a date that has not happened")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument (err: %v)", got, err)
	}
}

// A failed check is history worth keeping, but it is not the date the
// next calibration is counted from — an instrument that failed its check
// has not been calibrated.
func TestAFailedCalibrationDoesNotResetTheClock(t *testing.T) {
	f := newInstrumentFixture(t)
	resp, err := f.svc.CreateInstrument(f.ctx, connect.NewRequest(&stillhousev1.CreateInstrumentRequest{
		Kind:  stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER,
		Label: "Failing hydro", SerialNo: "FAIL-" + uuid.NewString()[:8],
		ApprovalReference:       "CRA-APP-12",
		CalibrationIntervalDays: 30,
	}))
	if err != nil {
		t.Fatalf("CreateInstrument: %v", err)
	}
	id := resp.Msg.GetInstrument().GetId()

	// A pass long ago, then a failure yesterday.
	for _, c := range []struct {
		daysAgo int
		passed  bool
	}{{400, true}, {1, false}} {
		if _, err := f.svc.RecordCalibration(f.ctx, connect.NewRequest(&stillhousev1.RecordCalibrationRequest{
			InstrumentId: id,
			CalibratedOn: time.Now().AddDate(0, 0, -c.daysAgo).Format("2006-01-02"),
			Passed:       c.passed,
		})); err != nil {
			t.Fatalf("RecordCalibration: %v", err)
		}
	}

	got, err := f.svc.GetInstrument(f.ctx, connect.NewRequest(&stillhousev1.GetInstrumentRequest{Id: id}))
	if err != nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if !got.Msg.GetInstrument().GetCalibrationOverdue() {
		t.Error("a failed check reset the calibration clock — a failure is not a calibration")
	}
	if n := len(got.Msg.GetCalibrations()); n != 2 {
		t.Errorf("calibration history has %d entries, want 2 — a failure is history worth keeping", n)
	}
}
