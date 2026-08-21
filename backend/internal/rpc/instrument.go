package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// The instrument register closes the last link in the audit chain.
//
// EDM3-1-1 ¶24 and EDM1-1-5: volume and absolute alcohol content must be
// determined using CRA-approved instruments, and each individual
// instrument must itself be approved — approval attaches to the serial
// number in front of the operator, not to the model on the box. Stillhouse
// recorded how a figure was determined and nothing about what determined
// it, so the trail ran quantity → movement → determination → nothing.

type InstrumentService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewInstrumentService(db *tenantdb.DB, logger *slog.Logger) *InstrumentService {
	return &InstrumentService{db: db, logger: logger}
}

// instrumentUsability decides whether an instrument may back a
// duty-relevant determination, and says why not when it may not.
//
// The rule is deliberately narrow. An instrument that is named but cannot
// be used is refused, because naming an unapproved instrument documents a
// gap — it is worse than naming none. An instrument that is merely overdue
// for calibration is a warning: it is still approved, and an overdue check
// is a compliance risk rather than a false reading.
func instrumentUsability(i sqlcgen.Instrument, on time.Time) (usable bool, reason string) {
	switch i.Status {
	case sqlcgen.InstrumentStatusRetired:
		return false, fmt.Sprintf("%s (%s) is retired and cannot be used for a determination", i.Label, i.SerialNo)
	case sqlcgen.InstrumentStatusSuspended:
		r := i.StatusReason
		if r == "" {
			r = "no reason recorded"
		}
		return false, fmt.Sprintf("%s (%s) is suspended from service: %s", i.Label, i.SerialNo, r)
	}
	if strings.TrimSpace(i.ApprovalReference) == "" {
		return false, fmt.Sprintf(
			"%s (%s) has no CRA approval reference on file — EDM1-1-5 requires each individual instrument to be approved",
			i.Label, i.SerialNo)
	}
	if i.ApprovalExpiresOn.Valid && !dayOf(on).Before(dayOf(i.ApprovalExpiresOn.Time)) {
		return false, fmt.Sprintf("%s (%s) approval expired on %s",
			i.Label, i.SerialNo, i.ApprovalExpiresOn.Time.Format("2006-01-02"))
	}
	return true, ""
}

// calibrationDue returns when the next calibration is due and whether it
// is past due, given the last passed calibration and the instrument's
// interval.
//
// No interval means never overdue: an interval nobody chose is not a
// deadline anybody missed. An interval WITH no calibration on file is
// overdue from the start, which is the honest reading — a check that was
// meant to happen has not.
func calibrationDue(i sqlcgen.Instrument, lastOn pgtype.Date, asOf time.Time) (due pgtype.Date, overdue bool) {
	if !i.CalibrationIntervalDays.Valid || i.CalibrationIntervalDays.Int32 <= 0 {
		return pgtype.Date{}, false
	}
	if !lastOn.Valid {
		return pgtype.Date{}, true
	}
	next := lastOn.Time.AddDate(0, 0, int(i.CalibrationIntervalDays.Int32))
	return pgtype.Date{Valid: true, Time: next}, dayOf(asOf).After(dayOf(next))
}

func (s *InstrumentService) CreateInstrument(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateInstrumentRequest],
) (*connect.Response[stillhousev1.CreateInstrumentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	kind, err := instrumentKindToDB(in.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	label := strings.TrimSpace(in.GetLabel())
	serial := strings.TrimSpace(in.GetSerialNo())
	if label == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("label is required"))
	}
	// The serial is the instrument's identity — it is what CRA approves —
	// so a register entry without one cannot be matched to an approval.
	if serial == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("serial_no is required: CRA approval attaches to the individual instrument, not the model"))
	}
	approvalDate, err := optionalDate(in.GetApprovalDate(), "approval_date")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	expiresOn, err := optionalDate(in.GetApprovalExpiresOn(), "approval_expires_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if in.GetCalibrationIntervalDays() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("calibration_interval_days cannot be negative"))
	}

	var row sqlcgen.Instrument
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.CreateInstrument(ctx, sqlcgen.CreateInstrumentParams{
			TenantID:                u.TenantID,
			Kind:                    kind,
			Label:                   label,
			Manufacturer:            strings.TrimSpace(in.GetManufacturer()),
			Model:                   strings.TrimSpace(in.GetModel()),
			SerialNo:                serial,
			ApprovalReference:       strings.TrimSpace(in.GetApprovalReference()),
			ApprovalDate:            approvalDate,
			ApprovalExpiresOn:       expiresOn,
			CalibrationIntervalDays: optionalInt32(in.GetCalibrationIntervalDays()),
			Notes:                   in.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "instrument", row.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"kind":               string(row.Kind),
				"label":              row.Label,
				"serial_no":          row.SerialNo,
				"approval_reference": row.ApprovalReference,
			})
	})
	if err != nil {
		if ce := classifyWriteErr(err, "instrument not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("CreateInstrument", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateInstrumentResponse{
		Instrument: instrumentToProto(row, pgtype.Date{}, time.Now()),
	}), nil
}

func (s *InstrumentService) UpdateInstrument(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateInstrumentRequest],
) (*connect.Response[stillhousev1.UpdateInstrumentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	label := strings.TrimSpace(in.GetLabel())
	if label == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("label is required"))
	}
	approvalDate, err := optionalDate(in.GetApprovalDate(), "approval_date")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	expiresOn, err := optionalDate(in.GetApprovalExpiresOn(), "approval_expires_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		row  sqlcgen.Instrument
		last pgtype.Date
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		before, e := q.GetInstrument(ctx, id)
		if e != nil {
			return e
		}
		row, e = q.UpdateInstrument(ctx, sqlcgen.UpdateInstrumentParams{
			ID:                      id,
			Label:                   label,
			Manufacturer:            strings.TrimSpace(in.GetManufacturer()),
			Model:                   strings.TrimSpace(in.GetModel()),
			ApprovalReference:       strings.TrimSpace(in.GetApprovalReference()),
			ApprovalDate:            approvalDate,
			ApprovalExpiresOn:       expiresOn,
			CalibrationIntervalDays: optionalInt32(in.GetCalibrationIntervalDays()),
			Notes:                   in.GetNotes(),
		})
		if e != nil {
			return e
		}
		last = latestCalibrationDate(ctx, q, id)
		// The approval reference is the field that decides whether this
		// instrument can back a determination at all, so a change to it is
		// worth its own audit entry rather than a generic "updated".
		return audit.Write(ctx, q, u.TenantID, u.ID, "instrument", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"label":                     row.Label,
				"serial_no":                 row.SerialNo,
				"approval_reference_before": before.ApprovalReference,
				"approval_reference_after":  row.ApprovalReference,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("instrument not found"))
		}
		if ce := classifyWriteErr(err, "instrument not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("UpdateInstrument", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateInstrumentResponse{
		Instrument: instrumentToProto(row, last, time.Now()),
	}), nil
}

func (s *InstrumentService) SetInstrumentStatus(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetInstrumentStatusRequest],
) (*connect.Response[stillhousev1.SetInstrumentStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	status, err := instrumentStatusToDB(req.Msg.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	// Taking an instrument out of service is the entry an auditor reads.
	// Putting one back needs no justification; withdrawing one does.
	if status != sqlcgen.InstrumentStatusActive && reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a reason is required to suspend or retire an instrument"))
	}

	var (
		row  sqlcgen.Instrument
		last pgtype.Date
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.SetInstrumentStatus(ctx, sqlcgen.SetInstrumentStatusParams{
			ID: id, Status: status, StatusReason: reason,
		})
		if e != nil {
			return e
		}
		last = latestCalibrationDate(ctx, q, id)
		return audit.Write(ctx, q, u.TenantID, u.ID, "instrument", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":     "status_changed",
				"status":    string(status),
				"reason":    reason,
				"serial_no": row.SerialNo,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("instrument not found"))
		}
		s.logger.Error("SetInstrumentStatus", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetInstrumentStatusResponse{
		Instrument: instrumentToProto(row, last, time.Now()),
	}), nil
}

func (s *InstrumentService) ListInstruments(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListInstrumentsRequest],
) (*connect.Response[stillhousev1.ListInstrumentsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var kindFilter sqlcgen.NullInstrumentKind
	if req.Msg.GetKind() != stillhousev1.InstrumentKind_INSTRUMENT_KIND_UNSPECIFIED {
		k, err := instrumentKindToDB(req.Msg.GetKind())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		kindFilter = sqlcgen.NullInstrumentKind{InstrumentKind: k, Valid: true}
	}

	var (
		rows []sqlcgen.Instrument
		last map[uuid.UUID]pgtype.Date
	)
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListInstruments(ctx, sqlcgen.ListInstrumentsParams{
			IncludeRetired: req.Msg.GetIncludeRetired(),
			Kind:           kindFilter,
		})
		if e != nil {
			return e
		}
		// One query for the whole register rather than one per instrument.
		cals, e := q.LatestCalibrationsForInstruments(ctx)
		if e != nil {
			return e
		}
		last = make(map[uuid.UUID]pgtype.Date, len(cals))
		for _, c := range cals {
			last[c.InstrumentID] = c.CalibratedOn
		}
		return nil
	})
	if err != nil {
		s.logger.Error("ListInstruments", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	now := time.Now()
	out := make([]*stillhousev1.Instrument, 0, len(rows))
	for _, r := range rows {
		out = append(out, instrumentToProto(r, last[r.ID], now))
	}
	return connect.NewResponse(&stillhousev1.ListInstrumentsResponse{Instruments: out}), nil
}

func (s *InstrumentService) GetInstrument(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetInstrumentRequest],
) (*connect.Response[stillhousev1.GetInstrumentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var (
		row  sqlcgen.Instrument
		cals []sqlcgen.InstrumentCalibration
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if row, e = q.GetInstrument(ctx, id); e != nil {
			return e
		}
		cals, e = q.ListCalibrations(ctx, id)
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("instrument not found"))
		}
		s.logger.Error("GetInstrument", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	var last pgtype.Date
	for _, c := range cals {
		if c.Passed {
			last = c.CalibratedOn
			break // ListCalibrations is newest first
		}
	}
	out := &stillhousev1.GetInstrumentResponse{
		Instrument:   instrumentToProto(row, last, time.Now()),
		Calibrations: make([]*stillhousev1.InstrumentCalibration, 0, len(cals)),
	}
	for _, c := range cals {
		out.Calibrations = append(out.Calibrations, calibrationToProto(c))
	}
	return connect.NewResponse(out), nil
}

func (s *InstrumentService) RecordCalibration(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordCalibrationRequest],
) (*connect.Response[stillhousev1.RecordCalibrationResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetInstrumentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid instrument_id"))
	}
	on, err := parseDateOrToday(req.Msg.GetCalibratedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// A calibration dated in the future would push the next due date out
	// on the strength of a check that has not happened.
	if on.Time.After(dayOf(time.Now())) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("calibrated_on cannot be in the future"))
	}

	var (
		row  sqlcgen.Instrument
		cal  sqlcgen.InstrumentCalibration
		last pgtype.Date
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if row, e = q.GetInstrument(ctx, id); e != nil {
			return e
		}
		cal, e = q.CreateCalibration(ctx, sqlcgen.CreateCalibrationParams{
			TenantID:       u.TenantID,
			InstrumentID:   id,
			CalibratedOn:   on,
			PerformedBy:    strings.TrimSpace(req.Msg.GetPerformedBy()),
			CertificateRef: strings.TrimSpace(req.Msg.GetCertificateRef()),
			Passed:         req.Msg.GetPassed(),
			Notes:          req.Msg.GetNotes(),
			RecordedBy:     uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}
		last = latestCalibrationDate(ctx, q, id)
		return audit.Write(ctx, q, u.TenantID, u.ID, "instrument", id.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"event":           "calibration",
				"serial_no":       row.SerialNo,
				"calibrated_on":   on.Time.Format("2006-01-02"),
				"passed":          cal.Passed,
				"certificate_ref": cal.CertificateRef,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("instrument not found"))
		}
		if ce := classifyWriteErr(err, "instrument not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("RecordCalibration", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RecordCalibrationResponse{
		Instrument:  instrumentToProto(row, last, time.Now()),
		Calibration: calibrationToProto(cal),
	}), nil
}

// latestCalibrationDate is a best-effort read for decorating a response.
// A missing row is not an error — an instrument with no calibration
// history is the ordinary state of a newly registered one.
func latestCalibrationDate(ctx context.Context, q *sqlcgen.Queries, id uuid.UUID) pgtype.Date {
	c, err := q.LatestCalibration(ctx, id)
	if err != nil {
		return pgtype.Date{}
	}
	return c.CalibratedOn
}

// resolvedInstruments is the outcome of checking the instruments named on
// one determination: the ids to store, and any warnings worth telling the
// operator about.
type resolvedInstruments struct {
	volume      uuid.NullUUID
	strength    uuid.NullUUID
	temperature uuid.NullUUID
	// warnings are conditions that do not block the determination but that
	// an operator and an auditor both want to know about — today, only
	// calibration past due.
	warnings []string
}

// checkInstruments validates the instruments named on a determination and
// returns the ids to store against it.
//
// Naming nothing is allowed: the register starts empty, and refusing every
// gauge until it is populated would take the distillery off the air to fix
// a paperwork gap. Naming something that cannot be used is refused —
// recording a determination against a retired or unapproved instrument
// documents a compliance gap in the audit trail rather than closing one.
//
// `on` is the date of the determination, not today: an instrument whose
// approval has since lapsed was still approved when the gauge was taken,
// and a backdated correction must not be judged against today's register.
func checkInstruments(
	ctx context.Context,
	q *sqlcgen.Queries,
	refs *stillhousev1.InstrumentRefs,
	on time.Time,
) (resolvedInstruments, error) {
	var out resolvedInstruments
	if refs == nil {
		return out, nil
	}
	for _, r := range []struct {
		role string
		id   string
		dst  *uuid.NullUUID
	}{
		{"volume", refs.GetVolumeInstrumentId(), &out.volume},
		{"strength", refs.GetStrengthInstrumentId(), &out.strength},
		{"temperature", refs.GetTemperatureInstrumentId(), &out.temperature},
	} {
		if r.id == "" {
			continue
		}
		id, err := uuid.Parse(r.id)
		if err != nil {
			return out, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid %s instrument id", r.role))
		}
		inst, err := q.GetInstrument(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return out, connect.NewError(connect.CodeNotFound,
					fmt.Errorf("%s instrument not found", r.role))
			}
			return out, err
		}
		if usable, reason := instrumentUsability(inst, on); !usable {
			return out, connect.NewError(connect.CodeFailedPrecondition, errors.New(reason))
		}
		if _, overdue := calibrationDue(inst, latestCalibrationDate(ctx, q, id), on); overdue {
			out.warnings = append(out.warnings, fmt.Sprintf(
				"%s (%s) is past due for calibration", inst.Label, inst.SerialNo))
		}
		*r.dst = uuid.NullUUID{UUID: id, Valid: true}
	}
	return out, nil
}

// instrumentCache resolves instrument ids to protos, reading each one at
// most once. A barrel with forty regauges against three instruments is
// three queries, not forty.
type instrumentCache struct {
	q    *sqlcgen.Queries
	asOf time.Time
	seen map[uuid.UUID]*stillhousev1.Instrument
}

func newInstrumentCache(q *sqlcgen.Queries, asOf time.Time) *instrumentCache {
	return &instrumentCache{q: q, asOf: asOf, seen: map[uuid.UUID]*stillhousev1.Instrument{}}
}

func (c *instrumentCache) get(ctx context.Context, id uuid.NullUUID) *stillhousev1.Instrument {
	if !id.Valid {
		return nil
	}
	if hit, ok := c.seen[id.UUID]; ok {
		return hit
	}
	inst, err := c.q.GetInstrument(ctx, id.UUID)
	if err != nil {
		c.seen[id.UUID] = nil
		return nil
	}
	p := instrumentToProto(inst, latestCalibrationDate(ctx, c.q, id.UUID), c.asOf)
	c.seen[id.UUID] = p
	return p
}

// refs builds the display block for one determination, or nil when it
// names no instrument — which is what every row predating the register
// says, and saying nothing is the honest answer there.
func (c *instrumentCache) refs(ctx context.Context, volume, strength, temperature uuid.NullUUID) *stillhousev1.DeterminationInstruments {
	if !volume.Valid && !strength.Valid && !temperature.Valid {
		return nil
	}
	return &stillhousev1.DeterminationInstruments{
		Volume:      c.get(ctx, volume),
		Strength:    c.get(ctx, strength),
		Temperature: c.get(ctx, temperature),
	}
}
