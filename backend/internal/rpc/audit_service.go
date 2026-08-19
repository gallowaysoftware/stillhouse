package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type AuditService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewAuditService(db *tenantdb.DB, logger *slog.Logger) *AuditService {
	return &AuditService{db: db, logger: logger}
}

func (s *AuditService) ListAuditEvents(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListAuditEventsRequest],
) (*connect.Response[stillhousev1.ListAuditEventsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	limit := req.Msg.GetLimit()
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := req.Msg.GetOffset()
	if offset < 0 {
		offset = 0
	}
	var entityType pgtype.Text
	if t := req.Msg.GetEntityType(); t != "" {
		entityType = pgtype.Text{String: t, Valid: true}
	}

	var (
		rows  []sqlcgen.ListAuditEventsRow
		count int64
	)
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListAuditEvents(ctx, sqlcgen.ListAuditEventsParams{
			EntityType: entityType,
			Limit:      limit,
			Offset:     offset,
		})
		if e != nil {
			return e
		}
		count, e = q.CountAuditEvents(ctx, sqlcgen.CountAuditEventsParams{
			EntityType: entityType,
		})
		return e
	})
	if err != nil {
		s.logger.Error("ListAuditEvents", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.ListAuditEventsResponse{
		Events:     make([]*stillhousev1.AuditEvent, 0, len(rows)),
		TotalCount: count,
	}
	for _, r := range rows {
		ev := &stillhousev1.AuditEvent{
			Id:          r.ID.String(),
			TenantId:    r.TenantID.String(),
			EntityType:  r.EntityType,
			EntityId:    r.EntityID,
			Action:      auditActionToProto(r.Action),
			OccurredAt:  timestamppb.New(r.OccurredAt.Time),
			PayloadJson: r.Payload,
		}
		if r.UserID.Valid {
			ev.UserId = r.UserID.UUID.String()
		}
		if r.UserEmail.Valid {
			ev.UserEmail = r.UserEmail.String
		}
		if r.UserDisplayName.Valid {
			ev.UserDisplayName = r.UserDisplayName.String
		}
		out.Events = append(out.Events, ev)
	}
	return connect.NewResponse(out), nil
}

func auditActionToProto(a sqlcgen.AuditAction) stillhousev1.AuditAction {
	switch a {
	case sqlcgen.AuditActionCreate:
		return stillhousev1.AuditAction_AUDIT_ACTION_CREATE
	case sqlcgen.AuditActionUpdate:
		return stillhousev1.AuditAction_AUDIT_ACTION_UPDATE
	case sqlcgen.AuditActionDelete:
		return stillhousev1.AuditAction_AUDIT_ACTION_DELETE
	case sqlcgen.AuditActionSign:
		return stillhousev1.AuditAction_AUDIT_ACTION_SIGN
	case sqlcgen.AuditActionLogin:
		return stillhousev1.AuditAction_AUDIT_ACTION_LOGIN
	case sqlcgen.AuditActionLogout:
		return stillhousev1.AuditAction_AUDIT_ACTION_LOGOUT
	}
	return stillhousev1.AuditAction_AUDIT_ACTION_UNSPECIFIED
}
