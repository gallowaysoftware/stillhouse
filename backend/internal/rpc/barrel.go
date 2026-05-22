package rpc

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	out := &stillhousev1.GetBarrelResponse{
		Barrel: barrelToProto(container, attrs),
		Events: make([]*stillhousev1.BarrelEvent, 0, len(events)),
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

	eventTime := timestampOrNow(in.GetEventDate())

	var (
		barrelContainer sqlcgen.BulkContainer
		attrs           sqlcgen.BarrelAttribute
		event           sqlcgen.BarrelEvent
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		barrel, e := q.GetBulkContainer(ctx, barrelID)
		if e != nil {
			return e
		}
		if barrel.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("target is not a barrel"))
		}
		source, e := q.GetBulkContainer(ctx, sourceID)
		if e != nil {
			return e
		}
		if source.CurrentVolumeL < in.GetVolumeL() {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("source container has insufficient volume"))
		}

		laa := in.GetVolumeL() * in.GetAbvPct() / 100

		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{UUID: sourceID, Valid: true},
			DestinationContainerID: uuid.NullUUID{UUID: barrelID, Valid: true},
			VolumeL:                in.GetVolumeL(),
			AbvPct:                 in.GetAbvPct(),
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

		// Update source (withdraw at source's existing ABV).
		newSrcVol := source.CurrentVolumeL - in.GetVolumeL()
		newSrcAbv := source.CurrentAbvPct
		newSrcLAA := 0.0
		if newSrcVol > 0 && newSrcAbv.Valid {
			newSrcLAA = newSrcVol * newSrcAbv.Float64 / 100
		} else if newSrcVol == 0 {
			newSrcAbv = pgtype.Float8{Valid: false}
		}
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
			in.GetVolumeL(), in.GetAbvPct(),
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
			TenantID:        u.TenantID,
			ContainerID:     barrelID,
			Kind:            sqlcgen.BarrelEventKindFill,
			EventDate:       eventTime,
			VolumeL:         pgtype.Float8{Float64: in.GetVolumeL(), Valid: true},
			AbvPct:          pgtype.Float8{Float64: in.GetAbvPct(), Valid: true},
			Laa:             pgtype.Float8{Float64: laa, Valid: true},
			BulkMovementID:  uuid.NullUUID{UUID: mv.ID, Valid: true},
			LocationAfter:   "",
			Notes:           in.GetNotes(),
			UserID:          uuid.NullUUID{UUID: u.ID, Valid: true},
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
		return nil
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

	eventTime := timestampOrNow(in.GetEventDate())

	var (
		barrelContainer sqlcgen.BulkContainer
		attrs           sqlcgen.BarrelAttribute
		event           sqlcgen.BarrelEvent
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		barrel, e := q.GetBulkContainer(ctx, barrelID)
		if e != nil {
			return e
		}
		if barrel.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("target is not a barrel"))
		}
		if barrel.CurrentVolumeL <= 0 {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("barrel is already empty"))
		}
		dest, e := q.GetBulkContainer(ctx, destID)
		if e != nil {
			return e
		}

		laa := in.GetVolumeL() * in.GetAbvPct() / 100

		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{UUID: barrelID, Valid: true},
			DestinationContainerID: uuid.NullUUID{UUID: destID, Valid: true},
			VolumeL:                in.GetVolumeL(),
			AbvPct:                 in.GetAbvPct(),
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
			in.GetVolumeL(), in.GetAbvPct(),
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
			TenantID:       u.TenantID,
			ContainerID:    barrelID,
			Kind:           sqlcgen.BarrelEventKindDump,
			EventDate:      eventTime,
			VolumeL:        pgtype.Float8{Float64: in.GetVolumeL(), Valid: true},
			AbvPct:         pgtype.Float8{Float64: in.GetAbvPct(), Valid: true},
			Laa:            pgtype.Float8{Float64: laa, Valid: true},
			BulkMovementID: uuid.NullUUID{UUID: mv.ID, Valid: true},
			Notes:          in.GetNotes(),
			UserID:         uuid.NullUUID{UUID: u.ID, Valid: true},
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
			ContainerID:      barrelID,
			DaysAgedAtDump:   pgtype.Int4{Int32: daysAged, Valid: true},
		}); e != nil {
			return e
		}
		attrs.FillDate = pgtype.Date{Valid: false}
		attrs.DaysAgedAtDump = pgtype.Int4{Int32: daysAged, Valid: true}
		return nil
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
	if in.GetNewVolumeL() < 0 || in.GetNewAbvPct() < 0 || in.GetNewAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid new_volume_l / new_abv_pct"))
	}

	eventTime := timestampOrNow(in.GetEventDate())

	var (
		barrelContainer sqlcgen.BulkContainer
		attrs           sqlcgen.BarrelAttribute
		event           sqlcgen.BarrelEvent
		lostLAA         float64
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		barrel, e := q.GetBulkContainer(ctx, barrelID)
		if e != nil {
			return e
		}
		if barrel.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("target is not a barrel"))
		}
		newLAA := in.GetNewVolumeL() * in.GetNewAbvPct() / 100
		if newLAA > barrel.CurrentLaa+1e-6 {
			return connect.NewError(connect.CodeInvalidArgument,
				errors.New("new LAA cannot exceed current LAA — regauges record losses only"))
		}
		lossVol := barrel.CurrentVolumeL - in.GetNewVolumeL()
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
		if in.GetNewVolumeL() > 0 {
			newAbv = pgtype.Float8{Float64: in.GetNewAbvPct(), Valid: true}
		}
		barrelContainer, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             barrelID,
			CurrentVolumeL: in.GetNewVolumeL(),
			CurrentAbvPct:  newAbv,
			CurrentLaa:     newLAA,
		})
		if e != nil {
			return e
		}

		event, e = q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
			TenantID:       u.TenantID,
			ContainerID:    barrelID,
			Kind:           sqlcgen.BarrelEventKindRegauge,
			EventDate:      eventTime,
			VolumeL:        pgtype.Float8{Float64: in.GetNewVolumeL(), Valid: true},
			AbvPct:         pgtype.Float8{Float64: in.GetNewAbvPct(), Valid: true},
			Laa:            pgtype.Float8{Float64: newLAA, Valid: true},
			BulkMovementID: mvID,
			Notes:          in.GetNotes(),
			UserID:         uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}

		attrs, e = q.GetBarrelAttributes(ctx, barrelID)
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
		s.logger.Error("RegaugeBarrel", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RegaugeBarrelResponse{
		Event:   barrelEventToProto(event),
		Barrel:  barrelToProto(barrelContainer, attrs),
		LostLaa: lostLAA,
	}), nil
}

// --- converters ---

func barrelToProto(c sqlcgen.BulkContainer, a sqlcgen.BarrelAttribute) *stillhousev1.Barrel {
	b := &stillhousev1.Barrel{
		Id:                a.ContainerID.String(),
		Name:              c.Name,
		Location:          c.Location,
		Notes:             c.Notes,
		Archived:          c.Archived,
		CurrentVolumeL:    c.CurrentVolumeL,
		CurrentLaa:        c.CurrentLaa,
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
		b.CapacityL = c.CapacityL.Float64
		b.CapacityLSet = true
		b.SmallWood = c.CapacityL.Float64 <= smallWoodCapacityL
	}
	if c.CurrentAbvPct.Valid {
		b.CurrentAbvPct = c.CurrentAbvPct.Float64
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
	return barrelToProto(container, attrs)
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
		out.VolumeL = e.VolumeL.Float64
		out.VolumeLSet = true
	}
	if e.AbvPct.Valid {
		out.AbvPct = e.AbvPct.Float64
		out.AbvPctSet = true
	}
	if e.Laa.Valid {
		out.Laa = e.Laa.Float64
		out.LaaSet = true
	}
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
