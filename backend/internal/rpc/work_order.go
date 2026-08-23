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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type WorkOrderService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewWorkOrderService(db *tenantdb.DB, logger *slog.Logger) *WorkOrderService {
	return &WorkOrderService{db: db, logger: logger}
}

func (s *WorkOrderService) SaveWorkOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveWorkOrderRequest],
) (*connect.Response[stillhousev1.SaveWorkOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	title := strings.TrimSpace(in.GetTitle())
	if title == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say what the job is"))
	}
	kind, err := workOrderKindToDB(in.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// At most one subject. The database enforces it too; this makes the
	// message useful.
	subjects := map[string]string{
		"container_id": in.GetContainerId(),
		"product_id":   in.GetProductId(),
		"recipe_id":    in.GetRecipeId(),
	}
	parsed := map[string]uuid.NullUUID{}
	var named int
	for field, v := range subjects {
		if v == "" {
			continue
		}
		id, perr := uuid.Parse(v)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid %s", field))
		}
		parsed[field] = uuid.NullUUID{UUID: id, Valid: true}
		named++
	}
	if named > 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a work order is about one thing — a cask, a product or a recipe"))
	}

	var assignedTo uuid.NullUUID
	if v := in.GetAssignedTo(); v != "" {
		id, perr := uuid.Parse(v)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid assigned_to"))
		}
		assignedTo = uuid.NullUUID{UUID: id, Valid: true}
	}
	var assignedRole sqlcgen.NullUserRole
	if r := in.GetAssignedRole(); r != stillhousev1.UserRole_USER_ROLE_UNSPECIFIED {
		dbRole, rerr := userRoleToDB(r)
		if rerr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, rerr)
		}
		assignedRole = sqlcgen.NullUserRole{UserRole: dbRole, Valid: true}
	}
	var locationID uuid.NullUUID
	if v := in.GetLocationId(); v != "" {
		id, perr := uuid.Parse(v)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid location_id"))
		}
		locationID = uuid.NullUUID{UUID: id, Valid: true}
	}
	scheduledFor, err := parseOptionalDate(in.GetScheduledFor(), "scheduled_for")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	dueOn, err := parseOptionalDate(in.GetDueOn(), "due_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if dueOn.Valid && scheduledFor.Valid && dueOn.Time.Before(scheduledFor.Time) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("the due date is before the day it is scheduled for"))
	}

	var row sqlcgen.WorkOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if in.GetId() == "" {
			// Serialised the same way as every other document counter
			// here — two orders raised at once otherwise claim the same
			// number and one dies on the UNIQUE.
			if e := q.LockDocumentSequence(ctx, "work_orders"); e != nil {
				return e
			}
			next, ne := q.NextWorkOrderNo(ctx)
			if ne != nil {
				return ne
			}
			row, e = q.CreateWorkOrder(ctx, sqlcgen.CreateWorkOrderParams{
				TenantID: u.TenantID, WorkOrderNo: next, Kind: kind,
				Title: title, Detail: in.GetDetail(),
				AssignedTo: assignedTo, AssignedRole: assignedRole,
				LocationID: locationID, ScheduledFor: scheduledFor, DueOn: dueOn,
				ContainerID: parsed["container_id"], ProductID: parsed["product_id"],
				RecipeID: parsed["recipe_id"], CreatedBy: u.ID,
			})
			if e != nil {
				return e
			}
		} else {
			id, perr := uuid.Parse(in.GetId())
			if perr != nil {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
			}
			row, e = q.UpdateWorkOrder(ctx, sqlcgen.UpdateWorkOrderParams{
				ID: id, Kind: kind, Title: title, Detail: in.GetDetail(),
				AssignedTo: assignedTo, AssignedRole: assignedRole,
				LocationID: locationID, ScheduledFor: scheduledFor, DueOn: dueOn,
				ContainerID: parsed["container_id"], ProductID: parsed["product_id"],
				RecipeID: parsed["recipe_id"],
			})
			if errors.Is(e, pgx.ErrNoRows) {
				// The UPDATE is scoped to open work orders, so no rows
				// means it is finished or cancelled. Editing history is a
				// different act from editing a plan.
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("this work order is finished or cancelled; raise a new one"))
			}
			if e != nil {
				return e
			}
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "work_order", row.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"work_order_no": row.WorkOrderNo, "kind": string(row.Kind),
				"title": row.Title, "assigned_to": nullUUIDString(row.AssignedTo),
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if ce := classifyWriteErr(err, "what this work order refers to no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("SaveWorkOrder", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SaveWorkOrderResponse{
		WorkOrder: workOrderToProto(row, workOrderNames{}),
	}), nil
}

func (s *WorkOrderService) SetWorkOrderStatus(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetWorkOrderStatusRequest],
) (*connect.Response[stillhousev1.SetWorkOrderStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	status, err := workOrderStatusToDB(req.Msg.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if status == sqlcgen.WorkOrderStatusCancelled &&
		strings.TrimSpace(req.Msg.GetCancelReason()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why it is being cancelled"))
	}

	var row sqlcgen.WorkOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.SetWorkOrderStatus(ctx, sqlcgen.SetWorkOrderStatusParams{
			ID: id, Status: status,
			CompletedBy:  uuid.NullUUID{UUID: u.ID, Valid: status == sqlcgen.WorkOrderStatusDone},
			CancelReason: req.Msg.GetCancelReason(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "work_order", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"work_order_no": row.WorkOrderNo, "status": string(row.Status),
				"cancel_reason": row.CancelReason,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("work order not found"))
		}
		s.logger.Error("SetWorkOrderStatus", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetWorkOrderStatusResponse{
		WorkOrder: workOrderToProto(row, workOrderNames{}),
	}), nil
}

func (s *WorkOrderService) ListWorkOrders(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListWorkOrdersRequest],
) (*connect.Response[stillhousev1.ListWorkOrdersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	params := sqlcgen.ListWorkOrdersParams{
		OpenOnly: req.Msg.GetOpenOnly(),
		RowLimit: 100,
	}
	if v := req.Msg.GetLimit(); v > 0 && v <= 500 {
		params.RowLimit = v
	}
	// "me" rather than an id, so a caller does not have to know its own
	// user id to ask for its own board.
	if v := req.Msg.GetAssignedTo(); v == "me" {
		params.AssignedTo = uuid.NullUUID{UUID: u.ID, Valid: true}
	} else if v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid assigned_to"))
		}
		params.AssignedTo = uuid.NullUUID{UUID: id, Valid: true}
	}

	var rows []sqlcgen.ListWorkOrdersRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListWorkOrders(ctx, params)
		return e
	}); err != nil {
		s.logger.Error("ListWorkOrders", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.WorkOrder, 0, len(rows))
	for _, r := range rows {
		out = append(out, workOrderToProto(sqlcgen.WorkOrder{
			ID: r.ID, WorkOrderNo: r.WorkOrderNo, Kind: r.Kind, Status: r.Status,
			Title: r.Title, Detail: r.Detail, AssignedTo: r.AssignedTo,
			AssignedRole: r.AssignedRole, LocationID: r.LocationID,
			ScheduledFor: r.ScheduledFor, DueOn: r.DueOn,
			ContainerID: r.ContainerID, ProductID: r.ProductID, RecipeID: r.RecipeID,
			MashRunID: r.MashRunID, DistillationRunID: r.DistillationRunID,
			BottlingRunID: r.BottlingRunID, StartedAt: r.StartedAt,
			CompletedAt: r.CompletedAt, CancelReason: r.CancelReason,
		}, workOrderNames{
			assignedTo:  r.AssignedToName,
			completedBy: r.CompletedByName,
			location:    r.LocationName,
			container:   r.ContainerName,
			product:     r.ProductName,
			recipe:      r.RecipeName,
		}))
	}
	return connect.NewResponse(&stillhousev1.ListWorkOrdersResponse{WorkOrders: out}), nil
}

type workOrderNames struct {
	assignedTo, completedBy, location, container, product, recipe string
}

func workOrderToProto(w sqlcgen.WorkOrder, n workOrderNames) *stillhousev1.WorkOrder {
	out := &stillhousev1.WorkOrder{
		Id:                w.ID.String(),
		WorkOrderNo:       w.WorkOrderNo,
		Kind:              workOrderKindToProto(w.Kind),
		Status:            workOrderStatusToProto(w.Status),
		Title:             w.Title,
		Detail:            w.Detail,
		AssignedTo:        nullUUIDString(w.AssignedTo),
		AssignedToName:    n.assignedTo,
		LocationId:        nullUUIDString(w.LocationID),
		LocationName:      n.location,
		ScheduledFor:      formatDate(w.ScheduledFor),
		DueOn:             formatDate(w.DueOn),
		ContainerId:       nullUUIDString(w.ContainerID),
		ContainerName:     n.container,
		ProductId:         nullUUIDString(w.ProductID),
		ProductName:       n.product,
		RecipeId:          nullUUIDString(w.RecipeID),
		RecipeName:        n.recipe,
		MashRunId:         nullUUIDString(w.MashRunID),
		DistillationRunId: nullUUIDString(w.DistillationRunID),
		BottlingRunId:     nullUUIDString(w.BottlingRunID),
		CompletedByName:   n.completedBy,
		CancelReason:      w.CancelReason,
	}
	if w.AssignedRole.Valid {
		out.AssignedRole = roleToProto(w.AssignedRole.UserRole)
	}
	if w.StartedAt.Valid {
		out.StartedAt = timestamppb.New(w.StartedAt.Time)
	}
	if w.CompletedAt.Valid {
		out.CompletedAt = timestamppb.New(w.CompletedAt.Time)
	}
	return out
}

func workOrderKindToDB(k stillhousev1.WorkOrderKind) (sqlcgen.WorkOrderKind, error) {
	switch k {
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_MASH:
		return sqlcgen.WorkOrderKindMash, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_FERMENTATION:
		return sqlcgen.WorkOrderKindFermentation, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_DISTILLATION:
		return sqlcgen.WorkOrderKindDistillation, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_BOTTLING:
		return sqlcgen.WorkOrderKindBottling, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_BARREL_FILL:
		return sqlcgen.WorkOrderKindBarrelFill, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_BARREL_DUMP:
		return sqlcgen.WorkOrderKindBarrelDump, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_REGAUGE:
		return sqlcgen.WorkOrderKindRegauge, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_CLEANING:
		return sqlcgen.WorkOrderKindCleaning, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_MAINTENANCE:
		return sqlcgen.WorkOrderKindMaintenance, nil
	case stillhousev1.WorkOrderKind_WORK_ORDER_KIND_OTHER,
		stillhousev1.WorkOrderKind_WORK_ORDER_KIND_UNSPECIFIED:
		return sqlcgen.WorkOrderKindOther, nil
	}
	return "", errors.New("invalid work order kind")
}

func workOrderKindToProto(k sqlcgen.WorkOrderKind) stillhousev1.WorkOrderKind {
	switch k {
	case sqlcgen.WorkOrderKindMash:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_MASH
	case sqlcgen.WorkOrderKindFermentation:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_FERMENTATION
	case sqlcgen.WorkOrderKindDistillation:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_DISTILLATION
	case sqlcgen.WorkOrderKindBottling:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_BOTTLING
	case sqlcgen.WorkOrderKindBarrelFill:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_BARREL_FILL
	case sqlcgen.WorkOrderKindBarrelDump:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_BARREL_DUMP
	case sqlcgen.WorkOrderKindRegauge:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_REGAUGE
	case sqlcgen.WorkOrderKindCleaning:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_CLEANING
	case sqlcgen.WorkOrderKindMaintenance:
		return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_MAINTENANCE
	}
	return stillhousev1.WorkOrderKind_WORK_ORDER_KIND_OTHER
}

func workOrderStatusToDB(s stillhousev1.WorkOrderStatus) (sqlcgen.WorkOrderStatus, error) {
	switch s {
	case stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_PLANNED:
		return sqlcgen.WorkOrderStatusPlanned, nil
	case stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_IN_PROGRESS:
		return sqlcgen.WorkOrderStatusInProgress, nil
	case stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_DONE:
		return sqlcgen.WorkOrderStatusDone, nil
	case stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_CANCELLED:
		return sqlcgen.WorkOrderStatusCancelled, nil
	}
	return "", errors.New("invalid work order status")
}

func workOrderStatusToProto(s sqlcgen.WorkOrderStatus) stillhousev1.WorkOrderStatus {
	switch s {
	case sqlcgen.WorkOrderStatusPlanned:
		return stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_PLANNED
	case sqlcgen.WorkOrderStatusInProgress:
		return stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_IN_PROGRESS
	case sqlcgen.WorkOrderStatusDone:
		return stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_DONE
	case sqlcgen.WorkOrderStatusCancelled:
		return stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_CANCELLED
	}
	return stillhousev1.WorkOrderStatus_WORK_ORDER_STATUS_UNSPECIFIED
}
