package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type FermentationService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewFermentationService(db *tenantdb.DB, logger *slog.Logger) *FermentationService {
	return &FermentationService{db: db, logger: logger}
}

func (s *FermentationService) CreateFermentationRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateFermentationRunRequest],
) (*connect.Response[stillhousev1.CreateFermentationRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	mashID, err := uuid.Parse(in.GetMashRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid mash_run_id"))
	}
	if in.GetFermenterLabel() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fermenter_label is required"))
	}
	pitch := timestampOrNow(in.GetPitchAt())
	var yeastID uuid.NullUUID
	if s := in.GetYeastMaterialId(); s != "" {
		id, e := uuid.Parse(s)
		if e != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid yeast_material_id"))
		}
		yeastID = uuid.NullUUID{UUID: id, Valid: true}
	}

	var run sqlcgen.FermentationRun
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if _, e := q.GetMashRun(ctx, mashID); e != nil {
			return e
		}
		var e error
		run, e = q.CreateFermentationRun(ctx, sqlcgen.CreateFermentationRunParams{
			TenantID:            u.TenantID,
			MashRunID:           mashID,
			FermenterLabel:      in.GetFermenterLabel(),
			YeastMaterialID:     yeastID,
			YeastNotes:          in.GetYeastNotes(),
			PitchAt:             pitch,
			TargetFinalGravity:  optionalFloat(in.GetTargetFinalGravitySet(), in.GetTargetFinalGravity()),
			InitialVolumeL:      optionalFloat(in.GetInitialVolumeLSet(), in.GetInitialVolumeL()),
			Status:              sqlcgen.FermentationStatusPitched,
			Notes:               in.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "fermentation_run", run.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"mash_run_id":     mashID.String(),
				"fermenter_label": run.FermenterLabel,
				"initial_volume":  in.GetInitialVolumeL(),
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("mash run not found"))
		}
		s.logger.Error("CreateFermentationRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateFermentationRunResponse{
		Run: fermentationRunToProto(run, 0, "", "", nil, nil, nil),
	}), nil
}

func (s *FermentationService) GetFermentationRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetFermentationRunRequest],
) (*connect.Response[stillhousev1.GetFermentationRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var (
		run        sqlcgen.FermentationRun
		mash       sqlcgen.MashRun
		recipeName string
		logs       []sqlcgen.FermentationLog
		mashMetrics []sqlcgen.MashMetric
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		run, e = q.GetFermentationRun(ctx, id)
		if e != nil {
			return e
		}
		mash, e = q.GetMashRun(ctx, run.MashRunID)
		if e != nil {
			return e
		}
		rv, e := q.GetRecipeVersion(ctx, mash.RecipeVersionID)
		if e != nil {
			return e
		}
		r, e := q.GetRecipe(ctx, rv.RecipeID)
		if e != nil {
			return e
		}
		recipeName = r.Name
		logs, e = q.ListFermentationLogs(ctx, id)
		if e != nil {
			return e
		}
		mashMetrics, e = q.ListMashMetrics(ctx, mash.ID)
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("fermentation run not found"))
		}
		s.logger.Error("GetFermentationRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	og := findLatestOG(mashMetrics)
	return connect.NewResponse(&stillhousev1.GetFermentationRunResponse{
		Run: fermentationRunToProto(run, mash.MashNo, formatDate(mash.MashDate), recipeName, logs, &og, nil),
	}), nil
}

func (s *FermentationService) ListFermentationRuns(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListFermentationRunsRequest],
) (*connect.Response[stillhousev1.ListFermentationRunsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	out := &stillhousev1.ListFermentationRunsResponse{}
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if mid := req.Msg.GetMashRunId(); mid != "" {
			mashID, e := uuid.Parse(mid)
			if e != nil {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid mash_run_id"))
			}
			runs, e := q.ListFermentationRunsByMash(ctx, mashID)
			if e != nil {
				return e
			}
			for _, r := range runs {
				out.Runs = append(out.Runs, fermentationRunToProto(r, 0, "", "", nil, nil, nil))
			}
			return nil
		}

		var statusArg sqlcgen.NullFermentationStatus
		if req.Msg.GetStatus() != stillhousev1.FermentationStatus_FERMENTATION_STATUS_UNSPECIFIED {
			st, e := fermentationStatusToDB(req.Msg.GetStatus())
			if e != nil {
				return connect.NewError(connect.CodeInvalidArgument, e)
			}
			statusArg = sqlcgen.NullFermentationStatus{FermentationStatus: st, Valid: true}
		}
		rows, e := q.ListFermentationRuns(ctx, statusArg)
		if e != nil {
			return e
		}
		for _, r := range rows {
			run := sqlcgen.FermentationRun{
				ID:                 r.ID,
				TenantID:           r.TenantID,
				MashRunID:          r.MashRunID,
				FermenterLabel:     r.FermenterLabel,
				YeastMaterialID:    r.YeastMaterialID,
				YeastNotes:         r.YeastNotes,
				PitchAt:            r.PitchAt,
				TargetFinalGravity: r.TargetFinalGravity,
				InitialVolumeL:     r.InitialVolumeL,
				Status:             r.Status,
				Notes:              r.Notes,
				CreatedAt:          r.CreatedAt,
				UpdatedAt:          r.UpdatedAt,
			}
			out.Runs = append(out.Runs, fermentationRunToProto(run, r.MashNo, formatDate(r.MashDate), r.RecipeName, nil, nil, nil))
		}
		return nil
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		s.logger.Error("ListFermentationRuns", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

func (s *FermentationService) UpdateFermentationStatus(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateFermentationStatusRequest],
) (*connect.Response[stillhousev1.UpdateFermentationStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	st, err := fermentationStatusToDB(req.Msg.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var run sqlcgen.FermentationRun
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		run, e = q.UpdateFermentationStatus(ctx, sqlcgen.UpdateFermentationStatusParams{ID: id, Status: st})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "fermentation_run", run.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":  "status_changed",
				"status": string(st),
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("fermentation run not found"))
		}
		s.logger.Error("UpdateFermentationStatus", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateFermentationStatusResponse{
		Run: fermentationRunToProto(run, 0, "", "", nil, nil, nil),
	}), nil
}

func (s *FermentationService) AddFermentationLog(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddFermentationLogRequest],
) (*connect.Response[stillhousev1.AddFermentationLogResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runID, err := uuid.Parse(req.Msg.GetFermentationRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid fermentation_run_id"))
	}
	observed := timestampOrNow(req.Msg.GetObservedAt())

	var inserted sqlcgen.FermentationLog
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		inserted, e = q.AddFermentationLog(ctx, sqlcgen.AddFermentationLogParams{
			TenantID:          u.TenantID,
			FermentationRunID: runID,
			ObservedAt:        observed,
			SpecificGravity:   optionalFloat(req.Msg.GetSpecificGravitySet(), req.Msg.GetSpecificGravity()),
			Ph:                optionalFloat(req.Msg.GetPhSet(), req.Msg.GetPh()),
			TemperatureC:      optionalFloat(req.Msg.GetTemperatureCSet(), req.Msg.GetTemperatureC()),
			Notes:             req.Msg.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "fermentation_log", inserted.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"fermentation_run_id": runID.String(),
				"sg":                  req.Msg.GetSpecificGravity(),
				"ph":                  req.Msg.GetPh(),
				"temp_c":              req.Msg.GetTemperatureC(),
			})
	})
	if err != nil {
		s.logger.Error("AddFermentationLog", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.AddFermentationLogResponse{Log: fermentationLogToProto(inserted)}), nil
}

// --- helpers ---

func fermentationRunToProto(
	r sqlcgen.FermentationRun,
	mashNo int32,
	mashDate string,
	recipeName string,
	logs []sqlcgen.FermentationLog,
	og *float64,
	_ any,
) *stillhousev1.FermentationRun {
	out := &stillhousev1.FermentationRun{
		Id:             r.ID.String(),
		TenantId:       r.TenantID.String(),
		MashRunId:      r.MashRunID.String(),
		FermenterLabel: r.FermenterLabel,
		YeastNotes:     r.YeastNotes,
		PitchAt:        timestamppb.New(r.PitchAt.Time),
		Status:         fermentationStatusToProto(r.Status),
		Notes:          r.Notes,
		CreatedAt:      timestamppb.New(r.CreatedAt.Time),
		UpdatedAt:      timestamppb.New(r.UpdatedAt.Time),
		MashNo:         mashNo,
		MashDate:       mashDate,
		RecipeName:     recipeName,
	}
	if r.YeastMaterialID.Valid {
		out.YeastMaterialId = r.YeastMaterialID.UUID.String()
	}
	if r.TargetFinalGravity.Valid {
		out.TargetFinalGravity = r.TargetFinalGravity.Float64
		out.TargetFinalGravitySet = true
	}
	if r.InitialVolumeL.Valid {
		out.InitialVolumeL = r.InitialVolumeL.Float64
		out.InitialVolumeLSet = true
	}
	for _, l := range logs {
		out.Logs = append(out.Logs, fermentationLogToProto(l))
	}
	// Latest gravity from logs (logs come ordered ascending by observed_at).
	for i := len(logs) - 1; i >= 0; i-- {
		if logs[i].SpecificGravity.Valid {
			out.CurrentSpecificGravity = logs[i].SpecificGravity.Float64
			out.CurrentSpecificGravitySet = true
			break
		}
	}
	// ABV estimate: (OG − current SG) × 131.25.
	if og != nil && *og > 0 && out.CurrentSpecificGravitySet {
		abv := (*og - out.CurrentSpecificGravity) * 131.25
		if abv < 0 {
			abv = 0
		}
		out.CalculatedAbvPct = round2(abv)
		out.CalculatedAbvPctSet = true
	}
	return out
}

func fermentationLogToProto(l sqlcgen.FermentationLog) *stillhousev1.FermentationLog {
	out := &stillhousev1.FermentationLog{
		Id:                l.ID.String(),
		FermentationRunId: l.FermentationRunID.String(),
		ObservedAt:        timestamppb.New(l.ObservedAt.Time),
		Notes:             l.Notes,
	}
	if l.SpecificGravity.Valid {
		out.SpecificGravity = l.SpecificGravity.Float64
		out.SpecificGravitySet = true
	}
	if l.Ph.Valid {
		out.Ph = l.Ph.Float64
		out.PhSet = true
	}
	if l.TemperatureC.Valid {
		out.TemperatureC = l.TemperatureC.Float64
		out.TemperatureCSet = true
	}
	return out
}

func fermentationStatusToDB(s stillhousev1.FermentationStatus) (sqlcgen.FermentationStatus, error) {
	switch s {
	case stillhousev1.FermentationStatus_FERMENTATION_STATUS_PITCHED:
		return sqlcgen.FermentationStatusPitched, nil
	case stillhousev1.FermentationStatus_FERMENTATION_STATUS_ACTIVE:
		return sqlcgen.FermentationStatusActive, nil
	case stillhousev1.FermentationStatus_FERMENTATION_STATUS_FINISHED:
		return sqlcgen.FermentationStatusFinished, nil
	case stillhousev1.FermentationStatus_FERMENTATION_STATUS_DISTILLED:
		return sqlcgen.FermentationStatusDistilled, nil
	case stillhousev1.FermentationStatus_FERMENTATION_STATUS_CANCELLED:
		return sqlcgen.FermentationStatusCancelled, nil
	}
	return "", errors.New("invalid fermentation status")
}

func fermentationStatusToProto(s sqlcgen.FermentationStatus) stillhousev1.FermentationStatus {
	switch s {
	case sqlcgen.FermentationStatusPitched:
		return stillhousev1.FermentationStatus_FERMENTATION_STATUS_PITCHED
	case sqlcgen.FermentationStatusActive:
		return stillhousev1.FermentationStatus_FERMENTATION_STATUS_ACTIVE
	case sqlcgen.FermentationStatusFinished:
		return stillhousev1.FermentationStatus_FERMENTATION_STATUS_FINISHED
	case sqlcgen.FermentationStatusDistilled:
		return stillhousev1.FermentationStatus_FERMENTATION_STATUS_DISTILLED
	case sqlcgen.FermentationStatusCancelled:
		return stillhousev1.FermentationStatus_FERMENTATION_STATUS_CANCELLED
	}
	return stillhousev1.FermentationStatus_FERMENTATION_STATUS_UNSPECIFIED
}

func findLatestOG(metrics []sqlcgen.MashMetric) float64 {
	for i := len(metrics) - 1; i >= 0; i-- {
		if metrics[i].Kind == sqlcgen.MashMetricKindOriginalGravity {
			return metrics[i].Value
		}
	}
	return 0
}

func round2(x float64) float64 {
	// Local to rpc package; intentionally duplicates distilling.round2 to avoid
	// pulling a runtime dep into a tiny helper.
	return float64(int(x*100+0.5)) / 100
}
