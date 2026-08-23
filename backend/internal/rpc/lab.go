package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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

// LabService records what the lab found and who signed a lot off.
type LabService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewLabService(db *tenantdb.DB, logger *slog.Logger) *LabService {
	return &LabService{db: db, logger: logger}
}

func (s *LabService) RecordLabResult(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordLabResultRequest],
) (*connect.Response[stillhousev1.RecordLabResultResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	analyte := strings.TrimSpace(in.GetAnalyte())
	if analyte == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say what was measured"))
	}

	// Exactly one subject. The database enforces it too, but a clear
	// message beats a constraint violation.
	subjects := map[string]string{
		"container_id":        in.GetContainerId(),
		"production_gauge_id": in.GetProductionGaugeId(),
		"bottling_run_id":     in.GetBottlingRunId(),
		"mash_run_id":         in.GetMashRunId(),
	}
	var named []string
	parsed := map[string]uuid.NullUUID{}
	for field, v := range subjects {
		if v == "" {
			continue
		}
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid %s", field))
		}
		named = append(named, field)
		parsed[field] = uuid.NullUUID{UUID: id, Valid: true}
	}
	switch len(named) {
	case 1:
	case 0:
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("attach the result to a cask, a gauge, a bottling run or a mash"))
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("a result belongs to one thing; this names %d", len(named)))
	}

	status, err := labStatusToDB(in.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// A pass or a fail is a judgement against something. Recording one
	// with no limit and no value is an opinion, and an opinion in a lab
	// register is worse than an informational reading.
	if status != sqlcgen.LabResultStatusInformational && !in.GetValueSet() {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a pass or fail needs the value it was judged on — "+
				"record it as informational if there is no number"))
	}
	sampledOn, err := parseOptionalDate(in.GetSampledOn(), "sampled_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	reportedOn, err := parseDateOrToday(in.GetReportedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("reported_on must be YYYY-MM-DD"))
	}

	var row sqlcgen.LabResult
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.CreateLabResult(ctx, sqlcgen.CreateLabResultParams{
			TenantID:          u.TenantID,
			ContainerID:       parsed["container_id"],
			ProductionGaugeID: parsed["production_gauge_id"],
			BottlingRunID:     parsed["bottling_run_id"],
			MashRunID:         parsed["mash_run_id"],
			Analyte:           analyte,
			Value:             optFloat(in.GetValue(), in.GetValueSet()),
			Uom:               strings.TrimSpace(in.GetUom()),
			SpecMin:           optFloat(in.GetSpecMin(), in.GetSpecMinSet()),
			SpecMax:           optFloat(in.GetSpecMax(), in.GetSpecMaxSet()),
			Status:            status,
			Method:            in.GetMethod(),
			Laboratory:        in.GetLaboratory(),
			Reference:         in.GetReference(),
			SampledOn:         sampledOn,
			ReportedOn:        reportedOn,
			Notes:             in.GetNotes(),
			RecordedBy:        u.ID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "lab_result", row.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"analyte": row.Analyte, "status": string(row.Status),
				"laboratory": row.Laboratory, "reference": row.Reference,
			})
	})
	if err != nil {
		if ce := classifyWriteErr(err, "what this result is attached to no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("RecordLabResult", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RecordLabResultResponse{
		Result: labResultToProto(row, u.DisplayName),
	}), nil
}

func (s *LabService) ListLabResults(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListLabResultsRequest],
) (*connect.Response[stillhousev1.ListLabResultsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	params := sqlcgen.ListLabResultsParams{RowLimit: 100}
	if v := in.GetLimit(); v > 0 && v <= 500 {
		params.RowLimit = v
	}
	for field, target := range map[string]*uuid.NullUUID{
		in.GetContainerId():       &params.ContainerID,
		in.GetBottlingRunId():     &params.BottlingRunID,
		in.GetProductionGaugeId(): &params.ProductionGaugeID,
		in.GetMashRunId():         &params.MashRunID,
	} {
		if field == "" {
			continue
		}
		id, err := uuid.Parse(field)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
		}
		*target = uuid.NullUUID{UUID: id, Valid: true}
	}

	var rows []sqlcgen.ListLabResultsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListLabResults(ctx, params)
		return e
	}); err != nil {
		s.logger.Error("ListLabResults", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.LabResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, labResultToProto(sqlcgen.LabResult{
			ID: r.ID, TenantID: r.TenantID, ContainerID: r.ContainerID,
			ProductionGaugeID: r.ProductionGaugeID, BottlingRunID: r.BottlingRunID,
			MashRunID: r.MashRunID, Analyte: r.Analyte, Value: r.Value, Uom: r.Uom,
			SpecMin: r.SpecMin, SpecMax: r.SpecMax, Status: r.Status,
			Method: r.Method, Laboratory: r.Laboratory, Reference: r.Reference,
			SampledOn: r.SampledOn, ReportedOn: r.ReportedOn, Notes: r.Notes,
			CreatedAt: r.CreatedAt,
		}, r.RecordedByName))
	}
	return connect.NewResponse(&stillhousev1.ListLabResultsResponse{Results: out}), nil
}

// ReleaseLot is a named person saying this stock may go.
//
// Notes are required. "Approved" is not a record of anything; what was
// checked is, and a release with nothing behind it is the shape of
// control that looks like control in an audit and answers nothing in a
// recall.
func (s *LabService) ReleaseLot(
	ctx context.Context,
	req *connect.Request[stillhousev1.ReleaseLotRequest],
) (*connect.Response[stillhousev1.ReleaseLotResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetPackagedInventoryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid packaged_inventory_id"))
	}
	notes := strings.TrimSpace(req.Msg.GetNotes())
	if notes == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say what was checked — a release with nothing behind it answers "+
				"nothing when somebody asks why this lot was let out"))
	}

	var row sqlcgen.PackagedInventory
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		state, e := q.GetPackagedLotReleaseState(ctx, id)
		if e != nil {
			return e
		}
		// A failing result on the run — or on the container it drew from
		// — does not block the release. It is surfaced in the audit
		// payload instead, because overriding a failure is a decision
		// somebody is entitled to make and is exactly the decision that
		// has to be findable afterwards.
		failed, e := q.CountFailedLabResultsForRun(ctx, sqlcgen.CountFailedLabResultsForRunParams{
			BottlingRunID: state.BottlingRunID.UUID,
			ContainerID:   uuid.NullUUID{},
		})
		if e != nil {
			return e
		}
		row, e = q.ReleasePackagedLot(ctx, sqlcgen.ReleasePackagedLotParams{
			ID: id, ReleasedBy: uuid.NullUUID{UUID: u.ID, Valid: true}, ReleaseNotes: notes,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "packaged_inventory", id.String(),
			sqlcgen.AuditActionSign, map[string]any{
				"event": "released", "notes": notes,
				"failing_lab_results": failed,
				"was_held":            state.HeldAt.Valid,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("lot not found"))
		}
		s.logger.Error("ReleaseLot", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.ReleaseLotResponse{
		PackagedInventoryId: id.String(), ReleasedByName: u.DisplayName,
	}
	if row.ReleasedAt.Valid {
		out.ReleasedAt = timestamppb.New(row.ReleasedAt.Time)
	}
	return connect.NewResponse(out), nil
}

// HoldLot stops a lot leaving.
//
// A hold does not clear an earlier release, and it blocks a removal
// whether or not the tenant requires release. Holding is an explicit act
// by a named person saying this stock must not go; honouring it only
// when a setting happens to be on would make the act meaningless. A lot
// held after release is a recall in its early form, and erasing the fact
// that somebody released it would remove the most important part of that
// record.
func (s *LabService) HoldLot(
	ctx context.Context,
	req *connect.Request[stillhousev1.HoldLotRequest],
) (*connect.Response[stillhousev1.HoldLotResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetPackagedInventoryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid packaged_inventory_id"))
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why it is being held"))
	}

	var row sqlcgen.PackagedInventory
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		state, e := q.GetPackagedLotReleaseState(ctx, id)
		if e != nil {
			return e
		}
		row, e = q.HoldPackagedLot(ctx, sqlcgen.HoldPackagedLotParams{
			ID: id, HeldBy: uuid.NullUUID{UUID: u.ID, Valid: true}, HoldReason: reason,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "packaged_inventory", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event": "held", "reason": reason,
				// Whether this is a hold on unreleased stock or a recall
				// of stock already let out. Different events, same act.
				"after_release": state.ReleasedAt.Valid,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("lot not found"))
		}
		s.logger.Error("HoldLot", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.HoldLotResponse{
		PackagedInventoryId: id.String(), HeldByName: u.DisplayName,
	}
	if row.HeldAt.Valid {
		out.HeldAt = timestamppb.New(row.HeldAt.Time)
	}
	return connect.NewResponse(out), nil
}

// SetBatchReleaseRequired turns the gate on or off for the tenant.
func (s *TenantService) SetBatchReleaseRequired(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetBatchReleaseRequiredRequest],
) (*connect.Response[stillhousev1.SetBatchReleaseRequiredResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var row sqlcgen.Tenant
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.SetTenantBatchReleaseRequired(ctx, sqlcgen.SetTenantBatchReleaseRequiredParams{
			ID: u.TenantID, RequireBatchRelease: req.Msg.GetRequired(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "tenant", u.TenantID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event": "batch_release_requirement", "required": row.RequireBatchRelease,
			})
	})
	if err != nil {
		s.logger.Error("SetBatchReleaseRequired", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetBatchReleaseRequiredResponse{
		Required: row.RequireBatchRelease,
	}), nil
}

func optFloat(v float64, set bool) pgtype.Float8 {
	if !set {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: v, Valid: true}
}

func labResultToProto(r sqlcgen.LabResult, recordedByName string) *stillhousev1.LabResult {
	out := &stillhousev1.LabResult{
		Id:             r.ID.String(),
		Analyte:        r.Analyte,
		Uom:            r.Uom,
		Status:         labStatusToProto(r.Status),
		Method:         r.Method,
		Laboratory:     r.Laboratory,
		Reference:      r.Reference,
		SampledOn:      formatDate(r.SampledOn),
		ReportedOn:     formatDate(r.ReportedOn),
		Notes:          r.Notes,
		RecordedByName: recordedByName,
	}
	if r.ContainerID.Valid {
		out.ContainerId = r.ContainerID.UUID.String()
	}
	if r.ProductionGaugeID.Valid {
		out.ProductionGaugeId = r.ProductionGaugeID.UUID.String()
	}
	if r.BottlingRunID.Valid {
		out.BottlingRunId = r.BottlingRunID.UUID.String()
	}
	if r.MashRunID.Valid {
		out.MashRunId = r.MashRunID.UUID.String()
	}
	if r.Value.Valid {
		out.Value, out.ValueSet = r.Value.Float64, true
	}
	if r.SpecMin.Valid {
		out.SpecMin, out.SpecMinSet = r.SpecMin.Float64, true
	}
	if r.SpecMax.Valid {
		out.SpecMax, out.SpecMaxSet = r.SpecMax.Float64, true
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(r.CreatedAt.Time)
	}
	return out
}

func labStatusToDB(s stillhousev1.LabResultStatus) (sqlcgen.LabResultStatus, error) {
	switch s {
	case stillhousev1.LabResultStatus_LAB_RESULT_STATUS_PASS:
		return sqlcgen.LabResultStatusPass, nil
	case stillhousev1.LabResultStatus_LAB_RESULT_STATUS_FAIL:
		return sqlcgen.LabResultStatusFail, nil
	case stillhousev1.LabResultStatus_LAB_RESULT_STATUS_INFORMATIONAL,
		stillhousev1.LabResultStatus_LAB_RESULT_STATUS_UNSPECIFIED:
		return sqlcgen.LabResultStatusInformational, nil
	}
	return "", errors.New("invalid lab result status")
}

func labStatusToProto(s sqlcgen.LabResultStatus) stillhousev1.LabResultStatus {
	switch s {
	case sqlcgen.LabResultStatusPass:
		return stillhousev1.LabResultStatus_LAB_RESULT_STATUS_PASS
	case sqlcgen.LabResultStatusFail:
		return stillhousev1.LabResultStatus_LAB_RESULT_STATUS_FAIL
	}
	return stillhousev1.LabResultStatus_LAB_RESULT_STATUS_INFORMATIONAL
}
