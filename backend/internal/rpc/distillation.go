package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/distilling"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type DistillationService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewDistillationService(db *tenantdb.DB, logger *slog.Logger) *DistillationService {
	return &DistillationService{db: db, logger: logger}
}

func (s *DistillationService) CreateDistillationRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateDistillationRunRequest],
) (*connect.Response[stillhousev1.CreateDistillationRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runDate, err := parseDateOrToday(req.Msg.GetRunDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var r sqlcgen.DistillationRun
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := q.LockDocumentSequence(ctx, "distillation_runs"); e != nil {
			return e
		}
		next, e := q.NextDistillationRunNo(ctx)
		if e != nil {
			return e
		}
		r, e = q.CreateDistillationRun(ctx, sqlcgen.CreateDistillationRunParams{
			TenantID:   u.TenantID,
			RunNo:      next,
			StillLabel: req.Msg.GetStillLabel(),
			RunDate:    runDate,
			Status:     sqlcgen.DistillationStatusPlanned,
			Notes:      req.Msg.GetNotes(),
		})
		return e
	})
	if err != nil {
		s.logger.Error("CreateDistillationRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateDistillationRunResponse{
		Run: distillationRunToProto(r, nil, nil, nil),
	}), nil
}

func (s *DistillationService) GetDistillationRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetDistillationRunRequest],
) (*connect.Response[stillhousev1.GetDistillationRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var (
		r        sqlcgen.DistillationRun
		charges  []sqlcgen.ListDistillationChargesRow
		cuts     []sqlcgen.DistillationCut
		gauge    *sqlcgen.ProductionGauge
		destName string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		r, e = q.GetDistillationRun(ctx, id)
		if e != nil {
			return e
		}
		charges, e = q.ListDistillationCharges(ctx, id)
		if e != nil {
			return e
		}
		cuts, e = q.ListDistillationCuts(ctx, id)
		if e != nil {
			return e
		}
		g, ge := q.GetProductionGaugeByRun(ctx, id)
		if ge == nil {
			gauge = &g
			if c, ce := q.GetBulkContainer(ctx, g.DestinationContainerID); ce == nil {
				destName = c.Name
			}
		} else if !errors.Is(ge, pgx.ErrNoRows) {
			return ge
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("distillation run not found"))
		}
		s.logger.Error("GetDistillationRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	var gaugeProto *stillhousev1.ProductionGauge
	if gauge != nil {
		gaugeProto = productionGaugeToProto(*gauge, destName)
	}
	return connect.NewResponse(&stillhousev1.GetDistillationRunResponse{
		Run: distillationRunToProto(r, charges, cuts, gaugeProto),
	}), nil
}

func (s *DistillationService) ListDistillationRuns(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListDistillationRunsRequest],
) (*connect.Response[stillhousev1.ListDistillationRunsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var statusArg sqlcgen.NullDistillationStatus
	if req.Msg.GetStatus() != stillhousev1.DistillationStatus_DISTILLATION_STATUS_UNSPECIFIED {
		st, err := distillationStatusToDB(req.Msg.GetStatus())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		statusArg = sqlcgen.NullDistillationStatus{DistillationStatus: st, Valid: true}
	}
	var rows []sqlcgen.DistillationRun
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListDistillationRuns(ctx, statusArg)
		return e
	})
	if err != nil {
		s.logger.Error("ListDistillationRuns", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.DistillationRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, distillationRunToProto(r, nil, nil, nil))
	}
	return connect.NewResponse(&stillhousev1.ListDistillationRunsResponse{Runs: out}), nil
}

func (s *DistillationService) UpdateDistillationStatus(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateDistillationStatusRequest],
) (*connect.Response[stillhousev1.UpdateDistillationStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	st, err := distillationStatusToDB(req.Msg.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var r sqlcgen.DistillationRun
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		r, e = q.UpdateDistillationStatus(ctx, sqlcgen.UpdateDistillationStatusParams{ID: id, Status: st})
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("distillation run not found"))
		}
		s.logger.Error("UpdateDistillationStatus", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateDistillationStatusResponse{
		Run: distillationRunToProto(r, nil, nil, nil),
	}), nil
}

func (s *DistillationService) AddDistillationCharge(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddDistillationChargeRequest],
) (*connect.Response[stillhousev1.AddDistillationChargeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runID, err := uuid.Parse(req.Msg.GetDistillationRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid distillation_run_id"))
	}
	fermID, err := uuid.Parse(req.Msg.GetFermentationRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid fermentation_run_id"))
	}
	if req.Msg.GetVolumeChargedL() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("volume_charged_l must be > 0"))
	}
	order := req.Msg.GetChargeOrder()
	if order == 0 {
		order = 1
	}
	var c sqlcgen.DistillationCharge
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		c, e = q.AddDistillationCharge(ctx, sqlcgen.AddDistillationChargeParams{
			TenantID:          u.TenantID,
			DistillationRunID: runID,
			FermentationRunID: fermID,
			VolumeChargedL:    req.Msg.GetVolumeChargedL(),
			AbvPct:            req.Msg.GetAbvPct(),
			ChargeOrder:       order,
			Notes:             req.Msg.GetNotes(),
		})
		return e
	})
	if err != nil {
		// A repeated fermenter, or a run/fermentation id that doesn't
		// exist, is the caller's mistake and has a useful answer. Only a
		// genuinely unrecognised failure becomes a 500.
		if ce := classifyWriteErr(err, "distillation run or fermentation run not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("AddDistillationCharge", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.AddDistillationChargeResponse{
		Charge: distillationChargeToProto(c, "", 0),
	}), nil
}

func (s *DistillationService) AddDistillationCut(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddDistillationCutRequest],
) (*connect.Response[stillhousev1.AddDistillationCutResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runID, err := uuid.Parse(req.Msg.GetDistillationRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid distillation_run_id"))
	}
	kind, err := distillationCutKindToDB(req.Msg.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	observed := timestampOrNow(req.Msg.GetObservedAt())
	order := req.Msg.GetCutOrder()
	if order == 0 {
		order = 1
	}
	var c sqlcgen.DistillationCut
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		c, e = q.AddDistillationCut(ctx, sqlcgen.AddDistillationCutParams{
			TenantID:          u.TenantID,
			DistillationRunID: runID,
			Kind:              kind,
			VolumeL:           req.Msg.GetVolumeL(),
			AbvPct:            req.Msg.GetAbvPct(),
			CutOrder:          order,
			ObservedAt:        observed,
			Notes:             req.Msg.GetNotes(),
		})
		return e
	})
	if err != nil {
		s.logger.Error("AddDistillationCut", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.AddDistillationCutResponse{Cut: distillationCutToProto(c)}), nil
}

func (s *DistillationService) UpdateDistillationCut(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateDistillationCutRequest],
) (*connect.Response[stillhousev1.UpdateDistillationCutResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	kind, err := distillationCutKindToDB(req.Msg.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	observed := timestampOrNow(req.Msg.GetObservedAt())
	order := req.Msg.GetCutOrder()
	if order == 0 {
		order = 1
	}
	var c sqlcgen.DistillationCut
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		c, e = q.UpdateDistillationCut(ctx, sqlcgen.UpdateDistillationCutParams{
			ID:         id,
			Kind:       kind,
			VolumeL:    req.Msg.GetVolumeL(),
			AbvPct:     req.Msg.GetAbvPct(),
			CutOrder:   order,
			ObservedAt: observed,
			Notes:      req.Msg.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "distillation_cut", c.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"distillation_run_id": c.DistillationRunID.String(),
				"kind":                string(c.Kind),
				"volume_l":            c.VolumeL,
				"abv_pct":             c.AbvPct,
				"cut_order":           c.CutOrder,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("cut not found"))
		}
		s.logger.Error("UpdateDistillationCut", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateDistillationCutResponse{Cut: distillationCutToProto(c)}), nil
}

func (s *DistillationService) DeleteDistillationCut(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteDistillationCutRequest],
) (*connect.Response[stillhousev1.DeleteDistillationCutResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Load before delete so the audit payload has the values that vanished.
		c, e := q.GetDistillationCut(ctx, id)
		if e != nil {
			return e
		}
		if e := q.DeleteDistillationCut(ctx, id); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "distillation_cut", id.String(),
			sqlcgen.AuditActionDelete, map[string]any{
				"distillation_run_id": c.DistillationRunID.String(),
				"kind":                string(c.Kind),
				"volume_l":            c.VolumeL,
				"abv_pct":             c.AbvPct,
				"cut_order":           c.CutOrder,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("cut not found"))
		}
		s.logger.Error("DeleteDistillationCut", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.DeleteDistillationCutResponse{}), nil
}

// RecordProductionGauge is the bridge from operational records to the bulk
// alcohol ledger. It runs in one transaction:
//
//  1. Insert a BulkMovement (reason=production_gauge) into the destination
//     container; that movement is the canonical source of truth for the new
//     alcohol.
//  2. Insert a ProductionGauge row linked to the movement.
//  3. Update the destination container's running balance (volume / weighted
//     ABV / LAA).
//  4. Bump the distillation run's status to GAUGED.
func (s *DistillationService) RecordProductionGauge(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordProductionGaugeRequest],
) (*connect.Response[stillhousev1.RecordProductionGaugeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	runID, err := uuid.Parse(in.GetDistillationRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid distillation_run_id"))
	}
	destID, err := uuid.Parse(in.GetDestinationContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid destination_container_id"))
	}
	if in.GetVolumeL() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("volume_l must be > 0"))
	}
	if in.GetAbvPct() < 0 || in.GetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("abv_pct must be in [0, 100]"))
	}
	// Resolve the reading to 20 °C before anything is written. What the
	// operator measured (volume at tank temperature, hydrometer
	// indication) is not what the B266 is built from — see the
	// alcoholometry package.
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
	gaugeTS := timestampOrNow(in.GetGaugeDate())

	var (
		gauge   sqlcgen.ProductionGauge
		updated sqlcgen.BulkContainer
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: gaugeTS.Time}); e != nil {
			return e
		}
		// 0. Sanity check: run exists, not already gauged.
		run, e := q.GetDistillationRun(ctx, runID)
		if e != nil {
			return e
		}
		if run.Status == sqlcgen.DistillationStatusGauged {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("distillation run already has a production gauge"))
		}
		container, e := q.GetBulkContainerForUpdate(ctx, destID)
		if e != nil {
			return e
		}

		// Everything downstream — the ledger, the container balance, the
		// B266 — works in litres and strength at 20 °C.
		volume := corrected.VolumeL20C
		abv := corrected.StrengthPct20C
		laa := corrected.LAA()

		// 1. Insert the BulkMovement.
		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{Valid: false},
			DestinationContainerID: uuid.NullUUID{UUID: destID, Valid: true},
			VolumeL:                volume,
			AbvPct:                 abv,
			Laa:                    laa,
			Reason:                 sqlcgen.BulkMovementReasonProductionGauge,
			ReferenceType:          "distillation_run",
			ReferenceID:            uuid.NullUUID{UUID: runID, Valid: true},
			Notes:                  in.GetNotes(),
			OccurredAt:             gaugeTS,
		})
		if e != nil {
			return e
		}

		// 2. Insert the ProductionGauge.
		gauge, e = q.CreateProductionGauge(ctx, sqlcgen.CreateProductionGaugeParams{
			TenantID:               u.TenantID,
			DistillationRunID:      runID,
			DestinationContainerID: destID,
			BulkMovementID:         mv.ID,
			GaugeDate:              gaugeTS,
			VolumeL:                volume,
			AbvPct:                 abv,
			TemperatureC:           optionalFloat(in.GetTemperatureCSet(), in.GetTemperatureC()),
			GaugerUserID:           u.ID,
			Notes:                  in.GetNotes(),
			ObservedVolumeL:        optionalFloat(true, in.GetVolumeL()),
			ObservedDensityKgM3:    optionalFloat(in.GetDensityKgM3Set(), in.GetDensityKgM3()),
			VolumeFactorC:          corrected.VolumeFactorC,
			StrengthSource:         strengthSourceToDB(corrected.Source),
		})
		if e != nil {
			return e
		}

		// 3. Update container balance.
		newVol, newABV, newLAA := applyDeposit(container.CurrentVolumeL, container.CurrentAbvPct, volume, abv)
		updated, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             destID,
			CurrentVolumeL: newVol,
			CurrentAbvPct:  newABV,
			CurrentLaa:     newLAA,
		})
		if e != nil {
			return e
		}

		// 4. Advance the run's status.
		if _, e = q.UpdateDistillationStatus(ctx, sqlcgen.UpdateDistillationStatusParams{
			ID:     runID,
			Status: sqlcgen.DistillationStatusGauged,
		}); e != nil {
			return e
		}

		// 5. Audit log — sign by the gauger.
		return audit.Write(ctx, q, u.TenantID, u.ID, "production_gauge", gauge.ID.String(),
			sqlcgen.AuditActionSign, map[string]any{
				"distillation_run_id": runID.String(),
				"destination":         updated.Name,
				"volume_l":            volume,
				"abv_pct":             abv,
				"laa":                 laa,
				"observed_volume_l":   in.GetVolumeL(),
				"volume_factor_c":     corrected.VolumeFactorC,
				"strength_source":     corrected.Source.String(),
			})
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("run or container not found"))
		}
		s.logger.Error("RecordProductionGauge", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	return connect.NewResponse(&stillhousev1.RecordProductionGaugeResponse{
		Gauge:                productionGaugeToProto(gauge, updated.Name),
		DestinationContainer: bulkContainerToProto(updated),
	}), nil
}

// VoidDistillationRun reverses the production gauge: refund the destination
// container's LAA, write an offsetting bulk_movement (regauge_correction with
// ref distillation_void), drop the production_gauge row's effect, and mark
// the run voided.
//
// Bottling runs that already consumed the spirit downstream are NOT blocked
// at this layer — the consequence is that the bottling run's source
// container balance may go negative-ish (the cost rollup query already
// dedupes mashes, but a void-after-bottle is a misuse the operator can
// recover from by voiding the bottling run first). The check matches stage
// 48's pattern: caller is responsible for ordering.
func (s *DistillationService) VoidDistillationRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.VoidDistillationRunRequest],
) (*connect.Response[stillhousev1.VoidDistillationRunResponse], error) {
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

	var voided sqlcgen.DistillationRun
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		existing, e := q.GetDistillationRun(ctx, id)
		if e != nil {
			return e
		}
		if existing.VoidedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("distillation run is already voided"))
		}
		if e := assertDateNotInLockedPeriod(ctx, q, existing.RunDate); e != nil {
			return e
		}

		// If a production gauge exists, reverse its container deposit.
		gauge, ge := q.GetProductionGaugeByRun(ctx, id)
		if ge != nil && !errors.Is(ge, pgx.ErrNoRows) {
			return ge
		}
		if ge == nil {
			container, ce := q.GetBulkContainerForUpdate(ctx, gauge.DestinationContainerID)
			if ce != nil {
				return ce
			}
			newVol := container.CurrentVolumeL - gauge.VolumeL
			if newVol < 0 {
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("destination container balance has dropped below the gauged volume — void downstream movements first"))
			}
			laa := gauge.VolumeL * gauge.AbvPct / 100
			newLAA := container.CurrentLaa - laa
			if newLAA < 0 {
				newLAA = 0
			}
			// ABV stays at whatever blended in afterward; if container empties,
			// drop ABV to NULL (matches CreateBulkContainer initial state).
			var newABV pgtype.Float8
			if newVol > 0 && container.CurrentAbvPct.Valid {
				newABV = container.CurrentAbvPct
			}
			if _, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
				ID:             container.ID,
				CurrentVolumeL: newVol,
				CurrentAbvPct:  newABV,
				CurrentLaa:     newLAA,
			}); e != nil {
				return e
			}
			// Offsetting ledger row so the audit trail is reconstructable.
			if _, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
				TenantID:               u.TenantID,
				SourceContainerID:      uuid.NullUUID{UUID: container.ID, Valid: true},
				DestinationContainerID: uuid.NullUUID{Valid: false},
				VolumeL:                gauge.VolumeL,
				AbvPct:                 gauge.AbvPct,
				Laa:                    laa,
				Reason:                 sqlcgen.BulkMovementReasonRegaugeCorrection,
				ReferenceType:          "distillation_run_void",
				ReferenceID:            uuid.NullUUID{UUID: id, Valid: true},
				Notes:                  "void of distillation run #" + fmt.Sprintf("%d", existing.RunNo) + ": " + reason,
				OccurredAt:             pgtype.Timestamptz{Valid: true, Time: time.Now()},
			}); e != nil {
				return e
			}
		}

		voided, e = q.VoidDistillationRun(ctx, sqlcgen.VoidDistillationRunParams{
			ID:           id,
			VoidedBy:     uuid.NullUUID{UUID: u.ID, Valid: true},
			VoidedReason: reason,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "distillation_run", voided.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":     "voided",
				"run_no":    voided.RunNo,
				"reason":    reason,
				"had_gauge": ge == nil,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("distillation run not found"))
		}
		s.logger.Error("VoidDistillationRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.VoidDistillationRunResponse{
		Run: distillationRunToProto(voided, nil, nil, nil),
	}), nil
}

// --- converters ---

func distillationRunToProto(
	r sqlcgen.DistillationRun,
	charges []sqlcgen.ListDistillationChargesRow,
	cuts []sqlcgen.DistillationCut,
	gauge *stillhousev1.ProductionGauge,
) *stillhousev1.DistillationRun {
	out := &stillhousev1.DistillationRun{
		Id:           r.ID.String(),
		TenantId:     r.TenantID.String(),
		RunNo:        r.RunNo,
		StillLabel:   r.StillLabel,
		RunDate:      formatDate(r.RunDate),
		Status:       distillationStatusToProto(r.Status),
		Notes:        r.Notes,
		CreatedAt:    timestamppb.New(r.CreatedAt.Time),
		UpdatedAt:    timestamppb.New(r.UpdatedAt.Time),
		Gauge:        gauge,
		VoidedReason: r.VoidedReason,
	}
	if r.VoidedAt.Valid {
		out.VoidedAt = timestamppb.New(r.VoidedAt.Time)
	}
	if r.VoidedBy.Valid {
		out.VoidedBy = r.VoidedBy.UUID.String()
	}
	for _, c := range charges {
		ch := sqlcgen.DistillationCharge{
			ID:                c.ID,
			TenantID:          c.TenantID,
			DistillationRunID: c.DistillationRunID,
			FermentationRunID: c.FermentationRunID,
			VolumeChargedL:    c.VolumeChargedL,
			AbvPct:            c.AbvPct,
			ChargeOrder:       c.ChargeOrder,
			Notes:             c.Notes,
			CreatedAt:         c.CreatedAt,
		}
		out.Charges = append(out.Charges, distillationChargeToProto(ch, c.FermenterLabel, c.MashNo))
	}
	for _, c := range cuts {
		p := distillationCutToProto(c)
		out.Cuts = append(out.Cuts, p)
		if c.Kind == sqlcgen.DistillationCutKindHearts {
			out.TotalCutLaa += p.Laa
		}
	}
	out.CutAnalysis = buildCutAnalysis(out.Charges, out.Cuts)
	return out
}

// buildCutAnalysis totals what a run charged against what came off it.
// Derived on every read, so it always reflects the latest edit to a cut.
func buildCutAnalysis(
	charges []*stillhousev1.DistillationCharge,
	cuts []*stillhousev1.DistillationCut,
) *stillhousev1.CutAnalysis {
	if len(cuts) == 0 {
		return nil
	}
	chargeLAA := 0.0
	for _, c := range charges {
		chargeLAA += c.GetLaa()
	}
	in := make([]distilling.Cut, 0, len(cuts))
	for _, c := range cuts {
		in = append(in, distilling.Cut{
			Kind:    cutKindToDomain(c.GetKind()),
			VolumeL: c.GetVolumeL(),
			ABVPct:  c.GetAbvPct(),
			LAA:     c.GetLaa(),
			Order:   int(c.GetCutOrder()),
		})
	}
	a := distilling.AnalyseRun(chargeLAA, in)

	out := &stillhousev1.CutAnalysis{
		ChargeLaa:      round4(a.ChargeLAA),
		CutLaa:         round4(a.CutLAA),
		AccountedPct:   round2(a.AccountedPct),
		HeartsLaa:      round4(a.HeartsLAA),
		HeartsSharePct: round2(a.HeartsSharePct),
		HeartsStartAbv: round2(a.HeartsStartABV),
		HeartsEndAbv:   round2(a.HeartsEndABV),
		HeartsSet:      a.HeartsSet,
	}
	for _, f := range a.Findings {
		out.Findings = append(out.Findings, &stillhousev1.CutFinding{
			Severity: cutSeverityToProto(f.Severity),
			Code:     f.Code,
			Title:    f.Title,
			Detail:   f.Detail,
		})
	}
	return out
}

func cutKindToDomain(k stillhousev1.DistillationCutKind) distilling.CutKind {
	switch k {
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_FORESHOTS:
		return distilling.CutForeshots
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEADS:
		return distilling.CutHeads
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEARTS:
		return distilling.CutHearts
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_TAILS:
		return distilling.CutTails
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_FEINTS_SAVED:
		return distilling.CutFeintsSaved
	default:
		return distilling.CutUnspecified
	}
}

func cutSeverityToProto(s distilling.Severity) stillhousev1.CutFindingSeverity {
	switch s {
	case distilling.SeverityProblem:
		return stillhousev1.CutFindingSeverity_CUT_FINDING_SEVERITY_PROBLEM
	case distilling.SeverityWarning:
		return stillhousev1.CutFindingSeverity_CUT_FINDING_SEVERITY_WARNING
	default:
		return stillhousev1.CutFindingSeverity_CUT_FINDING_SEVERITY_INFO
	}
}

func distillationChargeToProto(c sqlcgen.DistillationCharge, fermenterLabel string, mashNo int32) *stillhousev1.DistillationCharge {
	return &stillhousev1.DistillationCharge{
		Id:                c.ID.String(),
		DistillationRunId: c.DistillationRunID.String(),
		FermentationRunId: c.FermentationRunID.String(),
		FermenterLabel:    fermenterLabel,
		MashNo:            mashNo,
		VolumeChargedL:    c.VolumeChargedL,
		AbvPct:            c.AbvPct,
		Laa:               c.VolumeChargedL * c.AbvPct / 100,
		ChargeOrder:       c.ChargeOrder,
		Notes:             c.Notes,
	}
}

func distillationCutToProto(c sqlcgen.DistillationCut) *stillhousev1.DistillationCut {
	return &stillhousev1.DistillationCut{
		Id:                c.ID.String(),
		DistillationRunId: c.DistillationRunID.String(),
		Kind:              distillationCutKindToProto(c.Kind),
		VolumeL:           c.VolumeL,
		AbvPct:            c.AbvPct,
		Laa:               c.Laa.Float64,
		CutOrder:          c.CutOrder,
		ObservedAt:        timestamppb.New(c.ObservedAt.Time),
		Notes:             c.Notes,
	}
}

func productionGaugeToProto(g sqlcgen.ProductionGauge, destName string) *stillhousev1.ProductionGauge {
	out := &stillhousev1.ProductionGauge{
		Id:                       g.ID.String(),
		DistillationRunId:        g.DistillationRunID.String(),
		DestinationContainerId:   g.DestinationContainerID.String(),
		DestinationContainerName: destName,
		BulkMovementId:           g.BulkMovementID.String(),
		GaugeDate:                timestamppb.New(g.GaugeDate.Time),
		VolumeL:                  g.VolumeL,
		AbvPct:                   g.AbvPct,
		Laa:                      g.Laa.Float64,
		GaugerUserId:             g.GaugerUserID.String(),
		Notes:                    g.Notes,
	}
	if g.TemperatureC.Valid {
		out.TemperatureC = g.TemperatureC.Float64
		out.TemperatureCSet = true
	}
	if g.ObservedVolumeL.Valid {
		out.ObservedVolumeL = g.ObservedVolumeL.Float64
	}
	if g.ObservedDensityKgM3.Valid {
		out.ObservedDensityKgM3 = g.ObservedDensityKgM3.Float64
		out.ObservedDensityKgM3Set = true
	}
	out.VolumeFactorC = g.VolumeFactorC
	out.StrengthSource = strengthSourceToProto(g.StrengthSource)
	return out
}

func distillationStatusToDB(s stillhousev1.DistillationStatus) (sqlcgen.DistillationStatus, error) {
	switch s {
	case stillhousev1.DistillationStatus_DISTILLATION_STATUS_PLANNED:
		return sqlcgen.DistillationStatusPlanned, nil
	case stillhousev1.DistillationStatus_DISTILLATION_STATUS_CHARGING:
		return sqlcgen.DistillationStatusCharging, nil
	case stillhousev1.DistillationStatus_DISTILLATION_STATUS_DISTILLING:
		return sqlcgen.DistillationStatusDistilling, nil
	case stillhousev1.DistillationStatus_DISTILLATION_STATUS_GAUGED:
		return sqlcgen.DistillationStatusGauged, nil
	case stillhousev1.DistillationStatus_DISTILLATION_STATUS_CANCELLED:
		return sqlcgen.DistillationStatusCancelled, nil
	}
	return "", errors.New("invalid distillation status")
}

func distillationStatusToProto(s sqlcgen.DistillationStatus) stillhousev1.DistillationStatus {
	switch s {
	case sqlcgen.DistillationStatusPlanned:
		return stillhousev1.DistillationStatus_DISTILLATION_STATUS_PLANNED
	case sqlcgen.DistillationStatusCharging:
		return stillhousev1.DistillationStatus_DISTILLATION_STATUS_CHARGING
	case sqlcgen.DistillationStatusDistilling:
		return stillhousev1.DistillationStatus_DISTILLATION_STATUS_DISTILLING
	case sqlcgen.DistillationStatusGauged:
		return stillhousev1.DistillationStatus_DISTILLATION_STATUS_GAUGED
	case sqlcgen.DistillationStatusCancelled:
		return stillhousev1.DistillationStatus_DISTILLATION_STATUS_CANCELLED
	}
	return stillhousev1.DistillationStatus_DISTILLATION_STATUS_UNSPECIFIED
}

func distillationCutKindToDB(k stillhousev1.DistillationCutKind) (sqlcgen.DistillationCutKind, error) {
	switch k {
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_FORESHOTS:
		return sqlcgen.DistillationCutKindForeshots, nil
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEADS:
		return sqlcgen.DistillationCutKindHeads, nil
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEARTS:
		return sqlcgen.DistillationCutKindHearts, nil
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_TAILS:
		return sqlcgen.DistillationCutKindTails, nil
	case stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_FEINTS_SAVED:
		return sqlcgen.DistillationCutKindFeintsSaved, nil
	}
	return "", errors.New("invalid distillation cut kind")
}

func distillationCutKindToProto(k sqlcgen.DistillationCutKind) stillhousev1.DistillationCutKind {
	switch k {
	case sqlcgen.DistillationCutKindForeshots:
		return stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_FORESHOTS
	case sqlcgen.DistillationCutKindHeads:
		return stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEADS
	case sqlcgen.DistillationCutKindHearts:
		return stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_HEARTS
	case sqlcgen.DistillationCutKindTails:
		return stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_TAILS
	case sqlcgen.DistillationCutKindFeintsSaved:
		return stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_FEINTS_SAVED
	}
	return stillhousev1.DistillationCutKind_DISTILLATION_CUT_KIND_UNSPECIFIED
}
