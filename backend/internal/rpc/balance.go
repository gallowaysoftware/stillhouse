package rpc

import (
	"context"
	"errors"
	"fmt"
	"math"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// balance is what a container holds. Volume and alcohol are the record;
// the strength is always their quotient and never carried over, because a
// strength kept from before a movement is what the next deposit would
// multiply back into alcohol that is not there.
type balance struct {
	volumeL float64
	laa     float64
}

// balanceEps is float noise, not a tolerance: well under what any gauge
// resolves.
const balanceEps = 1e-6

func balanceOf(c sqlcgen.BulkContainer) balance {
	return balance{volumeL: c.CurrentVolumeL, laa: c.CurrentLaa}
}

func (b balance) abv() pgtype.Float8 {
	if b.volumeL <= balanceEps {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: math.Min(100, b.laa/b.volumeL*100), Valid: true}
}

func (b balance) add(volumeL, laa float64) balance {
	return balance{volumeL: b.volumeL + volumeL, laa: b.laa + laa}
}

// take removes volume and alcohol from what the named vessel holds.
// stranded is alcohol the book still had when the litres ran out; the
// caller writes a ledger row for it or refuses, and never drops it.
func (b balance) take(name string, volumeL, laa float64) (rest balance, stranded float64, err error) {
	if volumeL > b.volumeL+balanceEps {
		return b, 0, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s holds %.4f L; this takes %.4f", name, b.volumeL, volumeL))
	}
	if laa > b.laa+balanceEps {
		return b, 0, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s holds %.4f LAA; this takes %.4f", name, b.laa, laa))
	}
	rest = balance{volumeL: b.volumeL - volumeL, laa: math.Max(0, b.laa-laa)}
	if rest.volumeL <= balanceEps {
		if rest.laa > balanceEps {
			stranded = rest.laa
		}
		return balance{}, stranded, nil
	}
	if rest.laa > rest.volumeL+balanceEps {
		return b, 0, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"that would leave %s holding %.4f LAA in %.4f L, stronger than pure alcohol", name, rest.laa, rest.volumeL))
	}
	return rest, 0, nil
}

// takeBack undoes a deposit. Alcohol left over with no litres means
// something downstream already used the rest.
func (b balance) takeBack(name string, volumeL, laa float64) (balance, error) {
	rest, stranded, err := b.take(name, volumeL, laa)
	if err == nil && stranded > 0 {
		err = connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s would be left with %.4f LAA and no litres", name, stranded))
	}
	if err != nil {
		return b, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s has been drawn on or regauged since, so this cannot be taken back out (%s). "+
				"Void what came after it, or leave it and correct %s with a physical count",
			name, connectMessage(err), name))
	}
	return rest, nil
}

func connectMessage(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Message()
	}
	return err.Error()
}

// saveBalance is the only writer of a container's balance.
func saveBalance(ctx context.Context, q *sqlcgen.Queries, id uuid.UUID, b balance) (sqlcgen.BulkContainer, error) {
	return q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
		ID: id, CurrentVolumeL: b.volumeL, CurrentAbvPct: b.abv(), CurrentLaa: b.laa,
	})
}

// The screen shows a volume to 0.01 L and a strength to 0.01 %, and a form
// is filled in with them. A figure within half of that is the stored one.
const (
	shownVolumeL   = 0.005
	shownStrength  = 0.005
	shownTolerance = 1e-9
)

// draw is what a gauged withdrawal takes, and what it leaves.
type draw struct {
	volumeL float64
	laa     float64
	rest    balance
	// correction is what the book was wrong by, where the gauge shows it was:
	// positive is alcohol it had not known about. It goes on line D.
	correction float64
}

// drawFrom is a withdrawal gauged at abv. Within tolerance the gauge is the
// measurement and the book is arithmetic, so the gauge wins: the difference
// stays in the vessel, where the next draw averages it out. When too little
// is left to absorb it, a litre of heel would be booked at 160 % or at
// nothing, so the heel is taken to be what was just gauged, and the book's
// error is written down.
func drawFrom(c sqlcgen.BulkContainer, volumeL, abv float64) (draw, error) {
	have := balanceOf(c)
	book := have.abv()
	if !book.Valid {
		return draw{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s is empty", c.Name))
	}
	if err := assertFillStrengthMatchesSource(c, abv); err != nil {
		return draw{}, err
	}
	if volumeL > have.volumeL+shownVolumeL {
		return draw{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s holds %.4f L; this takes %.4f", c.Name, have.volumeL, volumeL))
	}
	d := draw{volumeL: volumeL, laa: volumeL * abv / 100}
	if math.Abs(volumeL-have.volumeL) <= shownVolumeL+shownTolerance {
		d.volumeL = have.volumeL
		d.laa = have.volumeL * abv / 100
	}
	if math.Abs(abv-book.Float64) <= shownStrength+shownTolerance {
		d.laa = have.laa * d.volumeL / have.volumeL
	}
	d.rest = balance{volumeL: have.volumeL - d.volumeL, laa: have.laa - d.laa}
	if d.rest.volumeL <= balanceEps {
		d.correction = -d.rest.laa
		d.rest = balance{}
	} else if left := d.rest.laa / d.rest.volumeL * 100; math.Abs(left-book.Float64) > fillStrengthToleranceABV {
		want := d.rest.volumeL * abv / 100
		d.correction = want - d.rest.laa
		d.rest.laa = want
	}
	if math.Abs(d.correction) <= balanceEps {
		d.correction = 0
	}
	return d, nil
}

// gaugedLAA is the alcohol in a vessel gauged in place. Alcohol cannot have
// appeared, but two gauges within tolerance can read that way; gain is how
// much, and goes on line D.
func gaugedLAA(c sqlcgen.BulkContainer, volumeL, abv float64) (laa, gain float64, err error) {
	have := balanceOf(c)
	laa = volumeL * abv / 100
	if math.Abs(volumeL-have.volumeL) <= shownVolumeL+shownTolerance &&
		math.Abs(abv-have.abv().Float64) <= shownStrength+shownTolerance {
		return have.laa, 0, nil
	}
	if gain = laa - have.laa; gain <= balanceEps {
		return laa, 0, nil
	}
	if gain > have.volumeL*fillStrengthToleranceABV/100 {
		return 0, 0, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%s is booked at %.4f LAA and this gauge finds %.4f, more than two gauges differ by; "+
				"check the reading, or record a physical count if the book is wrong", c.Name, have.laa, laa))
	}
	return laa, gain, nil
}

// bookCorrection writes the line D entry for what a gauge showed the book
// had wrong. cause is the event it goes with, if one can be voided.
func bookCorrection(
	ctx context.Context, q *sqlcgen.Queries, u sqlcgen.User, c sqlcgen.BulkContainer,
	laa, abv float64, at pgtype.Timestamptz, cause uuid.NullUUID, what string,
) error {
	if laa == 0 {
		return nil
	}
	why := fmt.Sprintf("%s gauged %s at %.2f%%; the book had it at %.2f%%", what, c.Name, abv, c.CurrentAbvPct.Float64)
	params := sqlcgen.InsertBulkMovementParams{
		TenantID:               u.TenantID,
		DestinationContainerID: uuid.NullUUID{UUID: c.ID, Valid: true},
		VolumeL:                c.CurrentVolumeL,
		AbvPct:                 math.Min(100, math.Abs(laa)/c.CurrentVolumeL*100),
		Laa:                    math.Abs(laa),
		Reason:                 sqlcgen.BulkMovementReasonAdjustmentIncrease,
		ReferenceType:          "inventory_adjustment",
		ReferenceID:            cause,
		Notes:                  why,
		OccurredAt:             at,
	}
	if laa < 0 {
		params.SourceContainerID, params.DestinationContainerID = params.DestinationContainerID, uuid.NullUUID{}
		params.Reason = sqlcgen.BulkMovementReasonAdjustmentDecrease
	}
	mv, err := q.InsertBulkMovement(ctx, params)
	if err != nil {
		return err
	}
	_, err = q.CreateInventoryAdjustment(ctx, sqlcgen.CreateInventoryAdjustmentParams{
		TenantID:       u.TenantID,
		ContainerID:    c.ID,
		BulkMovementID: uuid.NullUUID{UUID: mv.ID, Valid: true},
		Reason:         sqlcgen.InventoryAdjustmentReasonMeasurementCorrection,
		Explanation:    why,
		BookVolumeL:    c.CurrentVolumeL,
		BookAbvPct:     c.CurrentAbvPct,
		BookLaa:        c.CurrentLaa,
		CountedVolumeL: c.CurrentVolumeL,
		CountedAbvPct:  pgtype.Float8{Float64: abv, Valid: true},
		CountedLaa:     c.CurrentLaa + laa,
		DeltaLaa:       laa,
		VolumeFactorC:  1,
		StrengthSource: sqlcgen.StrengthSourceUncorrected,
		AdjustedBy:     u.ID,
		OccurredAt:     at,
	})
	return err
}

// assertTakesSpirit refuses a deposit that cannot physically happen.
func assertTakesSpirit(c sqlcgen.BulkContainer, volumeL float64) error {
	if c.Archived {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s is archived", c.Name))
	}
	if c.CapacityL.Valid && c.CurrentVolumeL+volumeL > c.CapacityL.Float64+balanceEps {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"%.4f L would overfill %s (%.4f L on hand, %.4f L capacity)",
			volumeL, c.Name, c.CurrentVolumeL, c.CapacityL.Float64))
	}
	return nil
}
