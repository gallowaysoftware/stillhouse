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
			"what went into %s has been drawn on since: void the later movements first (%s)", name, connectMessage(err)))
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

// roundedStrengthABV is how far a typed strength sits from a stored one
// that was displayed to two places.
const roundedStrengthABV = 0.005

// drawnLAA is the alcohol in a draw gauged at abv. A draw that empties the
// vessel at the strength shown for it takes everything: the screen shows
// 60.67 for 60.6667, and refusing that, or stranding the difference, is an
// argument with a rounded figure.
func drawnLAA(c sqlcgen.BulkContainer, volumeL, abv float64) float64 {
	b := balanceOf(c)
	if math.Abs(volumeL-b.volumeL) <= balanceEps && math.Abs(abv-b.abv().Float64) <= roundedStrengthABV+1e-9 {
		return b.laa
	}
	return volumeL * abv / 100
}

// bookStranded writes the line D entry for alcohol the book still showed
// when a draw gauged within tolerance emptied the vessel: the gauge is the
// measurement, so the book was wrong by the difference.
func bookStranded(
	ctx context.Context, q *sqlcgen.Queries, u sqlcgen.User, c sqlcgen.BulkContainer,
	stranded, volumeL, abv float64, at pgtype.Timestamptz, what string,
) error {
	if stranded <= 0 {
		return nil
	}
	why := fmt.Sprintf("%s emptied %s at a gauged %.2f%%; the book had it at %.2f%%",
		what, c.Name, abv, c.CurrentAbvPct.Float64)
	mv, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
		TenantID:          u.TenantID,
		SourceContainerID: uuid.NullUUID{UUID: c.ID, Valid: true},
		VolumeL:           volumeL,
		AbvPct:            math.Min(100, stranded/volumeL*100),
		Laa:               stranded,
		Reason:            sqlcgen.BulkMovementReasonAdjustmentDecrease,
		ReferenceType:     "inventory_adjustment",
		Notes:             why,
		OccurredAt:        at,
	})
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
		CountedLaa:     c.CurrentLaa - stranded,
		DeltaLaa:       -stranded,
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
