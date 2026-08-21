package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// Canadian Whisky maturation thresholds (FDR B.02.020):
//   - Aged ≥3 years (1095 days) in small wood (≤700 L) in Canada.
const (
	canadianWhiskyAgingDays = 1095
	smallWoodCapacityL      = 700
)

type BarrelService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewBarrelService(db *tenantdb.DB, logger *slog.Logger) *BarrelService {
	return &BarrelService{db: db, logger: logger}
}

func (s *BarrelService) CreateBarrel(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateBarrelRequest],
) (*connect.Response[stillhousev1.CreateBarrelResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	// A zero capacity is worse than none: it passes the "is a capacity
	// recorded" test and then refuses every fill into the vessel.
	if in.GetCapacityLSet() {
		if err := validateCapacityL(in.GetCapacityL()); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}

	var (
		container sqlcgen.BulkContainer
		attrs     sqlcgen.BarrelAttribute
	)
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		container, e = q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
			TenantID:  u.TenantID,
			Name:      in.GetName(),
			Kind:      sqlcgen.BulkContainerKindBarrel,
			CapacityL: optionalFloat(in.GetCapacityLSet(), in.GetCapacityL()),
			Location:  in.GetLocation(),
			Notes:     in.GetNotes(),
		})
		if e != nil {
			return e
		}
		var charLevel pgtype.Int4
		if in.GetCharLevelSet() {
			charLevel = pgtype.Int4{Int32: in.GetCharLevel(), Valid: true}
		}
		attrs, e = q.CreateBarrelAttributes(ctx, sqlcgen.CreateBarrelAttributesParams{
			ContainerID:       container.ID,
			TenantID:          u.TenantID,
			CooperageSupplier: in.GetCooperageSupplier(),
			CharLevel:         charLevel,
			WoodSpecies:       in.GetWoodSpecies(),
			PriorUse:          in.GetPriorUse(),
			SerialBurnin:      in.GetSerialBurnin(),
			Rickhouse:         in.GetRickhouse(),
			RowPosition:       in.GetRowPosition(),
			LevelPosition:     in.GetLevelPosition(),
			ColumnPosition:    in.GetColumnPosition(),
		})
		return e
	})
	if err != nil {
		s.logger.Error("CreateBarrel", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateBarrelResponse{
		Barrel: barrelToProto(container, attrs),
	}), nil
}

func (s *BarrelService) ListBarrels(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListBarrelsRequest],
) (*connect.Response[stillhousev1.ListBarrelsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListBarrelsRow
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListBarrels(ctx, req.Msg.GetIncludeArchived())
		return e
	})
	if err != nil {
		s.logger.Error("ListBarrels", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.ListBarrelsResponse{
		Barrels: make([]*stillhousev1.Barrel, 0, len(rows)),
	}
	for _, r := range rows {
		b := barrelRowToProto(r)
		out.Barrels = append(out.Barrels, b)
		out.TotalCount++
		out.TotalLaa += b.CurrentLaa
		if b.DaysAged > 0 {
			out.AgingCount++
		}
		if b.CanadianWhiskyEligible {
			out.EligibleCount++
		}
	}
	return connect.NewResponse(out), nil
}

func (s *BarrelService) GetBarrel(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetBarrelRequest],
) (*connect.Response[stillhousev1.GetBarrelResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var (
		container sqlcgen.BulkContainer
		attrs     sqlcgen.BarrelAttribute
		events    []sqlcgen.BarrelEvent
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		container, e = q.GetBulkContainer(ctx, id)
		if e != nil {
			return e
		}
		if container.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("container is not a barrel"))
		}
		attrs, e = q.GetBarrelAttributes(ctx, id)
		if e != nil {
			return e
		}
		events, e = q.ListBarrelEvents(ctx, id)
		return e
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("barrel not found"))
		}
		s.logger.Error("GetBarrel", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	barrel := barrelToProto(container, attrs)
	// The events are already loaded, so the fill this cask is living off
	// comes free — no need for the lateral join ListBarrels uses.
	if fillVol, fillAbv, fillLAA, ok := latestFill(events); ok {
		barrel.Maturation = buildMaturation(
			fillVol, fillAbv, fillLAA,
			container.CurrentVolumeL, barrel.CurrentAbvPct, container.CurrentLaa,
			barrel.DaysAged, attrs.LevelPosition,
		)
	}
	out := &stillhousev1.GetBarrelResponse{
		Barrel:     barrel,
		Events:     make([]*stillhousev1.BarrelEvent, 0, len(events)),
		Maturation: barrel.Maturation,
	}
	for _, e := range events {
		out.Events = append(out.Events, barrelEventToProto(e))
	}
	return connect.NewResponse(out), nil
}

func (s *BarrelService) FillBarrel(
	ctx context.Context,
	req *connect.Request[stillhousev1.FillBarrelRequest],
) (*connect.Response[stillhousev1.FillBarrelResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	barrelID, err := uuid.Parse(in.GetBarrelId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid barrel_id"))
	}
	sourceID, err := uuid.Parse(in.GetSourceContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid source_container_id"))
	}
	if in.GetVolumeL() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("volume_l must be > 0"))
	}
	if in.GetAbvPct() < 0 || in.GetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("abv_pct must be in [0, 100]"))
	}

	// Resolve the reading to 20 °C before anything is written. A warehouse
	// is the least likely place on site for a gauge to be taken at the
	// reference temperature, so the correction matters most here.
	corrected, err := resolveStrength(strengthInput{
		ObservedVolumeL: in.GetVolumeL(),
		AbvPct:          in.GetAbvPct(),
		DensityKgM3:     in.GetDensityKgM3(),
		DensityIsSet:    in.GetDensityKgM3Set(),
		TemperatureC:    in.GetTemperatureC(),
		TemperatureSet:  in.GetTemperatureCSet(),
	})
	if err != nil {
		return nil, alcoholometryError(err)
	}
	// Everything below works in litres and strength at 20 °C.
	volume, abv := corrected.VolumeL20C, corrected.StrengthPct20C

	eventTime := timestampOrNow(in.GetEventDate())

	var (
		barrelContainer sqlcgen.BulkContainer
		attrs           sqlcgen.BarrelAttribute
		event           sqlcgen.BarrelEvent
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: eventTime.Time}); e != nil {
			return e
		}
		locked, e := lockContainers(ctx, q, barrelID, sourceID)
		if e != nil {
			return e
		}
		barrel := locked[barrelID]
		if barrel.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("target is not a barrel"))
		}
		// Reject overfills. Recorded capacity is in litres; the small
		// tolerance covers float rounding when filling to exactly the
		// brim. If no capacity is recorded on the barrel we skip the
		// check — operator opted out of capacity hygiene for that vessel.
		if barrel.CapacityL.Valid {
			if barrel.CurrentVolumeL+volume > barrel.CapacityL.Float64+1e-6 {
				return connect.NewError(connect.CodeFailedPrecondition,
					fmt.Errorf("fill would overflow barrel: %.4f L on hand + %.4f L fill > %.4f L capacity",
						barrel.CurrentVolumeL, volume, barrel.CapacityL.Float64))
			}
		}
		source := locked[sourceID]
		if source.CurrentVolumeL < volume {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("source container has insufficient volume"))
		}
		if e := assertFillStrengthMatchesSource(source, abv); e != nil {
			return e
		}

		laa := volume * abv / 100
		if source.CurrentLaa+1e-6 < laa {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("source holds %.4f LAA; this fill would remove %.4f", source.CurrentLaa, laa))
		}

		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{UUID: sourceID, Valid: true},
			DestinationContainerID: uuid.NullUUID{UUID: barrelID, Valid: true},
			VolumeL:                volume,
			AbvPct:                 abv,
			Laa:                    laa,
			Reason:                 sqlcgen.BulkMovementReasonInterTankTransfer,
			ReferenceType:          "barrel_fill",
			ReferenceID:            uuid.NullUUID{UUID: barrelID, Valid: true},
			Notes:                  in.GetNotes(),
			OccurredAt:             eventTime,
		})
		if e != nil {
			return e
		}

		// Withdraw exactly what the barrel receives. Debiting the source
		// at its own recorded strength while crediting the barrel at the
		// gauged one is how alcohol gets created: 100 L "at 80%" out of a
		// 60% tank used to put 80 LAA in the cask and take 60 out. What is
		// left over is arithmetic — the remaining alcohol over the
		// remaining volume — not a second measurement.
		newSrcVol, newSrcAbv, newSrcLAA := applyWithdrawal(
			source.CurrentVolumeL, source.CurrentLaa, volume, laa)
		if _, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             sourceID,
			CurrentVolumeL: newSrcVol,
			CurrentAbvPct:  newSrcAbv,
			CurrentLaa:     newSrcLAA,
		}); e != nil {
			return e
		}

		// Update barrel (deposit at fill ABV).
		newBarrelVol, newBarrelAbv, newBarrelLAA := applyDeposit(
			barrel.CurrentVolumeL, barrel.CurrentAbvPct,
			volume, abv,
		)
		barrelContainer, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             barrelID,
			CurrentVolumeL: newBarrelVol,
			CurrentAbvPct:  newBarrelAbv,
			CurrentLaa:     newBarrelLAA,
		})
		if e != nil {
			return e
		}

		event, e = q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
			TemperatureC:        optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL:     optionalFloat(true, in.GetVolumeL()),
			ObservedDensityKgM3: optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:       corrected.VolumeFactorC,
			StrengthSource:      strengthSourceToDB(corrected.Source),
			TenantID:            u.TenantID,
			ContainerID:         barrelID,
			Kind:                sqlcgen.BarrelEventKindFill,
			EventDate:           eventTime,
			VolumeL:             pgtype.Float8{Float64: volume, Valid: true},
			AbvPct:              pgtype.Float8{Float64: abv, Valid: true},
			Laa:                 pgtype.Float8{Float64: laa, Valid: true},
			BulkMovementID:      uuid.NullUUID{UUID: mv.ID, Valid: true},
			LocationAfter:       "",
			Notes:               in.GetNotes(),
			UserID:              uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}

		// First fill sets fill_date.
		attrs, e = q.GetBarrelAttributes(ctx, barrelID)
		if e != nil {
			return e
		}
		if !attrs.FillDate.Valid {
			fillDate := pgtype.Date{Valid: true, Time: eventTime.Time.UTC()}
			if e := q.SetBarrelFillDate(ctx, sqlcgen.SetBarrelFillDateParams{
				ContainerID: barrelID,
				FillDate:    fillDate,
			}); e != nil {
				return e
			}
			attrs.FillDate = fillDate
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "barrel", barrelID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":     "fill",
				"barrel":    barrelContainer.Name,
				"source_id": sourceID.String(),
				"volume_l":  volume,
				"abv_pct":   abv,
				"laa":       laa,
				"fill_date": attrs.FillDate.Time.Format("2006-01-02"),
			})
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("barrel or source not found"))
		}
		s.logger.Error("FillBarrel", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.FillBarrelResponse{
		Event:  barrelEventToProto(event),
		Barrel: barrelToProto(barrelContainer, attrs),
	}), nil
}

func (s *BarrelService) DumpBarrel(
	ctx context.Context,
	req *connect.Request[stillhousev1.DumpBarrelRequest],
) (*connect.Response[stillhousev1.DumpBarrelResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	barrelID, err := uuid.Parse(in.GetBarrelId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid barrel_id"))
	}
	destID, err := uuid.Parse(in.GetDestinationContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid destination_container_id"))
	}
	if in.GetVolumeL() <= 0 || in.GetAbvPct() < 0 || in.GetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid volume_l / abv_pct"))
	}

	// Resolve the reading to 20 °C before anything is written. A warehouse
	// is the least likely place on site for a gauge to be taken at the
	// reference temperature, so the correction matters most here.
	corrected, err := resolveStrength(strengthInput{
		ObservedVolumeL: in.GetVolumeL(),
		AbvPct:          in.GetAbvPct(),
		DensityKgM3:     in.GetDensityKgM3(),
		DensityIsSet:    in.GetDensityKgM3Set(),
		TemperatureC:    in.GetTemperatureC(),
		TemperatureSet:  in.GetTemperatureCSet(),
	})
	if err != nil {
		return nil, alcoholometryError(err)
	}
	// Everything below works in litres and strength at 20 °C.
	volume, abv := corrected.VolumeL20C, corrected.StrengthPct20C

	eventTime := timestampOrNow(in.GetEventDate())

	var (
		barrelContainer sqlcgen.BulkContainer
		attrs           sqlcgen.BarrelAttribute
		event           sqlcgen.BarrelEvent
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: eventTime.Time}); e != nil {
			return e
		}
		locked, e := lockContainers(ctx, q, barrelID, destID)
		if e != nil {
			return e
		}
		barrel := locked[barrelID]
		if barrel.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("target is not a barrel"))
		}
		if barrel.CurrentVolumeL <= 0 {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("barrel is already empty"))
		}
		dest := locked[destID]

		laa := volume * abv / 100
		if barrel.CurrentLaa+1e-6 < laa {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s holds %.4f LAA; this dump would remove %.4f", barrel.Name, barrel.CurrentLaa, laa))
		}

		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{UUID: barrelID, Valid: true},
			DestinationContainerID: uuid.NullUUID{UUID: destID, Valid: true},
			VolumeL:                volume,
			AbvPct:                 abv,
			Laa:                    laa,
			Reason:                 sqlcgen.BulkMovementReasonInterTankTransfer,
			ReferenceType:          "barrel_dump",
			ReferenceID:            uuid.NullUUID{UUID: barrelID, Valid: true},
			Notes:                  in.GetNotes(),
			OccurredAt:             eventTime,
		})
		if e != nil {
			return e
		}

		// A dump empties the cask, so whatever the gauge didn't account for
		// stayed in the wood. That is a real loss and a reportable B266
		// line — zeroing the balance without recording it just deletes
		// alcohol from the ledger and understates losses for the period.
		residualLAA := barrel.CurrentLaa - laa
		residualVol := barrel.CurrentVolumeL - volume
		if residualLAA > 1e-9 {
			if residualVol < 0 {
				residualVol = 0
			}
			residualABV := 0.0
			if residualVol > 0 {
				residualABV = residualLAA / residualVol * 100
			}
			if _, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
				TenantID:          u.TenantID,
				SourceContainerID: uuid.NullUUID{UUID: barrelID, Valid: true},
				VolumeL:           residualVol,
				AbvPct:            residualABV,
				Laa:               residualLAA,
				Reason:            sqlcgen.BulkMovementReasonLossEvaporation,
				ReferenceType:     "barrel_dump_residual",
				ReferenceID:       uuid.NullUUID{UUID: barrelID, Valid: true},
				Notes:             "retained by the cask at dump",
				OccurredAt:        eventTime,
			}); e != nil {
				return e
			}
		}

		// Empty the barrel.
		barrelContainer, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             barrelID,
			CurrentVolumeL: 0,
			CurrentAbvPct:  pgtype.Float8{Valid: false},
			CurrentLaa:     0,
		})
		if e != nil {
			return e
		}

		// Deposit into destination.
		newDestVol, newDestAbv, newDestLAA := applyDeposit(
			dest.CurrentVolumeL, dest.CurrentAbvPct,
			volume, abv,
		)
		if _, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             destID,
			CurrentVolumeL: newDestVol,
			CurrentAbvPct:  newDestAbv,
			CurrentLaa:     newDestLAA,
		}); e != nil {
			return e
		}

		event, e = q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
			TemperatureC:        optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL:     optionalFloat(true, in.GetVolumeL()),
			ObservedDensityKgM3: optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:       corrected.VolumeFactorC,
			StrengthSource:      strengthSourceToDB(corrected.Source),
			TenantID:            u.TenantID,
			ContainerID:         barrelID,
			Kind:                sqlcgen.BarrelEventKindDump,
			EventDate:           eventTime,
			VolumeL:             pgtype.Float8{Float64: volume, Valid: true},
			AbvPct:              pgtype.Float8{Float64: abv, Valid: true},
			Laa:                 pgtype.Float8{Float64: laa, Valid: true},
			BulkMovementID:      uuid.NullUUID{UUID: mv.ID, Valid: true},
			Notes:               in.GetNotes(),
			UserID:              uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}

		// Clear fill_date, capture days-aged.
		attrs, e = q.GetBarrelAttributes(ctx, barrelID)
		if e != nil {
			return e
		}
		daysAged := int32(0)
		if attrs.FillDate.Valid {
			daysAged = int32(eventTime.Time.UTC().Sub(attrs.FillDate.Time).Hours() / 24)
		}
		if e := q.SetBarrelDumpedClock(ctx, sqlcgen.SetBarrelDumpedClockParams{
			ContainerID:    barrelID,
			DaysAgedAtDump: pgtype.Int4{Int32: daysAged, Valid: true},
		}); e != nil {
			return e
		}
		attrs.FillDate = pgtype.Date{Valid: false}
		attrs.DaysAgedAtDump = pgtype.Int4{Int32: daysAged, Valid: true}
		return audit.Write(ctx, q, u.TenantID, u.ID, "barrel", barrelID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":          "dump",
				"barrel":         barrelContainer.Name,
				"destination_id": destID.String(),
				"volume_l":       volume,
				"abv_pct":        abv,
				"laa":            laa,
				"days_aged":      daysAged,
			})
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("barrel or destination not found"))
		}
		s.logger.Error("DumpBarrel", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.DumpBarrelResponse{
		Event:  barrelEventToProto(event),
		Barrel: barrelToProto(barrelContainer, attrs),
	}), nil
}

// RegaugeBarrel records the actual contents on inspection. The lost
// portion (cur_LAA − new_LAA) is written as a loss_evaporation movement.
// new measurements must satisfy new_LAA <= cur_LAA.
func (s *BarrelService) RegaugeBarrel(
	ctx context.Context,
	req *connect.Request[stillhousev1.RegaugeBarrelRequest],
) (*connect.Response[stillhousev1.RegaugeBarrelResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	barrelID, err := uuid.Parse(in.GetBarrelId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid barrel_id"))
	}
	// Name the field and the bound. "invalid new_volume_l / new_abv_pct"
	// tells someone standing in a warehouse with a phone nothing about
	// which of the two numbers to look at.
	if in.GetNewVolumeL() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("new_volume_l must be >= 0, got %g", in.GetNewVolumeL()))
	}
	if err := validateAbvPct("new_abv_pct", in.GetNewAbvPct()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Resolve the reading to 20 °C before anything is written. A warehouse
	// is the least likely place on site for a gauge to be taken at the
	// reference temperature, so the correction matters most here.
	corrected, err := resolveStrength(strengthInput{
		ObservedVolumeL: in.GetNewVolumeL(),
		AbvPct:          in.GetNewAbvPct(),
		DensityKgM3:     in.GetDensityKgM3(),
		DensityIsSet:    in.GetDensityKgM3Set(),
		TemperatureC:    in.GetTemperatureC(),
		TemperatureSet:  in.GetTemperatureCSet(),
	})
	if err != nil {
		return nil, alcoholometryError(err)
	}
	// Everything below works in litres and strength at 20 °C.
	volume, abv := corrected.VolumeL20C, corrected.StrengthPct20C

	eventTime := timestampOrNow(in.GetEventDate())

	var (
		barrelContainer sqlcgen.BulkContainer
		attrs           sqlcgen.BarrelAttribute
		event           sqlcgen.BarrelEvent
		lostLAA         float64
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: eventTime.Time}); e != nil {
			return e
		}
		lockedRegauge, e := lockContainers(ctx, q, barrelID)
		if e != nil {
			return e
		}
		barrel := lockedRegauge[barrelID]
		if barrel.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("target is not a barrel"))
		}
		newLAA := volume * abv / 100
		if newLAA > barrel.CurrentLaa+1e-6 {
			return connect.NewError(connect.CodeInvalidArgument,
				errors.New("new LAA cannot exceed current LAA — regauges record losses only"))
		}
		// A regauge that drains a non-empty barrel to zero is almost
		// always a misclassified dump — the entire contents would be
		// posted as evaporation rather than as a transfer to a
		// destination container. Force the operator to use dump_barrel,
		// which preserves the audit trail (destination, days_aged) and
		// keeps the bulk ledger reconcilable. Empty→empty regauges (no-op
		// snapshots) are still allowed.
		if volume == 0 && barrel.CurrentVolumeL > 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("regauge cannot fully drain a non-empty barrel — use dump_barrel to record a transfer"))
		}
		lossVol := barrel.CurrentVolumeL - volume
		lossLAA := barrel.CurrentLaa - newLAA
		lostLAA = lossLAA

		var mvID uuid.NullUUID
		if lossVol > 0 && lossLAA > 0 {
			lossABV := lossLAA / lossVol * 100
			mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
				TenantID:               u.TenantID,
				SourceContainerID:      uuid.NullUUID{UUID: barrelID, Valid: true},
				DestinationContainerID: uuid.NullUUID{Valid: false},
				VolumeL:                lossVol,
				AbvPct:                 math.Max(0, math.Min(100, lossABV)),
				Laa:                    lossLAA,
				Reason:                 sqlcgen.BulkMovementReasonLossEvaporation,
				ReferenceType:          "barrel_regauge",
				ReferenceID:            uuid.NullUUID{UUID: barrelID, Valid: true},
				Notes:                  in.GetNotes(),
				OccurredAt:             eventTime,
			})
			if e != nil {
				return e
			}
			mvID = uuid.NullUUID{UUID: mv.ID, Valid: true}
		}

		// Update barrel to new measurements.
		var newAbv pgtype.Float8
		if volume > 0 {
			newAbv = pgtype.Float8{Float64: abv, Valid: true}
		}
		barrelContainer, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             barrelID,
			CurrentVolumeL: volume,
			CurrentAbvPct:  newAbv,
			CurrentLaa:     newLAA,
		})
		if e != nil {
			return e
		}

		event, e = q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
			TemperatureC:        optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL:     optionalFloat(true, in.GetNewVolumeL()),
			ObservedDensityKgM3: optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:       corrected.VolumeFactorC,
			StrengthSource:      strengthSourceToDB(corrected.Source),
			TenantID:            u.TenantID,
			ContainerID:         barrelID,
			Kind:                sqlcgen.BarrelEventKindRegauge,
			EventDate:           eventTime,
			VolumeL:             pgtype.Float8{Float64: volume, Valid: true},
			AbvPct:              pgtype.Float8{Float64: abv, Valid: true},
			Laa:                 pgtype.Float8{Float64: newLAA, Valid: true},
			BulkMovementID:      mvID,
			Notes:               in.GetNotes(),
			UserID:              uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}

		attrs, e = q.GetBarrelAttributes(ctx, barrelID)
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "barrel", barrelID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":        "regauge",
				"barrel":       barrelContainer.Name,
				"new_volume_l": volume,
				"new_abv_pct":  abv,
				"new_laa":      newLAA,
				"lost_laa":     lostLAA,
			})
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("barrel not found"))
		}
		s.logger.Error("RegaugeBarrel", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RegaugeBarrelResponse{
		Event:   barrelEventToProto(event),
		Barrel:  barrelToProto(barrelContainer, attrs),
		LostLaa: round4(lostLAA),
	}), nil
}

// VoidBarrelEvent unwinds a fill or dump event by inverting its linked
// bulk_movement and updating both container balances. Regauge events are
// rejected — they store a "snapshot" without preserving the previous
// state, so a clean undo isn't possible; the operator should record a
// new corrective regauge instead. Events with no linked bulk_movement
// (e.g. legacy or 'move' kind) just get marked voided.
func (s *BarrelService) VoidBarrelEvent(
	ctx context.Context,
	req *connect.Request[stillhousev1.VoidBarrelEventRequest],
) (*connect.Response[stillhousev1.VoidBarrelEventResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	reason := req.Msg.GetReason()
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("reason is required"))
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		ev, e := q.GetBarrelEvent(ctx, id)
		if e != nil {
			return e
		}
		if ev.VoidedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("event is already voided"))
		}
		if ev.Kind == sqlcgen.BarrelEventKindRegauge {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("regauge events cannot be voided; record a new corrective regauge instead"))
		}

		// Reverse the linked bulk_movement, if any. The pattern: swap src/dst,
		// reapply balances on both sides via applyDeposit / direct subtraction.
		if ev.BulkMovementID.Valid {
			mv, me := q.GetBulkMovementForBarrelEvent(ctx, ev.BulkMovementID.UUID)
			if me != nil {
				return me
			}
			// Destination loses (we're undoing a deposit there)…
			if mv.DestinationContainerID.Valid {
				dst, ge := q.GetBulkContainerForUpdate(ctx, mv.DestinationContainerID.UUID)
				if ge != nil {
					return ge
				}
				newVol := dst.CurrentVolumeL - mv.VolumeL
				if newVol < 0 {
					return connect.NewError(connect.CodeFailedPrecondition,
						errors.New("destination has been drained below the event volume — void downstream movements first"))
				}
				newLAA := dst.CurrentLaa - mv.Laa
				if newLAA < 0 {
					newLAA = 0
				}
				var newABV pgtype.Float8
				if newVol > 0 && dst.CurrentAbvPct.Valid {
					newABV = dst.CurrentAbvPct
				}
				if _, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
					ID: dst.ID, CurrentVolumeL: newVol, CurrentAbvPct: newABV, CurrentLaa: newLAA,
				}); e != nil {
					return e
				}
			}
			// …and source regains (we're undoing a withdrawal there).
			if mv.SourceContainerID.Valid {
				src, ge := q.GetBulkContainerForUpdate(ctx, mv.SourceContainerID.UUID)
				if ge != nil {
					return ge
				}
				newVol, newABV, newLAA := applyDeposit(
					src.CurrentVolumeL, src.CurrentAbvPct, mv.VolumeL, mv.AbvPct,
				)
				if _, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
					ID: src.ID, CurrentVolumeL: newVol, CurrentAbvPct: newABV, CurrentLaa: newLAA,
				}); e != nil {
					return e
				}
			}
			// Note on voiding a DUMP: this reverses the transfer only. The
			// residual the cask kept (booked as loss_evaporation by
			// DumpBarrel) stays on the books, and the barrel comes back
			// holding what was transferred rather than what it held before
			// the dump. That is deliberate and it conserves — 70.2 back in
			// the cask plus 9.8 still booked as loss is the same 80 LAA
			// that went in. The wood absorbed what it absorbed; voiding a
			// paperwork entry doesn't give it back.
			//
			// Audit-friendly offsetting ledger row so the journal stays
			// reconstructable. Reason regauge_correction matches the pattern
			// used by other void handlers.
			swappedSrc := mv.DestinationContainerID
			swappedDst := mv.SourceContainerID
			if _, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
				TenantID:               u.TenantID,
				SourceContainerID:      swappedSrc,
				DestinationContainerID: swappedDst,
				VolumeL:                mv.VolumeL,
				AbvPct:                 mv.AbvPct,
				Laa:                    mv.Laa,
				Reason:                 sqlcgen.BulkMovementReasonRegaugeCorrection,
				ReferenceType:          "barrel_event_void",
				ReferenceID:            uuid.NullUUID{UUID: id, Valid: true},
				Notes:                  "void of barrel event " + string(ev.Kind) + ": " + reason,
				OccurredAt:             pgtype.Timestamptz{Valid: true, Time: time.Now()},
			}); e != nil {
				return e
			}
		}

		if _, e := q.VoidBarrelEvent(ctx, sqlcgen.VoidBarrelEventParams{
			ID:           id,
			VoidedBy:     uuid.NullUUID{UUID: u.ID, Valid: true},
			VoidedReason: reason,
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "barrel_event", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":        "voided",
				"kind":         string(ev.Kind),
				"container_id": ev.ContainerID.String(),
				"reason":       reason,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("barrel event not found"))
		}
		s.logger.Error("VoidBarrelEvent", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.VoidBarrelEventResponse{}), nil
}

// --- converters ---

func barrelToProto(c sqlcgen.BulkContainer, a sqlcgen.BarrelAttribute) *stillhousev1.Barrel {
	b := &stillhousev1.Barrel{
		Id:                a.ContainerID.String(),
		Name:              c.Name,
		Location:          c.Location,
		Notes:             c.Notes,
		Archived:          c.Archived,
		CurrentVolumeL:    round4(c.CurrentVolumeL),
		CurrentLaa:        round4(c.CurrentLaa),
		CreatedAt:         timestamppb.New(c.CreatedAt.Time),
		UpdatedAt:         timestamppb.New(c.UpdatedAt.Time),
		CooperageSupplier: a.CooperageSupplier,
		WoodSpecies:       a.WoodSpecies,
		PriorUse:          a.PriorUse,
		SerialBurnin:      a.SerialBurnin,
		Rickhouse:         a.Rickhouse,
		RowPosition:       a.RowPosition,
		LevelPosition:     a.LevelPosition,
		ColumnPosition:    a.ColumnPosition,
	}
	if c.CapacityL.Valid {
		b.CapacityL = round4(c.CapacityL.Float64)
		b.CapacityLSet = true
		b.SmallWood = c.CapacityL.Float64 <= smallWoodCapacityL
	}
	if c.CurrentAbvPct.Valid {
		b.CurrentAbvPct = round2(c.CurrentAbvPct.Float64)
		b.CurrentAbvPctSet = true
	}
	if a.CharLevel.Valid {
		b.CharLevel = a.CharLevel.Int32
		b.CharLevelSet = true
	}
	if a.FillDate.Valid {
		b.FillDate = a.FillDate.Time.Format("2006-01-02")
		days := int32(time.Since(a.FillDate.Time).Hours() / 24)
		if days < 0 {
			days = 0
		}
		b.DaysAged = days
		b.CanadianWhiskyEligible = b.SmallWood && days >= canadianWhiskyAgingDays
		if !b.CanadianWhiskyEligible {
			b.DaysToCanadianWhiskyEligible = canadianWhiskyAgingDays - days
			if b.DaysToCanadianWhiskyEligible < 0 {
				b.DaysToCanadianWhiskyEligible = 0
			}
		}
	}
	if a.DaysAgedAtDump.Valid {
		b.DaysAgedAtDump = a.DaysAgedAtDump.Int32
		b.DaysAgedAtDumpSet = true
	}
	return b
}

func barrelRowToProto(r sqlcgen.ListBarrelsRow) *stillhousev1.Barrel {
	container := sqlcgen.BulkContainer{
		ID:             r.ID,
		Name:           r.Name,
		Kind:           r.Kind,
		CapacityL:      r.CapacityL,
		Location:       r.Location,
		Notes:          r.Notes,
		Archived:       r.Archived,
		CurrentVolumeL: r.CurrentVolumeL,
		CurrentAbvPct:  r.CurrentAbvPct,
		CurrentLaa:     r.CurrentLaa,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
	attrs := sqlcgen.BarrelAttribute{
		ContainerID:       r.ID,
		CooperageSupplier: r.CooperageSupplier,
		CharLevel:         r.CharLevel,
		WoodSpecies:       r.WoodSpecies,
		PriorUse:          r.PriorUse,
		SerialBurnin:      r.SerialBurnin,
		Rickhouse:         r.Rickhouse,
		RowPosition:       r.RowPosition,
		LevelPosition:     r.LevelPosition,
		ColumnPosition:    r.ColumnPosition,
		FillDate:          r.FillDate,
		DaysAgedAtDump:    r.DaysAgedAtDump,
	}
	out := barrelToProto(container, attrs)
	out.Maturation = buildMaturation(
		r.FillVolumeL, r.FillAbvPct, r.FillLaa,
		r.CurrentVolumeL, out.CurrentAbvPct, r.CurrentLaa,
		out.DaysAged, r.LevelPosition,
	)
	return out
}

func barrelEventToProto(e sqlcgen.BarrelEvent) *stillhousev1.BarrelEvent {
	out := &stillhousev1.BarrelEvent{
		Id:             e.ID.String(),
		ContainerId:    e.ContainerID.String(),
		Kind:           barrelEventKindToProto(e.Kind),
		EventDate:      timestamppb.New(e.EventDate.Time),
		BulkMovementId: nullUUIDString(e.BulkMovementID),
		LocationAfter:  e.LocationAfter,
		Notes:          e.Notes,
		UserId:         nullUUIDString(e.UserID),
		CreatedAt:      timestamppb.New(e.CreatedAt.Time),
	}
	if e.VolumeL.Valid {
		out.VolumeL = round4(e.VolumeL.Float64)
		out.VolumeLSet = true
	}
	if e.AbvPct.Valid {
		out.AbvPct = round2(e.AbvPct.Float64)
		out.AbvPctSet = true
	}
	if e.Laa.Valid {
		out.Laa = round4(e.Laa.Float64)
		out.LaaSet = true
	}
	if e.TemperatureC.Valid {
		out.TemperatureC = e.TemperatureC.Float64
		out.TemperatureCSet = true
	}
	if e.ObservedVolumeL.Valid {
		out.ObservedVolumeL = round4(e.ObservedVolumeL.Float64)
	}
	if e.ObservedDensityKgM3.Valid {
		out.ObservedDensityKgM3 = e.ObservedDensityKgM3.Float64
		out.ObservedDensityKgM3Set = true
	}
	out.VolumeFactorC = e.VolumeFactorC
	out.StrengthSource = strengthSourceToProto(e.StrengthSource)
	return out
}

func barrelEventKindToProto(k sqlcgen.BarrelEventKind) stillhousev1.BarrelEventKind {
	switch k {
	case sqlcgen.BarrelEventKindFill:
		return stillhousev1.BarrelEventKind_BARREL_EVENT_KIND_FILL
	case sqlcgen.BarrelEventKindRegauge:
		return stillhousev1.BarrelEventKind_BARREL_EVENT_KIND_REGAUGE
	case sqlcgen.BarrelEventKindSample:
		return stillhousev1.BarrelEventKind_BARREL_EVENT_KIND_SAMPLE
	case sqlcgen.BarrelEventKindDump:
		return stillhousev1.BarrelEventKind_BARREL_EVENT_KIND_DUMP
	case sqlcgen.BarrelEventKindMove:
		return stillhousev1.BarrelEventKind_BARREL_EVENT_KIND_MOVE
	case sqlcgen.BarrelEventKindDestroy:
		return stillhousev1.BarrelEventKind_BARREL_EVENT_KIND_DESTROY
	}
	return stillhousev1.BarrelEventKind_BARREL_EVENT_KIND_UNSPECIFIED
}

// latestFill finds the most recent non-voided fill in an event list.
// ListBarrelEvents returns newest first, so the first match wins.
func latestFill(events []sqlcgen.BarrelEvent) (vol, abv, laa pgtype.Float8, ok bool) {
	for _, e := range events {
		if e.Kind != sqlcgen.BarrelEventKindFill || e.VoidedAt.Valid {
			continue
		}
		return e.VolumeL, e.AbvPct, e.Laa, true
	}
	return pgtype.Float8{}, pgtype.Float8{}, pgtype.Float8{}, false
}
