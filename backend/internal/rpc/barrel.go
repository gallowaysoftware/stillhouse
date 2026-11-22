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
	"github.com/gallowaysoftware/stillhouse/backend/internal/zone"
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
		Barrel: barrelToProto(container, attrs, s.db.Today(ctx, u.TenantID)),
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
	today := s.db.Today(ctx, u.TenantID)
	out := &stillhousev1.ListBarrelsResponse{
		Barrels: make([]*stillhousev1.Barrel, 0, len(rows)),
	}
	for _, r := range rows {
		b := barrelRowToProto(r, today)
		b.OwnerCustomerId = nullUUIDString(r.OwnerCustomerID)
		b.OwnerName = r.OwnerName
		b.Possession = bulkPossessionToProto(r.Possession)
		b.HeldByName = r.HeldByName
		b.HeldByLicenceNo = r.HeldByLicenceNo
		out.Barrels = append(out.Barrels, b)
		out.TotalCount++
		out.TotalLaa += b.CurrentLaa
		// The same alcohol counted four ways, because "how much whisky is
		// in the rackhouse" and "how much whisky do we own" stop being the
		// same question the first time a customer's cask arrives.
		here := r.Possession != sqlcgen.BulkPossessionHeldElsewhere
		mine := !r.OwnerCustomerID.Valid
		if mine {
			out.OwnedLaa += b.CurrentLaa
		} else {
			out.ThirdPartyCount++
		}
		if here {
			out.HeldLaa += b.CurrentLaa
		}
		switch {
		case here && !mine:
			out.HeldForOthersLaa += b.CurrentLaa
		case !here && mine:
			out.HeldElsewhereLaa += b.CurrentLaa
		case !here && !mine:
			out.ThirdPartyElsewhereLaa += b.CurrentLaa
		}
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
	barrel := barrelToProto(container, attrs, s.db.Today(ctx, u.TenantID))
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
	// The last link in the audit chain: which approved instrument made
	// each determination (EDM3-1-1 ¶24). Resolved in one pass over the
	// distinct instruments rather than once per event.
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		cache := newInstrumentCache(q, time.Now())
		for i, e := range events {
			out.Events[i].Instruments = cache.refs(ctx,
				e.VolumeInstrumentID, e.StrengthInstrumentID, e.TemperatureInstrumentID)
		}
		return nil
	}); err != nil {
		// Decoration only. A gauge is still a gauge without its
		// instrument block, and failing the whole read over it would be
		// worse than showing the figures.
		s.logger.Warn("GetBarrel: resolve instruments", "err", err)
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
	if sourceID == barrelID {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a barrel cannot be filled from itself"))
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
	// Conditions that don't block the event but that the operator
	// should see — an instrument past due for calibration.
	var eventWarnings []string
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, zone.Date(ctx, eventTime.Time)); e != nil {
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
		if e := assertTakesSpirit(barrel, volume); e != nil {
			return e
		}
		source := locked[sourceID]
		d, e := drawFrom(source, volume, abv)
		if e != nil {
			return e
		}
		volume := d.volumeL
		laa := d.laa
		attrs, e = q.GetBarrelAttributes(ctx, barrelID)
		if e != nil {
			return e
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
		if _, e := saveBalance(ctx, q, sourceID, d.rest); e != nil {
			return e
		}
		barrelContainer, e = saveBalance(ctx, q, barrelID, balanceOf(barrel).add(volume, laa))
		if e != nil {
			return e
		}

		// The instruments this determination was made with. Checked
		// against the date of the event, not today — an instrument whose
		// approval has since lapsed was still approved when the reading
		// was taken.
		instr, e := checkInstruments(ctx, q, in.GetInstruments(), eventTime.Time)
		if e != nil {
			return e
		}
		eventWarnings = instr.warnings

		event, e = q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
			TemperatureC:            optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL:         optionalFloat(true, in.GetVolumeL()),
			ObservedDensityKgM3:     optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:           corrected.VolumeFactorC,
			StrengthSource:          strengthSourceToDB(corrected.Source),
			VolumeInstrumentID:      instr.volume,
			StrengthInstrumentID:    instr.strength,
			TemperatureInstrumentID: instr.temperature,
			TenantID:                u.TenantID,
			ContainerID:             barrelID,
			Kind:                    sqlcgen.BarrelEventKindFill,
			EventDate:               eventTime,
			VolumeL:                 pgtype.Float8{Float64: volume, Valid: true},
			AbvPct:                  pgtype.Float8{Float64: abv, Valid: true},
			Laa:                     pgtype.Float8{Float64: laa, Valid: true},
			BulkMovementID:          uuid.NullUUID{UUID: mv.ID, Valid: true},
			LocationAfter:           "",
			Notes:                   in.GetNotes(),
			UserID:                  uuid.NullUUID{UUID: u.ID, Valid: true},
			PriorVolumeL:            pgtype.Float8{Float64: barrel.CurrentVolumeL, Valid: true},
			PriorLaa:                pgtype.Float8{Float64: barrel.CurrentLaa, Valid: true},
			PriorFillDate:           attrs.FillDate,
		})
		if e != nil {
			return e
		}

		if e := bookCorrection(ctx, q, u, source, d.correction, abv, eventTime,
			uuid.NullUUID{UUID: event.ID, Valid: true}, "a barrel fill"); e != nil {
			return e
		}

		// First fill sets fill_date.
		if !attrs.FillDate.Valid {
			fillDate := zone.Date(ctx, eventTime.Time)
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
		Event:    barrelEventToProto(event),
		Barrel:   barrelToProto(barrelContainer, attrs, s.db.Today(ctx, u.TenantID)),
		Warnings: eventWarnings,
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
	if destID == barrelID {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a barrel cannot be dumped into itself"))
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
	// Conditions that don't block the event but that the operator
	// should see — an instrument past due for calibration.
	var eventWarnings []string
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, zone.Date(ctx, eventTime.Time)); e != nil {
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
		if e := assertTakesSpirit(dest, volume); e != nil {
			return e
		}

		laa, gain, e := gaugedLAA(barrel, volume, abv)
		if e != nil {
			return e
		}
		attrs, e = q.GetBarrelAttributes(ctx, barrelID)
		if e != nil {
			return e
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

		// Empty the barrel.
		barrelContainer, e = saveBalance(ctx, q, barrelID, balance{})
		if e != nil {
			return e
		}
		if _, e := saveBalance(ctx, q, destID, balanceOf(dest).add(volume, laa)); e != nil {
			return e
		}

		// The instruments this determination was made with. Checked
		// against the date of the event, not today — an instrument whose
		// approval has since lapsed was still approved when the reading
		// was taken.
		instr, e := checkInstruments(ctx, q, in.GetInstruments(), eventTime.Time)
		if e != nil {
			return e
		}
		eventWarnings = instr.warnings

		event, e = q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
			TemperatureC:            optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL:         optionalFloat(true, in.GetVolumeL()),
			ObservedDensityKgM3:     optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:           corrected.VolumeFactorC,
			StrengthSource:          strengthSourceToDB(corrected.Source),
			VolumeInstrumentID:      instr.volume,
			StrengthInstrumentID:    instr.strength,
			TemperatureInstrumentID: instr.temperature,
			TenantID:                u.TenantID,
			ContainerID:             barrelID,
			Kind:                    sqlcgen.BarrelEventKindDump,
			EventDate:               eventTime,
			VolumeL:                 pgtype.Float8{Float64: volume, Valid: true},
			AbvPct:                  pgtype.Float8{Float64: abv, Valid: true},
			Laa:                     pgtype.Float8{Float64: laa, Valid: true},
			BulkMovementID:          uuid.NullUUID{UUID: mv.ID, Valid: true},
			Notes:                   in.GetNotes(),
			UserID:                  uuid.NullUUID{UUID: u.ID, Valid: true},
			PriorVolumeL:            pgtype.Float8{Float64: barrel.CurrentVolumeL, Valid: true},
			PriorLaa:                pgtype.Float8{Float64: barrel.CurrentLaa, Valid: true},
			PriorFillDate:           attrs.FillDate,
		})
		if e != nil {
			return e
		}

		// A dump empties the cask, so whatever the gauge didn't account for
		// stayed in the wood. That is a real loss and a reportable B266
		// line — zeroing the balance without recording it just deletes
		// alcohol from the ledger and understates losses for the period.
		residualLAA := barrel.CurrentLaa + gain - laa
		residualVol := barrel.CurrentVolumeL - volume
		if residualLAA > balanceEps {
			lossVol, lossABV := lossRow(residualVol, barrel.CurrentVolumeL, residualLAA)
			if _, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
				TenantID:          u.TenantID,
				SourceContainerID: uuid.NullUUID{UUID: barrelID, Valid: true},
				VolumeL:           lossVol,
				AbvPct:            lossABV,
				Laa:               residualLAA,
				Reason:            sqlcgen.BulkMovementReasonLossEvaporation,
				ReferenceType:     "barrel_dump_residual",
				ReferenceID:       uuid.NullUUID{UUID: event.ID, Valid: true},
				Notes:             "retained by the cask at dump",
				OccurredAt:        eventTime,
			}); e != nil {
				return e
			}
		}

		if e := bookCorrection(ctx, q, u, barrel, gain, abv, eventTime,
			uuid.NullUUID{UUID: event.ID, Valid: true}, "a dump"); e != nil {
			return e
		}

		// Clear fill_date, capture days-aged.
		daysAged := int32(0)
		if attrs.FillDate.Valid {
			daysAged = int32(zone.Day(zone.From(ctx), eventTime.Time).Sub(attrs.FillDate.Time).Hours() / 24)
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
		Event:    barrelEventToProto(event),
		Barrel:   barrelToProto(barrelContainer, attrs, s.db.Today(ctx, u.TenantID)),
		Warnings: eventWarnings,
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
	// Conditions that don't block the event but that the operator
	// should see — an instrument past due for calibration.
	var eventWarnings []string
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, zone.Date(ctx, eventTime.Time)); e != nil {
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
		newLAA, gain, e := gaugedLAA(barrel, volume, abv)
		if e != nil {
			return e
		}
		attrs, e = q.GetBarrelAttributes(ctx, barrelID)
		if e != nil {
			return e
		}
		// A regauge that drains a non-empty barrel to zero is almost
		// always a misclassified dump — the entire contents would be
		// posted as evaporation rather than as a transfer to a
		// destination container. Force the operator to use dump_barrel,
		// which preserves the audit trail (destination, days_aged) and
		// keeps the bulk ledger reconcilable. Empty→empty regauges (no-op
		// snapshots) are still allowed.
		if newLAA <= balanceEps && barrel.CurrentLaa > balanceEps {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("a regauge cannot take all the alcohol out of a barrel: check the strength, or dump it to record a transfer"))
		}
		lossVol := barrel.CurrentVolumeL - volume
		lossLAA := barrel.CurrentLaa + gain - newLAA
		lostLAA = lossLAA

		var mvID uuid.NullUUID
		if lossLAA > balanceEps {
			// A weaker cask has lost alcohol whether or not it lost litres:
			// in a humid warehouse it can gain them.
			rowVol, rowABV := lossRow(lossVol, barrel.CurrentVolumeL, lossLAA)
			mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
				TenantID:               u.TenantID,
				SourceContainerID:      uuid.NullUUID{UUID: barrelID, Valid: true},
				DestinationContainerID: uuid.NullUUID{Valid: false},
				VolumeL:                rowVol,
				AbvPct:                 rowABV,
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

		barrelContainer, e = saveBalance(ctx, q, barrelID, balance{volumeL: volume, laa: newLAA})
		if e != nil {
			return e
		}

		// The instruments this determination was made with. Checked
		// against the date of the event, not today — an instrument whose
		// approval has since lapsed was still approved when the reading
		// was taken.
		instr, e := checkInstruments(ctx, q, in.GetInstruments(), eventTime.Time)
		if e != nil {
			return e
		}
		eventWarnings = instr.warnings

		event, e = q.InsertBarrelEvent(ctx, sqlcgen.InsertBarrelEventParams{
			TemperatureC:            optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL:         optionalFloat(true, in.GetNewVolumeL()),
			ObservedDensityKgM3:     optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:           corrected.VolumeFactorC,
			StrengthSource:          strengthSourceToDB(corrected.Source),
			VolumeInstrumentID:      instr.volume,
			StrengthInstrumentID:    instr.strength,
			TemperatureInstrumentID: instr.temperature,
			TenantID:                u.TenantID,
			ContainerID:             barrelID,
			Kind:                    sqlcgen.BarrelEventKindRegauge,
			EventDate:               eventTime,
			VolumeL:                 pgtype.Float8{Float64: volume, Valid: true},
			AbvPct:                  pgtype.Float8{Float64: abv, Valid: true},
			Laa:                     pgtype.Float8{Float64: newLAA, Valid: true},
			BulkMovementID:          mvID,
			Notes:                   in.GetNotes(),
			UserID:                  uuid.NullUUID{UUID: u.ID, Valid: true},
			PriorVolumeL:            pgtype.Float8{Float64: barrel.CurrentVolumeL, Valid: true},
			PriorLaa:                pgtype.Float8{Float64: barrel.CurrentLaa, Valid: true},
			PriorFillDate:           attrs.FillDate,
		})
		if e != nil {
			return e
		}

		if e := bookCorrection(ctx, q, u, barrel, gain, abv, eventTime,
			uuid.NullUUID{UUID: event.ID, Valid: true}, "a regauge"); e != nil {
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
		Event:    barrelEventToProto(event),
		Barrel:   barrelToProto(barrelContainer, attrs, s.db.Today(ctx, u.TenantID)),
		LostLaa:  round4(lostLAA),
		Warnings: eventWarnings,
	}), nil
}

// lossRow is the litres and strength a loss is written with. The ledger
// wants positive litres and a strength, and a cask that lost alcohol
// without losing that many litres has neither, so the row rests on what
// the cask held. The LAA is the figure; these two only carry it.
func lossRow(lostVolumeL, heldVolumeL, lostLAA float64) (volumeL, abvPct float64) {
	if lostVolumeL > balanceEps && lostLAA <= lostVolumeL {
		return lostVolumeL, lostLAA / lostVolumeL * 100
	}
	return heldVolumeL, math.Min(100, lostLAA/heldVolumeL*100)
}

// VoidBarrelEvent puts back what a fill, dump or regauge changed: both
// vessels, what the wood was said to keep, any correction booked with it,
// and the maturation clock. The reversals are dated with the event, so as of
// any moment the two cancel and no return carries a trace.
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
		if e := unwindBarrelEvent(ctx, q, u, ev, reason); e != nil {
			return e
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

// unwindBarrelEvent reverses everything an event did to the ledger.
func unwindBarrelEvent(ctx context.Context, q *sqlcgen.Queries, u sqlcgen.User, ev sqlcgen.BarrelEvent, reason string) error {
	caused, err := q.ListBarrelEventConsequences(ctx, uuid.NullUUID{UUID: ev.ID, Valid: true})
	if err != nil {
		return err
	}
	ids := []uuid.UUID{ev.ContainerID}
	var moved []sqlcgen.BulkMovement
	if ev.BulkMovementID.Valid {
		mv, err := q.GetBulkMovementForBarrelEvent(ctx, ev.BulkMovementID.UUID)
		if err != nil {
			return err
		}
		moved = append(moved, mv)
	}
	moved = append(moved, caused...)
	for _, mv := range moved {
		// A move between our own vessels is on no return. A loss, a
		// correction and an adoption are: they have one end.
		if !mv.SourceContainerID.Valid || !mv.DestinationContainerID.Valid {
			if err := assertDateNotInLockedPeriod(ctx, q, zone.Date(ctx, mv.OccurredAt.Time)); err != nil {
				return err
			}
		}
		for _, side := range []uuid.NullUUID{mv.DestinationContainerID, mv.SourceContainerID} {
			if side.Valid {
				ids = append(ids, side.UUID)
			}
		}
	}
	locked, err := lockContainers(ctx, q, ids...)
	if err != nil {
		return err
	}
	cask := locked[ev.ContainerID]
	recorded := ev.PriorVolumeL.Valid && ev.PriorLaa.Valid
	prior := balance{volumeL: ev.PriorVolumeL.Float64, laa: ev.PriorLaa.Float64}

	later, err := q.LaterBarrelEventExists(ctx, sqlcgen.LaterBarrelEventExistsParams{
		ContainerID: ev.ContainerID, EventID: ev.ID, CreatedAt: ev.CreatedAt,
	})
	if err != nil {
		return err
	}

	switch ev.Kind {
	case sqlcgen.BarrelEventKindRegauge:
		if !recorded {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New(
				"this regauge was recorded before Stillhouse kept what the cask held; record a corrective regauge instead"))
		}
		if later {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s has been filled, dumped or regauged since; void the later event first", cask.Name))
		}
		if _, err := saveBalance(ctx, q, cask.ID, prior); err != nil {
			return err
		}
	case sqlcgen.BarrelEventKindDump:
		// Into the cask goes what it held, whatever the dump said it kept.
		mv := moved[0]
		if !recorded {
			prior = balance{volumeL: mv.VolumeL, laa: mv.Laa}
		}
		dst := locked[mv.DestinationContainerID.UUID]
		rest, err := balanceOf(dst).takeBack(dst.Name, mv.VolumeL, mv.Laa)
		if err != nil {
			return err
		}
		if _, err := saveBalance(ctx, q, dst.ID, rest); err != nil {
			return err
		}
		if _, err := saveBalance(ctx, q, cask.ID, balanceOf(cask).add(prior.volumeL, prior.laa)); err != nil {
			return err
		}
	case sqlcgen.BarrelEventKindFill:
		if len(moved) == 0 {
			break
		}
		mv := moved[0]
		rest, err := balanceOf(cask).takeBack(cask.Name, mv.VolumeL, mv.Laa)
		if err != nil {
			return err
		}
		if _, err := saveBalance(ctx, q, cask.ID, rest); err != nil {
			return err
		}
		if mv.SourceContainerID.Valid {
			src := locked[mv.SourceContainerID.UUID]
			back := balanceOf(src).add(mv.VolumeL, mv.Laa)
			for _, c := range caused {
				if c.DestinationContainerID.Valid {
					back.laa -= c.Laa
				} else {
					back.laa += c.Laa
				}
			}
			if _, err := saveBalance(ctx, q, src.ID, back); err != nil {
				return err
			}
		}
	}

	for _, mv := range moved {
		if _, err := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      mv.DestinationContainerID,
			DestinationContainerID: mv.SourceContainerID,
			VolumeL:                mv.VolumeL,
			AbvPct:                 mv.AbvPct,
			Laa:                    mv.Laa,
			Reason:                 sqlcgen.BulkMovementReasonRegaugeCorrection,
			ReferenceType:          "barrel_event_void",
			ReferenceID:            uuid.NullUUID{UUID: ev.ID, Valid: true},
			Notes:                  "void of barrel event " + string(ev.Kind) + ": " + reason,
			OccurredAt:             mv.OccurredAt,
		}); err != nil {
			return err
		}
		if mv.ReferenceType == "inventory_adjustment" {
			if err := q.VoidAdjustmentByMovement(ctx, uuid.NullUUID{UUID: mv.ID, Valid: true}); err != nil {
				return err
			}
		}
	}
	if ev.Kind == sqlcgen.BarrelEventKindRegauge || later {
		return nil
	}

	// The clock goes back with the last thing that set it.
	clock := ev.PriorFillDate
	if !recorded && ev.Kind == sqlcgen.BarrelEventKindDump {
		attrs, err := q.GetBarrelAttributes(ctx, ev.ContainerID)
		if err != nil {
			return err
		}
		if attrs.DaysAgedAtDump.Valid {
			day := zone.Day(zone.From(ctx), ev.EventDate.Time).AddDate(0, 0, -int(attrs.DaysAgedAtDump.Int32))
			clock = pgtype.Date{Valid: true, Time: day}
		}
	}
	if !recorded && ev.Kind == sqlcgen.BarrelEventKindFill {
		now, err := q.GetBulkContainer(ctx, ev.ContainerID)
		if err != nil || now.CurrentVolumeL > balanceEps {
			return err
		}
	}
	return q.RestoreBarrelClock(ctx, sqlcgen.RestoreBarrelClockParams{ContainerID: ev.ContainerID, FillDate: clock})
}

func barrelToProto(c sqlcgen.BulkContainer, a sqlcgen.BarrelAttribute, today time.Time) *stillhousev1.Barrel {
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
		days := int32(today.Sub(a.FillDate.Time).Hours() / 24)
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

func barrelRowToProto(r sqlcgen.ListBarrelsRow, today time.Time) *stillhousev1.Barrel {
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
	out := barrelToProto(container, attrs, today)
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
	if e.VoidedAt.Valid {
		out.VoidedAt = timestamppb.New(e.VoidedAt.Time)
		out.VoidedReason = e.VoidedReason
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
