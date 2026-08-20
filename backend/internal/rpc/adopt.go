package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alcoholometry"
	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// AdoptOpeningInventory books stock that was already in the warehouse when
// the distillery started using Stillhouse.
//
// This is the day-one path. A working distillery adopting this system has
// casks with no mash, no fermentation and no distillation run behind them —
// that history lives in whatever they kept records in before, and there is
// no honest way to reconstruct it. What they do have is a scale reading and
// a hydrometer, which is exactly what CRA's Mass/Density Procedure takes.
//
// The alcohol enters the ledger with no source container, the same shape as
// a production gauge, but under its own reason so the B266 never mistakes
// it for something the distillery made. See migration 000025.
func (s *BulkService) AdoptOpeningInventory(
	ctx context.Context,
	req *connect.Request[stillhousev1.AdoptOpeningInventoryRequest],
) (*connect.Response[stillhousev1.AdoptOpeningInventoryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	containerID, err := uuid.Parse(in.GetContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid container_id"))
	}

	measured, err := resolveAdoptedStock(in)
	if err != nil {
		return nil, alcoholometryError(err)
	}

	var fillDate pgtype.Date
	if d := in.GetFillDate(); d != "" {
		t, perr := time.Parse("2006-01-02", d)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("fill_date must be an ISO date (YYYY-MM-DD)"))
		}
		if t.After(time.Now()) {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("fill_date cannot be in the future"))
		}
		fillDate = pgtype.Date{Valid: true, Time: t}
	}
	occurredAt := timestampOrNow(in.GetAsOf())

	var (
		container sqlcgen.BulkContainer
		movement  sqlcgen.BulkMovement
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: occurredAt.Time}); e != nil {
			return e
		}
		lockedAdopt, e := lockContainers(ctx, q, containerID)
		if e != nil {
			return e
		}
		existing := lockedAdopt[containerID]
		// Same overflow guard FillBarrel has. This is the day-one path, so
		// a decimal point in the wrong place (1524 kg for 152.4) lands in
		// opening inventory and therefore on the first return — 1658 L
		// booked into a 200 L cask, and nothing said a word.
		if existing.CapacityL.Valid && measured.VolumeL20C > existing.CapacityL.Float64+1e-6 {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%.4f L would not fit in %s (%.4f L capacity) — check the measurement",
				measured.VolumeL20C, existing.Name, existing.CapacityL.Float64))
		}
		// Adoption establishes a balance; it does not top one up. A vessel
		// that already holds alcohol has a history in the ledger, and
		// adding "opening" stock on top of it would double-count.
		if existing.CurrentVolumeL > 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("%s already holds %.4f L — adopt only into an empty vessel, "+
					"and use a regauge to correct a balance that is already recorded",
					existing.Name, existing.CurrentVolumeL))
		}

		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{Valid: false},
			DestinationContainerID: uuid.NullUUID{UUID: containerID, Valid: true},
			VolumeL:                measured.VolumeL20C,
			AbvPct:                 measured.StrengthPct20C,
			Laa:                    measured.LAA(),
			Reason:                 sqlcgen.BulkMovementReasonOpeningInventory,
			ReferenceType:          "opening_inventory",
			Notes:                  in.GetNotes(),
			OccurredAt:             occurredAt,
		})
		if e != nil {
			return e
		}
		movement = mv

		container, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             containerID,
			CurrentVolumeL: measured.VolumeL20C,
			CurrentAbvPct:  pgtype.Float8{Float64: measured.StrengthPct20C, Valid: true},
			CurrentLaa:     measured.LAA(),
		})
		if e != nil {
			return e
		}

		// A barrel keeps the age it actually has. Without this an adopted
		// three-year-old cask restarts at zero and silently loses its
		// Canadian Whisky eligibility.
		if existing.Kind == sqlcgen.BulkContainerKindBarrel && fillDate.Valid {
			if e := q.SetBarrelFillDate(ctx, sqlcgen.SetBarrelFillDateParams{
				ContainerID: containerID,
				FillDate:    fillDate,
			}); e != nil {
				return e
			}
			if _, e := q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
				TenantID:            u.TenantID,
				ContainerID:         containerID,
				Kind:                sqlcgen.BarrelEventKindFill,
				EventDate:           pgtype.Timestamptz{Valid: true, Time: fillDate.Time},
				VolumeL:             pgtype.Float8{Float64: measured.VolumeL20C, Valid: true},
				AbvPct:              pgtype.Float8{Float64: measured.StrengthPct20C, Valid: true},
				Laa:                 pgtype.Float8{Float64: measured.LAA(), Valid: true},
				BulkMovementID:      uuid.NullUUID{UUID: mv.ID, Valid: true},
				Notes:               "adopted opening inventory",
				UserID:              uuid.NullUUID{UUID: u.ID, Valid: true},
				TemperatureC:        optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
				ObservedVolumeL:     optionalFloat(in.GetVolumeLSet(), in.GetVolumeL()),
				ObservedDensityKgM3: optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
				VolumeFactorC:       measured.VolumeFactorC,
				StrengthSource:      strengthSourceToDB(measured.Source),
			}); e != nil {
				return e
			}
		}

		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", containerID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"event":           "opening_inventory",
				"container":       container.Name,
				"volume_l_20c":    measured.VolumeL20C,
				"abv_pct_20c":     measured.StrengthPct20C,
				"laa":             measured.LAA(),
				"mass_kg":         in.GetMassKg(),
				"strength_source": measured.Source.String(),
				"fill_date":       in.GetFillDate(),
			})
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("container not found"))
		}
		s.logger.Error("AdoptOpeningInventory", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	return connect.NewResponse(&stillhousev1.AdoptOpeningInventoryResponse{
		Container:       bulkContainerToProto(container),
		Movement:        bulkMovementToProto(movement),
		VolumeL_20C:     round4(measured.VolumeL20C),
		StrengthPct_20C: round2(measured.StrengthPct20C),
		Laa:             round4(measured.LAA()),
	}), nil
}

// resolveAdoptedStock turns whatever the operator could measure into litres
// and strength at 20 °C.
//
// A weighed charge takes CRA's Mass/Density Procedure directly: column A
// converts kilograms to litres at 20 °C, and column B gives the strength.
// No volume correction is involved at all, because a scale reading doesn't
// expand with temperature.
func resolveAdoptedStock(in *stillhousev1.AdoptOpeningInventoryRequest) (resolvedStrength, error) {
	// A supplied-but-impossible figure is a different mistake from a
	// missing one, and saying "supply either mass_kg or volume_l" to
	// someone who supplied mass_kg sends them to the wrong field.
	if in.GetMassKgSet() && in.GetMassKg() <= 0 {
		return resolvedStrength{}, invalidInput(fmt.Errorf(
			"mass_kg must be > 0, got %g", in.GetMassKg()))
	}
	if in.GetVolumeLSet() && in.GetVolumeL() <= 0 {
		return resolvedStrength{}, invalidInput(fmt.Errorf(
			"volume_l must be > 0, got %g", in.GetVolumeL()))
	}
	hasMass := in.GetMassKgSet()
	hasVolume := in.GetVolumeLSet()
	if !hasMass && !hasVolume {
		return resolvedStrength{}, invalidInput(errors.New("supply either mass_kg or volume_l"))
	}
	if hasMass && hasVolume {
		return resolvedStrength{}, invalidInput(errors.New("supply mass_kg or volume_l, not both"))
	}
	// Only meaningful on the volume route — a weighed charge takes its
	// strength from the tables — but checking it here keeps the database's
	// check constraint from being the thing that reports it, as a 500.
	if !in.GetDensityKgM3Set() {
		if err := validateAbvPct("abv_pct", in.GetAbvPct()); err != nil {
			return resolvedStrength{}, invalidInput(err)
		}
	}

	if hasMass {
		if !in.GetDensityKgM3Set() {
			return resolvedStrength{}, invalidInput(errors.New(
				"a weighed charge needs the hydrometer indication (density_kg_m3) to give a strength"))
		}
		if !in.GetTemperatureCSet() {
			return resolvedStrength{}, errMissingTemperature
		}
		r, err := alcoholometry.Lookup(in.GetTemperatureC(), in.GetDensityKgM3())
		if err != nil {
			return resolvedStrength{}, err
		}
		return resolvedStrength{
			StrengthPct20C: r.StrengthPct,
			VolumeL20C:     in.GetMassKg() * r.LitresPerKg,
			// The mass route never applies a volume factor: A already
			// lands on litres at 20 °C.
			VolumeFactorC: 1,
			Source:        stillhousev1.StrengthSource_STRENGTH_SOURCE_TABLE_DENSITY,
		}, nil
	}

	return resolveStrength(strengthInput{
		ObservedVolumeL: in.GetVolumeL(),
		AbvPct:          in.GetAbvPct(),
		DensityKgM3:     in.GetDensityKgM3(),
		DensityIsSet:    in.GetDensityKgM3Set(),
		TemperatureC:    in.GetTemperatureC(),
		TemperatureSet:  in.GetTemperatureCSet(),
	})
}
