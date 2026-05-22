package rpc

import (
	"context"
	"errors"
	"log/slog"
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

type MashService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewMashService(db *tenantdb.DB, logger *slog.Logger) *MashService {
	return &MashService{db: db, logger: logger}
}

func (s *MashService) CreateMashRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateMashRunRequest],
) (*connect.Response[stillhousev1.CreateMashRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	rvID, err := uuid.Parse(req.Msg.GetRecipeVersionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_version_id"))
	}

	mashDate, err := parseDateOrToday(req.Msg.GetMashDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var created sqlcgen.MashRun
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if _, e := q.GetRecipeVersion(ctx, rvID); e != nil {
			return e
		}
		nextNo, e := q.NextMashNo(ctx)
		if e != nil {
			return e
		}
		created, e = q.CreateMashRun(ctx, sqlcgen.CreateMashRunParams{
			TenantID:        u.TenantID,
			RecipeVersionID: rvID,
			MashNo:          nextNo,
			MashDate:        mashDate,
			Status:          sqlcgen.MashStatusPlanned,
			Notes:           req.Msg.GetNotes(),
		})
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("recipe version not found"))
		}
		s.logger.Error("CreateMashRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateMashRunResponse{
		MashRun: mashRunToProto(created, "", 0, nil, nil),
	}), nil
}

func (s *MashService) GetMashRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetMashRunRequest],
) (*connect.Response[stillhousev1.GetMashRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var (
		mash      sqlcgen.MashRun
		ingredients []sqlcgen.ListMashIngredientsRow
		metrics   []sqlcgen.MashMetric
		recipeName string
		versionNo  int32
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		mash, e = q.GetMashRun(ctx, id)
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
		versionNo = rv.VersionNo
		ingredients, e = q.ListMashIngredients(ctx, id)
		if e != nil {
			return e
		}
		metrics, e = q.ListMashMetrics(ctx, id)
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("mash run not found"))
		}
		s.logger.Error("GetMashRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetMashRunResponse{
		MashRun: mashRunToProto(mash, recipeName, versionNo, ingredients, metrics),
	}), nil
}

func (s *MashService) ListMashRuns(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListMashRunsRequest],
) (*connect.Response[stillhousev1.ListMashRunsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	params := sqlcgen.ListMashRunsParams{}
	if id := req.Msg.GetRecipeId(); id != "" {
		rid, err := uuid.Parse(id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_id"))
		}
		params.RecipeID = uuid.NullUUID{UUID: rid, Valid: true}
	}
	if req.Msg.GetStatus() != stillhousev1.MashStatus_MASH_STATUS_UNSPECIFIED {
		status, err := mashStatusToDB(req.Msg.GetStatus())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		params.Status = sqlcgen.NullMashStatus{MashStatus: status, Valid: true}
	}

	var rows []sqlcgen.ListMashRunsRow
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListMashRuns(ctx, params)
		return e
	})
	if err != nil {
		s.logger.Error("ListMashRuns", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.MashRun, 0, len(rows))
	for _, r := range rows {
		// Map ListMashRunsRow into a MashRun struct for the converter.
		mash := sqlcgen.MashRun{
			ID:              r.ID,
			TenantID:        r.TenantID,
			RecipeVersionID: r.RecipeVersionID,
			MashNo:          r.MashNo,
			MashDate:        r.MashDate,
			Status:          r.Status,
			Notes:           r.Notes,
			CreatedAt:       r.CreatedAt,
			UpdatedAt:       r.UpdatedAt,
		}
		out = append(out, mashRunToProto(mash, r.RecipeName, r.RecipeVersionNo, nil, nil))
	}
	return connect.NewResponse(&stillhousev1.ListMashRunsResponse{MashRuns: out}), nil
}

func (s *MashService) UpdateMashStatus(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateMashStatusRequest],
) (*connect.Response[stillhousev1.UpdateMashStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	status, err := mashStatusToDB(req.Msg.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var updated sqlcgen.MashRun
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		updated, e = q.UpdateMashStatus(ctx, sqlcgen.UpdateMashStatusParams{ID: id, Status: status})
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("mash run not found"))
		}
		s.logger.Error("UpdateMashStatus", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateMashStatusResponse{
		MashRun: mashRunToProto(updated, "", 0, nil, nil),
	}), nil
}

func (s *MashService) AddMashIngredient(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddMashIngredientRequest],
) (*connect.Response[stillhousev1.AddMashIngredientResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	mashID, err := uuid.Parse(req.Msg.GetMashRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid mash_run_id"))
	}
	matID, err := uuid.Parse(req.Msg.GetMaterialId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid material_id"))
	}
	if req.Msg.GetQuantityUsed() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("quantity_used must be > 0"))
	}
	if req.Msg.GetUom() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("uom is required"))
	}

	var inserted sqlcgen.MashIngredientUsage
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		inserted, e = q.AddMashIngredient(ctx, sqlcgen.AddMashIngredientParams{
			TenantID:     u.TenantID,
			MashRunID:    mashID,
			MaterialID:   matID,
			QuantityUsed: req.Msg.GetQuantityUsed(),
			Uom:          req.Msg.GetUom(),
			Notes:        req.Msg.GetNotes(),
		})
		return e
	})
	if err != nil {
		s.logger.Error("AddMashIngredient", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.AddMashIngredientResponse{
		Usage: mashIngredientUsageToProto(inserted, "", sqlcgen.MaterialKindOther, pgtype.Float8{}),
	}), nil
}

func (s *MashService) AddMashMetric(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddMashMetricRequest],
) (*connect.Response[stillhousev1.AddMashMetricResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	mashID, err := uuid.Parse(req.Msg.GetMashRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid mash_run_id"))
	}
	kind, err := mashMetricKindToDB(req.Msg.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	observed := pgtype.Timestamptz{Valid: true, Time: time.Now()}
	if t := req.Msg.GetObservedAt().AsTime(); !t.IsZero() {
		observed.Time = t
	}

	var inserted sqlcgen.MashMetric
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		inserted, e = q.AddMashMetric(ctx, sqlcgen.AddMashMetricParams{
			TenantID:   u.TenantID,
			MashRunID:  mashID,
			Kind:       kind,
			Value:      req.Msg.GetValue(),
			Unit:       req.Msg.GetUnit(),
			ObservedAt: observed,
			Notes:      req.Msg.GetNotes(),
		})
		return e
	})
	if err != nil {
		s.logger.Error("AddMashMetric", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.AddMashMetricResponse{Metric: mashMetricToProto(inserted)}), nil
}

// --- helpers ---

func parseDateOrToday(s string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{Valid: true, Time: time.Now().UTC()}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}, errors.New("mash_date must be YYYY-MM-DD")
	}
	return pgtype.Date{Valid: true, Time: t}, nil
}

func mashRunToProto(
	m sqlcgen.MashRun,
	recipeName string,
	versionNo int32,
	ingredients []sqlcgen.ListMashIngredientsRow,
	metrics []sqlcgen.MashMetric,
) *stillhousev1.MashRun {
	out := &stillhousev1.MashRun{
		Id:              m.ID.String(),
		TenantId:        m.TenantID.String(),
		RecipeVersionId: m.RecipeVersionID.String(),
		MashNo:          m.MashNo,
		MashDate:        formatDate(m.MashDate),
		Status:          mashStatusToProto(m.Status),
		Notes:           m.Notes,
		CreatedAt:       timestamppb.New(m.CreatedAt.Time),
		UpdatedAt:       timestamppb.New(m.UpdatedAt.Time),
		RecipeName:      recipeName,
		RecipeVersionNo: versionNo,
	}
	for _, ing := range ingredients {
		out.Ingredients = append(out.Ingredients,
			mashIngredientUsageToProto(
				sqlcgen.MashIngredientUsage{
					ID:           ing.ID,
					TenantID:     ing.TenantID,
					MashRunID:    ing.MashRunID,
					MaterialID:   ing.MaterialID,
					QuantityUsed: ing.QuantityUsed,
					Uom:          ing.Uom,
					Notes:        ing.Notes,
					CreatedAt:    ing.CreatedAt,
				},
				ing.MaterialName,
				ing.MaterialKind,
				ing.MaterialExtractPct,
			))
	}
	for _, m := range metrics {
		out.Metrics = append(out.Metrics, mashMetricToProto(m))
	}
	return out
}

func mashIngredientUsageToProto(
	u sqlcgen.MashIngredientUsage,
	materialName string,
	materialKind sqlcgen.MaterialKind,
	extract pgtype.Float8,
) *stillhousev1.MashIngredientUsage {
	out := &stillhousev1.MashIngredientUsage{
		Id:           u.ID.String(),
		MashRunId:    u.MashRunID.String(),
		MaterialId:   u.MaterialID.String(),
		MaterialName: materialName,
		MaterialKind: materialKindToProto(materialKind),
		QuantityUsed: u.QuantityUsed,
		Uom:          u.Uom,
		Notes:        u.Notes,
		CreatedAt:    timestamppb.New(u.CreatedAt.Time),
	}
	if extract.Valid {
		out.MaterialExtractPct = extract.Float64
		out.MaterialExtractPctSet = true
	}
	return out
}

func mashMetricToProto(m sqlcgen.MashMetric) *stillhousev1.MashMetric {
	return &stillhousev1.MashMetric{
		Id:         m.ID.String(),
		MashRunId:  m.MashRunID.String(),
		Kind:       mashMetricKindToProto(m.Kind),
		Value:      m.Value,
		Unit:       m.Unit,
		ObservedAt: timestamppb.New(m.ObservedAt.Time),
		Notes:      m.Notes,
	}
}

func formatDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format("2006-01-02")
}

func mashStatusToDB(s stillhousev1.MashStatus) (sqlcgen.MashStatus, error) {
	switch s {
	case stillhousev1.MashStatus_MASH_STATUS_PLANNED:
		return sqlcgen.MashStatusPlanned, nil
	case stillhousev1.MashStatus_MASH_STATUS_IN_PROGRESS:
		return sqlcgen.MashStatusInProgress, nil
	case stillhousev1.MashStatus_MASH_STATUS_FERMENTING:
		return sqlcgen.MashStatusFermenting, nil
	case stillhousev1.MashStatus_MASH_STATUS_DISTILLED:
		return sqlcgen.MashStatusDistilled, nil
	case stillhousev1.MashStatus_MASH_STATUS_CANCELLED:
		return sqlcgen.MashStatusCancelled, nil
	}
	return "", errors.New("invalid mash status")
}

func mashStatusToProto(s sqlcgen.MashStatus) stillhousev1.MashStatus {
	switch s {
	case sqlcgen.MashStatusPlanned:
		return stillhousev1.MashStatus_MASH_STATUS_PLANNED
	case sqlcgen.MashStatusInProgress:
		return stillhousev1.MashStatus_MASH_STATUS_IN_PROGRESS
	case sqlcgen.MashStatusFermenting:
		return stillhousev1.MashStatus_MASH_STATUS_FERMENTING
	case sqlcgen.MashStatusDistilled:
		return stillhousev1.MashStatus_MASH_STATUS_DISTILLED
	case sqlcgen.MashStatusCancelled:
		return stillhousev1.MashStatus_MASH_STATUS_CANCELLED
	}
	return stillhousev1.MashStatus_MASH_STATUS_UNSPECIFIED
}

func mashMetricKindToDB(k stillhousev1.MashMetricKind) (sqlcgen.MashMetricKind, error) {
	switch k {
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_ORIGINAL_GRAVITY:
		return sqlcgen.MashMetricKindOriginalGravity, nil
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_PH:
		return sqlcgen.MashMetricKindMashPh, nil
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_TEMP_C:
		return sqlcgen.MashMetricKindMashTempC, nil
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_WATER_VOLUME_L:
		return sqlcgen.MashMetricKindWaterVolumeL, nil
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_STRIKE_TEMP_C:
		return sqlcgen.MashMetricKindStrikeTempC, nil
	case stillhousev1.MashMetricKind_MASH_METRIC_KIND_OTHER:
		return sqlcgen.MashMetricKindOther, nil
	}
	return "", errors.New("invalid mash metric kind")
}

func mashMetricKindToProto(k sqlcgen.MashMetricKind) stillhousev1.MashMetricKind {
	switch k {
	case sqlcgen.MashMetricKindOriginalGravity:
		return stillhousev1.MashMetricKind_MASH_METRIC_KIND_ORIGINAL_GRAVITY
	case sqlcgen.MashMetricKindMashPh:
		return stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_PH
	case sqlcgen.MashMetricKindMashTempC:
		return stillhousev1.MashMetricKind_MASH_METRIC_KIND_MASH_TEMP_C
	case sqlcgen.MashMetricKindWaterVolumeL:
		return stillhousev1.MashMetricKind_MASH_METRIC_KIND_WATER_VOLUME_L
	case sqlcgen.MashMetricKindStrikeTempC:
		return stillhousev1.MashMetricKind_MASH_METRIC_KIND_STRIKE_TEMP_C
	case sqlcgen.MashMetricKindOther:
		return stillhousev1.MashMetricKind_MASH_METRIC_KIND_OTHER
	}
	return stillhousev1.MashMetricKind_MASH_METRIC_KIND_UNSPECIFIED
}
