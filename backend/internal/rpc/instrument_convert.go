package rpc

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// instrumentToProto decorates the stored row with the two derived facts an
// operator needs at the bench: whether this instrument may back a
// determination right now, and whether its calibration is past due.
func instrumentToProto(i sqlcgen.Instrument, lastCalibrated pgtype.Date, asOf time.Time) *stillhousev1.Instrument {
	usable, reason := instrumentUsability(i, asOf)
	due, overdue := calibrationDue(i, lastCalibrated, asOf)

	out := &stillhousev1.Instrument{
		Id:                 i.ID.String(),
		Kind:               instrumentKindToProto(i.Kind),
		Label:              i.Label,
		Manufacturer:       i.Manufacturer,
		Model:              i.Model,
		SerialNo:           i.SerialNo,
		ApprovalReference:  i.ApprovalReference,
		ApprovalDate:       formatDate(i.ApprovalDate),
		ApprovalExpiresOn:  formatDate(i.ApprovalExpiresOn),
		Status:             instrumentStatusToProto(i.Status),
		StatusReason:       i.StatusReason,
		Notes:              i.Notes,
		CreatedAt:          timestamppb.New(i.CreatedAt.Time),
		UpdatedAt:          timestamppb.New(i.UpdatedAt.Time),
		Usable:             usable,
		UnusableReason:     reason,
		LastCalibratedOn:   formatDate(lastCalibrated),
		CalibrationDueOn:   formatDate(due),
		CalibrationOverdue: overdue,
	}
	if i.CalibrationIntervalDays.Valid {
		out.CalibrationIntervalDays = i.CalibrationIntervalDays.Int32
	}
	return out
}

func calibrationToProto(c sqlcgen.InstrumentCalibration) *stillhousev1.InstrumentCalibration {
	out := &stillhousev1.InstrumentCalibration{
		Id:             c.ID.String(),
		InstrumentId:   c.InstrumentID.String(),
		CalibratedOn:   formatDate(c.CalibratedOn),
		PerformedBy:    c.PerformedBy,
		CertificateRef: c.CertificateRef,
		Passed:         c.Passed,
		Notes:          c.Notes,
		CreatedAt:      timestamppb.New(c.CreatedAt.Time),
	}
	if c.RecordedBy.Valid {
		out.RecordedBy = c.RecordedBy.UUID.String()
	}
	return out
}

func instrumentKindToDB(k stillhousev1.InstrumentKind) (sqlcgen.InstrumentKind, error) {
	switch k {
	case stillhousev1.InstrumentKind_INSTRUMENT_KIND_THERMOMETER:
		return sqlcgen.InstrumentKindThermometer, nil
	case stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER:
		return sqlcgen.InstrumentKindHydrometer, nil
	case stillhousev1.InstrumentKind_INSTRUMENT_KIND_DENSITY_METER:
		return sqlcgen.InstrumentKindDensityMeter, nil
	case stillhousev1.InstrumentKind_INSTRUMENT_KIND_MASS_FLOW_METER:
		return sqlcgen.InstrumentKindMassFlowMeter, nil
	case stillhousev1.InstrumentKind_INSTRUMENT_KIND_SCALE:
		return sqlcgen.InstrumentKindScale, nil
	case stillhousev1.InstrumentKind_INSTRUMENT_KIND_VOLUMETRIC_MEASURE:
		return sqlcgen.InstrumentKindVolumetricMeasure, nil
	case stillhousev1.InstrumentKind_INSTRUMENT_KIND_OTHER:
		return sqlcgen.InstrumentKindOther, nil
	}
	return "", errors.New("kind is required")
}

func instrumentKindToProto(k sqlcgen.InstrumentKind) stillhousev1.InstrumentKind {
	switch k {
	case sqlcgen.InstrumentKindThermometer:
		return stillhousev1.InstrumentKind_INSTRUMENT_KIND_THERMOMETER
	case sqlcgen.InstrumentKindHydrometer:
		return stillhousev1.InstrumentKind_INSTRUMENT_KIND_HYDROMETER
	case sqlcgen.InstrumentKindDensityMeter:
		return stillhousev1.InstrumentKind_INSTRUMENT_KIND_DENSITY_METER
	case sqlcgen.InstrumentKindMassFlowMeter:
		return stillhousev1.InstrumentKind_INSTRUMENT_KIND_MASS_FLOW_METER
	case sqlcgen.InstrumentKindScale:
		return stillhousev1.InstrumentKind_INSTRUMENT_KIND_SCALE
	case sqlcgen.InstrumentKindVolumetricMeasure:
		return stillhousev1.InstrumentKind_INSTRUMENT_KIND_VOLUMETRIC_MEASURE
	case sqlcgen.InstrumentKindOther:
		return stillhousev1.InstrumentKind_INSTRUMENT_KIND_OTHER
	}
	return stillhousev1.InstrumentKind_INSTRUMENT_KIND_UNSPECIFIED
}

func instrumentStatusToDB(s stillhousev1.InstrumentStatus) (sqlcgen.InstrumentStatus, error) {
	switch s {
	case stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE:
		return sqlcgen.InstrumentStatusActive, nil
	case stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_SUSPENDED:
		return sqlcgen.InstrumentStatusSuspended, nil
	case stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_RETIRED:
		return sqlcgen.InstrumentStatusRetired, nil
	}
	return "", errors.New("status is required")
}

func instrumentStatusToProto(s sqlcgen.InstrumentStatus) stillhousev1.InstrumentStatus {
	switch s {
	case sqlcgen.InstrumentStatusActive:
		return stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE
	case sqlcgen.InstrumentStatusSuspended:
		return stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_SUSPENDED
	case sqlcgen.InstrumentStatusRetired:
		return stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_RETIRED
	}
	return stillhousev1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED
}

// optionalDate parses an ISO date that may be absent. An empty string is
// "not set"; anything else has to parse, because a date silently dropped
// because it was mistyped is worse than a rejected form.
func optionalDate(s, field string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}, fmt.Errorf("%s must be YYYY-MM-DD", field)
	}
	return pgtype.Date{Valid: true, Time: t}, nil
}

// optionalInt32 treats zero as "unset", which is what a proto scalar sends
// when the field is absent. Zero is not a meaningful calibration interval
// anyway — an instrument due for recalibration every zero days is not a
// policy anyone holds.
func optionalInt32(v int32) pgtype.Int4 {
	if v <= 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: v, Valid: true}
}
