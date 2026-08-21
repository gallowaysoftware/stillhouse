package rpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// Inventory adjustments reconcile a container's book balance to what was
// physically found. Line D on B266 page 3 is a reason-coded entry, and
// before stage 145 Stillhouse had no concept of one:
//
//   - RegaugeBarrel refuses any upward variance outright ("regauges record
//     losses only"), so a cask gauging higher than the book — a mis-keyed
//     fill, an instrument error, a genuine count — had no path.
//   - Tanks could not be reconciled at all; regauge is barrel-only.
//   - A downward variance on a barrel was booked as loss_evaporation
//     whatever caused it, so a counting error and the angels' share landed
//     on the same line. Under EDM3-4-1 they do not have the same duty
//     treatment.
//
// An adjustment is a determination like any other gauge — same 20 °C
// correction, same instrument register (stage 144). What makes it
// different is that it says why, names who, and keeps the book figure
// beside the counted one.

// RecordInventoryAdjustment reconciles one container to a physical count.
func (s *BulkService) RecordInventoryAdjustment(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordInventoryAdjustmentRequest],
) (*connect.Response[stillhousev1.RecordInventoryAdjustmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	containerID, err := uuid.Parse(in.GetContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid container_id"))
	}
	reason, err := adjustmentReasonToDB(in.GetReason())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Always required, not only for "other". A reconciliation entry is
	// read by an auditor asking why the numbers moved, and the reason code
	// alone does not answer that.
	explanation := strings.TrimSpace(in.GetExplanation())
	if explanation == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("an explanation is required: line D is what an auditor reads to find out why the books moved"))
	}
	if in.GetCountedVolumeL() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("counted_volume_l cannot be negative"))
	}
	if in.GetAbvPct() < 0 || in.GetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("abv_pct must be in [0, 100]"))
	}

	// Resolve the count to 20 °C before comparing it with anything. A
	// warm tank gauged against a book figure at 20 °C would show a
	// variance that is entirely the thermometer's.
	counted, err := resolveStrength(strengthInput{
		ObservedVolumeL: in.GetCountedVolumeL(),
		AbvPct:          in.GetAbvPct(),
		DensityKgM3:     in.GetDensityKgM3(),
		DensityIsSet:    in.GetDensityKgM3Set(),
		TemperatureC:    in.GetTemperatureC(),
		TemperatureSet:  in.GetTemperatureCSet(),
	})
	if err != nil {
		return nil, alcoholometryError(err)
	}
	occurredAt := timestampOrNow(in.GetOccurredAt())

	var (
		adjustment sqlcgen.InventoryAdjustment
		container  sqlcgen.BulkContainer
		warnings   []string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: occurredAt.Time}); e != nil {
			return e
		}
		// Read the book balance under a lock: the whole point of the
		// entry is the difference between the book and the count, and a
		// concurrent movement between the read and the write would make
		// the recorded delta describe a balance that never existed.
		locked, e := lockContainers(ctx, q, containerID)
		if e != nil {
			return e
		}
		book := locked[containerID]

		instr, e := checkInstruments(ctx, q, in.GetInstruments(), occurredAt.Time)
		if e != nil {
			return e
		}
		warnings = instr.warnings

		if book.CapacityL.Valid && counted.VolumeL20C > book.CapacityL.Float64+1e-6 {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%.4f L would not fit in %s (%.4f L capacity) — check the measurement",
				counted.VolumeL20C, book.Name, book.CapacityL.Float64))
		}

		deltaLAA := counted.LAA() - book.CurrentLaa
		deltaVol := counted.VolumeL20C - book.CurrentVolumeL

		// The ledger row. An adjustment that confirms the book exactly
		// moves no alcohol and writes none — but the adjustment row is
		// still kept, because a count that found no variance is evidence
		// the count was done.
		var movementID uuid.NullUUID
		if math.Abs(deltaLAA) > 1e-9 || math.Abs(deltaVol) > 1e-9 {
			mvReason := sqlcgen.BulkMovementReasonAdjustmentIncrease
			src, dst := uuid.NullUUID{}, uuid.NullUUID{UUID: containerID, Valid: true}
			vol, laa := deltaVol, deltaLAA
			if deltaLAA < 0 || deltaVol < 0 {
				mvReason = sqlcgen.BulkMovementReasonAdjustmentDecrease
				src, dst = uuid.NullUUID{UUID: containerID, Valid: true}, uuid.NullUUID{}
				vol, laa = -deltaVol, -deltaLAA
			}
			// volume_l CHECK is > 0 and laa CHECK is >= 0, so a
			// strength-only adjustment (same litres, different ABV) needs
			// a positive volume to sit on. The litres involved in the
			// change of strength are the container's own.
			if vol <= 0 {
				vol = math.Max(counted.VolumeL20C, book.CurrentVolumeL)
			}
			mvABV := 0.0
			if vol > 0 {
				mvABV = math.Max(0, math.Min(100, math.Abs(laa)/vol*100))
			}
			mv, me := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
				TenantID:               u.TenantID,
				SourceContainerID:      src,
				DestinationContainerID: dst,
				VolumeL:                vol,
				AbvPct:                 mvABV,
				Laa:                    math.Abs(laa),
				Reason:                 mvReason,
				ReferenceType:          "inventory_adjustment",
				Notes:                  explanation,
				OccurredAt:             occurredAt,
			})
			if me != nil {
				return me
			}
			movementID = uuid.NullUUID{UUID: mv.ID, Valid: true}
		}

		adjustment, e = q.CreateInventoryAdjustment(ctx, sqlcgen.CreateInventoryAdjustmentParams{
			TenantID:        u.TenantID,
			ContainerID:     containerID,
			BulkMovementID:  movementID,
			Reason:          reason,
			Explanation:     explanation,
			BookVolumeL:     book.CurrentVolumeL,
			BookAbvPct:      book.CurrentAbvPct,
			BookLaa:         book.CurrentLaa,
			CountedVolumeL:  counted.VolumeL20C,
			CountedAbvPct:   pgtype.Float8{Float64: counted.StrengthPct20C, Valid: counted.VolumeL20C > 0},
			CountedLaa:      counted.LAA(),
			DeltaLaa:        deltaLAA,
			DeltaVolumeL:    deltaVol,
			TemperatureC:    optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			ObservedVolumeL: optionalFloat(true, in.GetCountedVolumeL()),

			ObservedDensityKgM3:     optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:           counted.VolumeFactorC,
			StrengthSource:          strengthSourceToDB(counted.Source),
			VolumeInstrumentID:      instr.volume,
			StrengthInstrumentID:    instr.strength,
			TemperatureInstrumentID: instr.temperature,
			AdjustedBy:              u.ID,
			Notes:                   in.GetNotes(),
			OccurredAt:              occurredAt,
		})
		if e != nil {
			return e
		}

		// An emptied container loses its strength, matching what every
		// other path does when a vessel goes to zero.
		newABV := pgtype.Float8{Float64: counted.StrengthPct20C, Valid: counted.VolumeL20C > 0}
		container, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             containerID,
			CurrentVolumeL: counted.VolumeL20C,
			CurrentAbvPct:  newABV,
			CurrentLaa:     counted.LAA(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "inventory_adjustment", adjustment.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"container":   book.Name,
				"reason":      string(reason),
				"explanation": explanation,
				"book_laa":    book.CurrentLaa,
				"counted_laa": counted.LAA(),
				"delta_laa":   deltaLAA,
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
		if ce := classifyWriteErr(err, "container not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("RecordInventoryAdjustment", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := inventoryAdjustmentToProto(adjustment, container.Name, u.DisplayName)
	// Resolve the instrument block for the response, so the caller sees the
	// same trail a later read will show.
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		out.Instruments = newInstrumentCache(q, time.Now()).refs(ctx,
			adjustment.VolumeInstrumentID, adjustment.StrengthInstrumentID, adjustment.TemperatureInstrumentID)
		return nil
	}); err != nil {
		s.logger.Warn("RecordInventoryAdjustment: resolve instruments", "err", err)
	}
	return connect.NewResponse(&stillhousev1.RecordInventoryAdjustmentResponse{
		Adjustment: out,
		Container:  bulkContainerToProto(container),
		Warnings:   warnings,
	}), nil
}

func (s *BulkService) ListInventoryAdjustments(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListInventoryAdjustmentsRequest],
) (*connect.Response[stillhousev1.ListInventoryAdjustmentsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	params := sqlcgen.ListInventoryAdjustmentsParams{}
	if id := req.Msg.GetContainerId(); id != "" {
		cid, err := uuid.Parse(id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid container_id"))
		}
		params.ContainerID = uuid.NullUUID{UUID: cid, Valid: true}
	}
	var err error
	if params.PeriodStart, err = optionalDate(req.Msg.GetPeriodStart(), "period_start"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if params.PeriodEnd, err = optionalDate(req.Msg.GetPeriodEnd(), "period_end"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out []*stillhousev1.InventoryAdjustment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListInventoryAdjustments(ctx, params)
		if e != nil {
			return e
		}
		cache := newInstrumentCache(q, time.Now())
		out = make([]*stillhousev1.InventoryAdjustment, 0, len(rows))
		for _, r := range rows {
			p := inventoryAdjustmentToProto(sqlcgen.InventoryAdjustment{
				ID: r.ID, ContainerID: r.ContainerID, BulkMovementID: r.BulkMovementID,
				Reason: r.Reason, Explanation: r.Explanation,
				BookVolumeL: r.BookVolumeL, BookAbvPct: r.BookAbvPct, BookLaa: r.BookLaa,
				CountedVolumeL: r.CountedVolumeL, CountedAbvPct: r.CountedAbvPct, CountedLaa: r.CountedLaa,
				DeltaLaa: r.DeltaLaa, DeltaVolumeL: r.DeltaVolumeL,
				TemperatureC: r.TemperatureC, ObservedVolumeL: r.ObservedVolumeL,
				ObservedDensityKgM3: r.ObservedDensityKgM3, VolumeFactorC: r.VolumeFactorC,
				StrengthSource:     r.StrengthSource,
				VolumeInstrumentID: r.VolumeInstrumentID, StrengthInstrumentID: r.StrengthInstrumentID,
				TemperatureInstrumentID: r.TemperatureInstrumentID,
				AdjustedBy:              r.AdjustedBy, Notes: r.Notes,
				OccurredAt: r.OccurredAt, CreatedAt: r.CreatedAt,
			}, r.ContainerName, r.AdjustedByName)
			p.Instruments = cache.refs(ctx,
				r.VolumeInstrumentID, r.StrengthInstrumentID, r.TemperatureInstrumentID)
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		s.logger.Error("ListInventoryAdjustments", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.ListInventoryAdjustmentsResponse{Adjustments: out}), nil
}

func inventoryAdjustmentToProto(a sqlcgen.InventoryAdjustment, containerName, adjustedByName string) *stillhousev1.InventoryAdjustment {
	out := &stillhousev1.InventoryAdjustment{
		Id:             a.ID.String(),
		ContainerId:    a.ContainerID.String(),
		ContainerName:  containerName,
		Reason:         adjustmentReasonToProto(a.Reason),
		Explanation:    a.Explanation,
		BookVolumeL:    a.BookVolumeL,
		BookLaa:        a.BookLaa,
		CountedVolumeL: a.CountedVolumeL,
		CountedLaa:     a.CountedLaa,
		DeltaLaa:       round4(a.DeltaLaa),
		DeltaVolumeL:   round4(a.DeltaVolumeL),
		VolumeFactorC:  a.VolumeFactorC,
		StrengthSource: strengthSourceToProto(a.StrengthSource),
		AdjustedBy:     a.AdjustedBy.String(),
		AdjustedByName: adjustedByName,
		Notes:          a.Notes,
		OccurredAt:     timestamppb.New(a.OccurredAt.Time),
		CreatedAt:      timestamppb.New(a.CreatedAt.Time),
	}
	if a.BulkMovementID.Valid {
		out.BulkMovementId = a.BulkMovementID.UUID.String()
	}
	if a.BookAbvPct.Valid {
		out.BookAbvPct, out.BookAbvPctSet = a.BookAbvPct.Float64, true
	}
	if a.CountedAbvPct.Valid {
		out.CountedAbvPct, out.CountedAbvPctSet = a.CountedAbvPct.Float64, true
	}
	if a.TemperatureC.Valid {
		out.TemperatureC, out.TemperatureCSet = a.TemperatureC.Float64, true
	}
	if a.ObservedVolumeL.Valid {
		out.ObservedVolumeL = a.ObservedVolumeL.Float64
	}
	if a.ObservedDensityKgM3.Valid {
		out.ObservedDensityKgM3, out.ObservedDensityKgM3Set = a.ObservedDensityKgM3.Float64, true
	}
	return out
}

func adjustmentReasonToDB(r stillhousev1.InventoryAdjustmentReason) (sqlcgen.InventoryAdjustmentReason, error) {
	switch r {
	case stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT:
		return sqlcgen.InventoryAdjustmentReasonPhysicalCount, nil
	case stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_MEASUREMENT_CORRECTION:
		return sqlcgen.InventoryAdjustmentReasonMeasurementCorrection, nil
	case stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_DATA_ENTRY_ERROR:
		return sqlcgen.InventoryAdjustmentReasonDataEntryError, nil
	case stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_OTHER:
		return sqlcgen.InventoryAdjustmentReasonOther, nil
	}
	return "", errors.New("reason is required: line D is reason-coded")
}

func adjustmentReasonToProto(r sqlcgen.InventoryAdjustmentReason) stillhousev1.InventoryAdjustmentReason {
	switch r {
	case sqlcgen.InventoryAdjustmentReasonPhysicalCount:
		return stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_PHYSICAL_COUNT
	case sqlcgen.InventoryAdjustmentReasonMeasurementCorrection:
		return stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_MEASUREMENT_CORRECTION
	case sqlcgen.InventoryAdjustmentReasonDataEntryError:
		return stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_DATA_ENTRY_ERROR
	case sqlcgen.InventoryAdjustmentReasonOther:
		return stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_OTHER
	}
	return stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_UNSPECIFIED
}
