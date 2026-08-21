package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The duty point is the event at which excise duty becomes payable, and
// until stage 143 Stillhouse only modelled one of the two.
//
// `excise.Owed` was called in exactly one place — a removal — and the B266
// derived its whole duty figure from removal totals. That is the
// excise-warehouse pattern: hold packaged spirits non-duty-paid, pay when
// they leave for the duty-paid market. A spirits licensee WITHOUT an
// excise warehouse licence cannot possess non-duty-paid packaged spirits
// at all (EDM3-1-1 ¶18), and duty becomes payable at the time the spirits
// are packaged (¶29). For that licensee Stillhouse reported duty in the
// month of sale when CRA expects it in the month of bottling — a timing
// error on a filed return, carrying interest.
//
// The duty point is derived from whether the tenant holds a warehouse
// licence, in the database, as a generated column. It is not a toggle an
// operator can get wrong.

// dutyBasis is the tenant's duty point together with the date it started
// governing.
type dutyBasis struct {
	point sqlcgen.DutyPoint
	// from is the cutover. Duty events before it crystallised at removal,
	// because that is what Stillhouse did and what has already been filed.
	from time.Time
}

// dutiesAtPackaging reports whether a bottling run on the given date is a
// duty event.
//
// Both conditions matter. The tenant must pay at packaging, and the run
// must fall on or after the cutover — a run before it belongs to the era
// when duty crystallised at removal, and its stock is dutied on the way
// out. Without the second condition a tenant switching basis would see
// stock bottled under the old basis dutied at packaging retroactively,
// against periods that may already be filed.
func (b dutyBasis) dutiesAtPackaging(on time.Time) bool {
	if b.point != sqlcgen.DutyPointAtPackaging {
		return false
	}
	return !dayOf(on).Before(dayOf(b.from))
}

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// tenantDutyBasis reads the basis inside an open tenant transaction.
func tenantDutyBasis(ctx context.Context, q *sqlcgen.Queries, tenantID uuid.UUID) (dutyBasis, error) {
	t, err := q.GetTenantByID(ctx, tenantID)
	if err != nil {
		return dutyBasis{}, err
	}
	return dutyBasis{point: t.DutyPoint, from: t.DutyPointEffectiveFrom.Time}, nil
}

// packagingDuty computes the duty crystallised by packaging `bottles` of a
// product on a date.
//
// Charged on the sealed bottles, not on what was drawn from the tank: the
// packaging loss never became packaged spirits, and its treatment is a
// separate question (PLAN A5). Above 7% ABV the charge is per litre of
// absolute alcohol; at or below, per litre of product — the same two units
// a removal uses, so the two sides of a cutover are directly comparable.
func packagingDuty(on time.Time, bottles int32, bottleSizeML int32, abvPct float64) (
	ratePerLAA, amountCAD float64, source string, err error) {
	litres := float64(bottles) * float64(bottleSizeML) / 1000
	band, err := excise.RateOn(on)
	if err != nil {
		return 0, 0, "", err
	}
	ratePerLAA, amountCAD, err = excise.Owed(on, litres, abvPct)
	if err != nil {
		return 0, 0, "", err
	}
	return ratePerLAA, amountCAD, band.Source, nil
}

// asRateRefusal turns an unknown-rate error into a message an operator can
// act on. Everything else is passed through for the caller to classify.
func asRateRefusal(err error) error {
	var unknown *excise.UnknownRateError
	if errors.As(err, &unknown) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return err
}

func dutyPointToProto(p sqlcgen.DutyPoint) stillhousev1.DutyPoint {
	switch p {
	case sqlcgen.DutyPointAtPackaging:
		return stillhousev1.DutyPoint_DUTY_POINT_AT_PACKAGING
	case sqlcgen.DutyPointAtRemoval:
		return stillhousev1.DutyPoint_DUTY_POINT_AT_REMOVAL
	}
	return stillhousev1.DutyPoint_DUTY_POINT_UNSPECIFIED
}
