package rpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alerting"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// AlertService reads what the evaluator found and records that a human
// has seen it.
//
// It cannot create or resolve an alert, and that is the design rather
// than an omission: an alert is a condition, so only the thing that
// evaluates conditions may open or close one. What a person can do is
// acknowledge — say they have seen it — which leaves the alert open
// while its condition holds. A "dismiss" button that closed a still-true
// condition would be the feature that makes the whole system ignorable.
type AlertService struct {
	db     *tenantdb.DB
	runner *alerting.Runner
	logger *slog.Logger
}

func NewAlertService(db *tenantdb.DB, runner *alerting.Runner, logger *slog.Logger) *AlertService {
	return &AlertService{db: db, runner: runner, logger: logger}
}

func (s *AlertService) ListAlerts(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListAlertsRequest],
) (*connect.Response[stillhousev1.ListAlertsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out, err := s.list(ctx, u.TenantID, req.Msg.GetIncludeResolved(), req.Msg.GetLimit())
	if err != nil {
		s.logger.Error("ListAlerts", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

func (s *AlertService) AcknowledgeAlert(
	ctx context.Context,
	req *connect.Request[stillhousev1.AcknowledgeAlertRequest],
) (*connect.Response[stillhousev1.AcknowledgeAlertResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var row sqlcgen.Alert
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.AcknowledgeAlert(ctx, sqlcgen.AcknowledgeAlertParams{
			ID: id, AcknowledgedBy: uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound,
				errors.New("alert not found, or it has already resolved"))
		}
		s.logger.Error("AcknowledgeAlert", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.AcknowledgeAlertResponse{
		Alert: alertToProto(row, u.DisplayName),
	}), nil
}

// EvaluateAlerts re-runs the rules for the caller's tenant. What the
// dashboard calls when someone has just fixed the thing and wants to
// watch it go away, rather than waiting out the timer.
func (s *AlertService) EvaluateAlerts(
	ctx context.Context,
	_ *connect.Request[stillhousev1.EvaluateAlertsRequest],
) (*connect.Response[stillhousev1.EvaluateAlertsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var tenant sqlcgen.Tenant
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		tenant, err = q.GetTenantByID(ctx, u.TenantID)
		return err
	}); err != nil {
		s.logger.Error("EvaluateAlerts: tenant", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if err := s.runner.RunTenant(ctx, tenant, time.Now().UTC()); err != nil {
		s.logger.Error("EvaluateAlerts", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out, err := s.list(ctx, u.TenantID, false, 0)
	if err != nil {
		s.logger.Error("EvaluateAlerts: list", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.EvaluateAlertsResponse{Alerts: out}), nil
}

func (s *AlertService) SetAlertEmail(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetAlertEmailRequest],
) (*connect.Response[stillhousev1.SetAlertEmailResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var row sqlcgen.User
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.SetUserAlertEmail(ctx, sqlcgen.SetUserAlertEmailParams{
			ID: u.ID, AlertEmail: req.Msg.GetEnabled(),
		})
		return err
	})
	if err != nil {
		s.logger.Error("SetAlertEmail", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetAlertEmailResponse{Enabled: row.AlertEmail}), nil
}

func (s *AlertService) list(
	ctx context.Context, tenantID uuid.UUID, includeResolved bool, limit int32,
) (*stillhousev1.ListAlertsResponse, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	out := &stillhousev1.ListAlertsResponse{}
	err := s.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if includeResolved {
			rows, err := q.ListRecentAlerts(ctx, limit)
			if err != nil {
				return err
			}
			for _, r := range rows {
				a := alertToProto(sqlcgen.Alert{
					ID: r.ID, TenantID: r.TenantID, Kind: r.Kind, Severity: r.Severity,
					SubjectKey: r.SubjectKey, Title: r.Title, Detail: r.Detail,
					EntityType: r.EntityType, EntityID: r.EntityID,
					OpenedAt: r.OpenedAt, LastSeenAt: r.LastSeenAt, ResolvedAt: r.ResolvedAt,
					AcknowledgedAt: r.AcknowledgedAt, AcknowledgedBy: r.AcknowledgedBy,
				}, r.AcknowledgedByName)
				out.Alerts = append(out.Alerts, a)
			}
			return nil
		}
		rows, err := q.ListOpenAlerts(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			a := alertToProto(sqlcgen.Alert{
				ID: r.ID, TenantID: r.TenantID, Kind: r.Kind, Severity: r.Severity,
				SubjectKey: r.SubjectKey, Title: r.Title, Detail: r.Detail,
				EntityType: r.EntityType, EntityID: r.EntityID,
				OpenedAt: r.OpenedAt, LastSeenAt: r.LastSeenAt, ResolvedAt: r.ResolvedAt,
				AcknowledgedAt: r.AcknowledgedAt, AcknowledgedBy: r.AcknowledgedBy,
			}, r.AcknowledgedByName)
			out.Alerts = append(out.Alerts, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, a := range out.Alerts {
		if a.GetResolvedAt() != nil {
			continue
		}
		out.OpenCount++
		if a.GetSeverity() == stillhousev1.AlertSeverity_ALERT_SEVERITY_CRITICAL {
			out.CriticalCount++
		}
	}
	return out, nil
}

func alertToProto(a sqlcgen.Alert, acknowledgedByName string) *stillhousev1.Alert {
	out := &stillhousev1.Alert{
		Id:                 a.ID.String(),
		Kind:               alertKindToProto(a.Kind),
		Severity:           alertSeverityToProto(a.Severity),
		Title:              a.Title,
		Detail:             a.Detail,
		EntityType:         a.EntityType,
		AcknowledgedByName: acknowledgedByName,
	}
	if a.EntityID.Valid {
		out.EntityId = a.EntityID.UUID.String()
	}
	if a.OpenedAt.Valid {
		out.OpenedAt = timestamppb.New(a.OpenedAt.Time)
	}
	if a.LastSeenAt.Valid {
		out.LastSeenAt = timestamppb.New(a.LastSeenAt.Time)
	}
	if a.ResolvedAt.Valid {
		out.ResolvedAt = timestamppb.New(a.ResolvedAt.Time)
	}
	if a.AcknowledgedAt.Valid {
		out.AcknowledgedAt = timestamppb.New(a.AcknowledgedAt.Time)
	}
	return out
}

func alertKindToProto(k sqlcgen.AlertKind) stillhousev1.AlertKind {
	switch k {
	case sqlcgen.AlertKindFilingDue:
		return stillhousev1.AlertKind_ALERT_KIND_FILING_DUE
	case sqlcgen.AlertKindFilingOverdue:
		return stillhousev1.AlertKind_ALERT_KIND_FILING_OVERDUE
	case sqlcgen.AlertKindStampsLow:
		return stillhousev1.AlertKind_ALERT_KIND_STAMPS_LOW
	case sqlcgen.AlertKindFermentationStalled:
		return stillhousev1.AlertKind_ALERT_KIND_FERMENTATION_STALLED
	case sqlcgen.AlertKindBarrelUnmeasured:
		return stillhousev1.AlertKind_ALERT_KIND_BARREL_UNMEASURED
	case sqlcgen.AlertKindLicenceExpiring:
		return stillhousev1.AlertKind_ALERT_KIND_LICENCE_EXPIRING
	case sqlcgen.AlertKindLicenceExpired:
		return stillhousev1.AlertKind_ALERT_KIND_LICENCE_EXPIRED
	case sqlcgen.AlertKindLicenceSecurityExpiring:
		return stillhousev1.AlertKind_ALERT_KIND_LICENCE_SECURITY_EXPIRING
	case sqlcgen.AlertKindWorkOrderOverdue:
		return stillhousev1.AlertKind_ALERT_KIND_WORK_ORDER_OVERDUE
	case sqlcgen.AlertKindWorkOrderUnassigned:
		return stillhousev1.AlertKind_ALERT_KIND_WORK_ORDER_UNASSIGNED
	case sqlcgen.AlertKindRedistillationOpen:
		return stillhousev1.AlertKind_ALERT_KIND_REDISTILLATION_OPEN
	case sqlcgen.AlertKindProvincialFilingDue:
		return stillhousev1.AlertKind_ALERT_KIND_PROVINCIAL_FILING_DUE
	case sqlcgen.AlertKindProvincialFilingOverdue:
		return stillhousev1.AlertKind_ALERT_KIND_PROVINCIAL_FILING_OVERDUE
	case sqlcgen.AlertKindInvoiceOverdue:
		return stillhousev1.AlertKind_ALERT_KIND_INVOICE_OVERDUE
	case sqlcgen.AlertKindEquipmentServiceDue:
		return stillhousev1.AlertKind_ALERT_KIND_EQUIPMENT_SERVICE_DUE
	case sqlcgen.AlertKindEquipmentDown:
		return stillhousev1.AlertKind_ALERT_KIND_EQUIPMENT_DOWN
	case sqlcgen.AlertKindMaterialLow:
		return stillhousev1.AlertKind_ALERT_KIND_MATERIAL_LOW
	}
	return stillhousev1.AlertKind_ALERT_KIND_UNSPECIFIED
}

func alertSeverityToProto(s sqlcgen.AlertSeverity) stillhousev1.AlertSeverity {
	switch s {
	case sqlcgen.AlertSeverityInfo:
		return stillhousev1.AlertSeverity_ALERT_SEVERITY_INFO
	case sqlcgen.AlertSeverityWarning:
		return stillhousev1.AlertSeverity_ALERT_SEVERITY_WARNING
	case sqlcgen.AlertSeverityCritical:
		return stillhousev1.AlertSeverity_ALERT_SEVERITY_CRITICAL
	}
	return stillhousev1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED
}
