package rpc

import (
	"context"
	"errors"
	"log/slog"

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

type ExciseStampService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewExciseStampService(db *tenantdb.DB, logger *slog.Logger) *ExciseStampService {
	return &ExciseStampService{db: db, logger: logger}
}

func (s *ExciseStampService) CreateStampOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateStampOrderRequest],
) (*connect.Response[stillhousev1.CreateStampOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetJurisdiction() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("jurisdiction is required (e.g. CA-ON)"))
	}
	if in.GetQuantityOrdered() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("quantity_ordered must be > 0"))
	}
	var o sqlcgen.ExciseStampOrder
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		o, e = q.CreateStampOrder(ctx, sqlcgen.CreateStampOrderParams{
			TenantID:        u.TenantID,
			Jurisdiction:    in.GetJurisdiction(),
			QuantityOrdered: in.GetQuantityOrdered(),
			Notes:           in.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "excise_stamp_order", o.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"jurisdiction":     o.Jurisdiction,
				"quantity_ordered": o.QuantityOrdered,
			})
	})
	if err != nil {
		s.logger.Error("CreateStampOrder", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateStampOrderResponse{Order: stampOrderToProto(o)}), nil
}

func (s *ExciseStampService) ReceiveStampOrder(
	ctx context.Context,
	req *connect.Request[stillhousev1.ReceiveStampOrderRequest],
) (*connect.Response[stillhousev1.ReceiveStampOrderResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if in.GetQuantityReceived() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("quantity_received must be > 0"))
	}
	received := pgtype.Timestamptz{Valid: false}
	if in.GetReceivedAt() != nil {
		received = pgtype.Timestamptz{Valid: true, Time: in.GetReceivedAt().AsTime()}
	}

	var o sqlcgen.ExciseStampOrder
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		o, e = q.ReceiveStampOrder(ctx, sqlcgen.ReceiveStampOrderParams{
			ID:               id,
			ReceivedAt:       received,
			QuantityReceived: in.GetQuantityReceived(),
			SerialStart:      pgtype.Text{String: in.GetSerialStart(), Valid: in.GetSerialStart() != ""},
			SerialEnd:        pgtype.Text{String: in.GetSerialEnd(), Valid: in.GetSerialEnd() != ""},
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "excise_stamp_order", o.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":             "received",
				"jurisdiction":      o.Jurisdiction,
				"quantity_received": o.QuantityReceived,
				"serial_start":      o.SerialStart.String,
				"serial_end":        o.SerialEnd.String,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("stamp order not found"))
		}
		s.logger.Error("ReceiveStampOrder", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.ReceiveStampOrderResponse{Order: stampOrderToProto(o)}), nil
}

func (s *ExciseStampService) ListStampOrders(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListStampOrdersRequest],
) (*connect.Response[stillhousev1.ListStampOrdersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var jurisdiction pgtype.Text
	if j := req.Msg.GetJurisdiction(); j != "" {
		jurisdiction = pgtype.Text{String: j, Valid: true}
	}
	var (
		orders    []sqlcgen.ExciseStampOrder
		summaries []sqlcgen.SumStampInventoryRow
	)
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		orders, e = q.ListStampOrders(ctx, jurisdiction)
		if e != nil {
			return e
		}
		summaries, e = q.SumStampInventory(ctx)
		return e
	})
	if err != nil {
		s.logger.Error("ListStampOrders", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.ListStampOrdersResponse{
		Orders:    make([]*stillhousev1.ExciseStampOrder, 0, len(orders)),
		Summaries: make([]*stillhousev1.ExciseStampJurisdictionSummary, 0, len(summaries)),
	}
	for _, o := range orders {
		out.Orders = append(out.Orders, stampOrderToProto(o))
	}
	for _, s := range summaries {
		out.Summaries = append(out.Summaries, &stillhousev1.ExciseStampJurisdictionSummary{
			Jurisdiction:  s.Jurisdiction,
			TotalReceived: s.TotalReceived,
			TotalApplied:  s.TotalApplied,
			TotalVoided:   s.TotalVoided,
			TotalOnHand:   s.TotalOnHand,
		})
	}
	return connect.NewResponse(out), nil
}

func stampOrderToProto(o sqlcgen.ExciseStampOrder) *stillhousev1.ExciseStampOrder {
	available := o.QuantityReceived - o.QuantityApplied - o.QuantityVoided
	out := &stillhousev1.ExciseStampOrder{
		Id:               o.ID.String(),
		TenantId:         o.TenantID.String(),
		Jurisdiction:     o.Jurisdiction,
		OrderedAt:        timestamppb.New(o.OrderedAt.Time),
		QuantityOrdered:  o.QuantityOrdered,
		QuantityReceived: o.QuantityReceived,
		QuantityApplied:  o.QuantityApplied,
		QuantityVoided:   o.QuantityVoided,
		AvailableCount:   available,
		Status:           stampOrderStatusToProto(o.Status),
		Notes:            o.Notes,
		CreatedAt:        timestamppb.New(o.CreatedAt.Time),
		UpdatedAt:        timestamppb.New(o.UpdatedAt.Time),
	}
	if o.ReceivedAt.Valid {
		out.ReceivedAt = timestamppb.New(o.ReceivedAt.Time)
	}
	if o.SerialStart.Valid {
		out.SerialStart = o.SerialStart.String
	}
	if o.SerialEnd.Valid {
		out.SerialEnd = o.SerialEnd.String
	}
	return out
}

func stampOrderStatusToProto(s sqlcgen.ExciseStampOrderStatus) stillhousev1.ExciseStampOrderStatus {
	switch s {
	case sqlcgen.ExciseStampOrderStatusOrdered:
		return stillhousev1.ExciseStampOrderStatus_EXCISE_STAMP_ORDER_STATUS_ORDERED
	case sqlcgen.ExciseStampOrderStatusReceived:
		return stillhousev1.ExciseStampOrderStatus_EXCISE_STAMP_ORDER_STATUS_RECEIVED
	case sqlcgen.ExciseStampOrderStatusClosed:
		return stillhousev1.ExciseStampOrderStatus_EXCISE_STAMP_ORDER_STATUS_CLOSED
	}
	return stillhousev1.ExciseStampOrderStatus_EXCISE_STAMP_ORDER_STATUS_UNSPECIFIED
}
