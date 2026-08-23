package rpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/secrets"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
	"github.com/gallowaysoftware/stillhouse/backend/internal/webhook"
)

type WebhookService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewWebhookService(db *tenantdb.DB, logger *slog.Logger) *WebhookService {
	return &WebhookService{db: db, logger: logger}
}

func (s *WebhookService) ListWebhookEndpoints(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListWebhookEndpointsRequest],
) (*connect.Response[stillhousev1.ListWebhookEndpointsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out := &stillhousev1.ListWebhookEndpointsResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListWebhookEndpoints(ctx)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Endpoints = append(out.Endpoints, webhookEndpointToProto(endpointRow{
				ID: r.ID, Url: r.Url, Kinds: r.Kinds, Enabled: r.Enabled,
				Description: r.Description, CreatedAt: r.CreatedAt.Time,
			}))
		}
		return nil
	}); err != nil {
		s.logger.Error("ListWebhookEndpoints", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// CreateWebhookEndpoint registers a URL and mints its signing secret.
//
// The URL is validated here for the operator's benefit — a fat-fingered
// internal hostname should be refused at the form, not by silence — but
// this is NOT the protection. DNS resolves at delivery time, so the
// address is checked again on every connection by the dialler in
// internal/webhook. See the comment on ValidateURL.
func (s *WebhookService) CreateWebhookEndpoint(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateWebhookEndpointRequest],
) (*connect.Response[stillhousev1.CreateWebhookEndpointResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	url := strings.TrimSpace(req.Msg.GetUrl())
	if err := webhook.ValidateURL(url); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	kinds := make([]string, 0, len(req.Msg.GetKinds()))
	for _, k := range req.Msg.GetKinds() {
		v, err := webhookKindFromProto(k)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		kinds = append(kinds, string(v))
	}
	if len(kinds) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("subscribe to at least one event, or the endpoint will never be called"))
	}

	// 32 bytes from crypto/rand. Shown once and sealed; see the proto.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.logger.Error("CreateWebhookEndpoint: rand", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	sealed, err := secrets.Seal(raw)
	if err != nil {
		s.logger.Error("CreateWebhookEndpoint: seal", "err", err)
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("secrets are not configured on this server, so a signing key cannot be stored"))
	}

	var row endpointRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.CreateWebhookEndpoint(ctx, sqlcgen.CreateWebhookEndpointParams{
			TenantID:     u.TenantID,
			Url:          url,
			SecretSealed: sealed,
			Kinds:        kinds,
			Description:  req.Msg.GetDescription(),
		})
		if e != nil {
			return e
		}
		row = endpointRow{ID: r.ID, Url: r.Url, Kinds: r.Kinds, Enabled: r.Enabled,
			Description: r.Description, CreatedAt: r.CreatedAt.Time}
		return audit.Write(ctx, q, u.TenantID, u.ID, "webhook_endpoint", r.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{"url": url})
	}); err != nil {
		if ce := uniqueViolation(err, "webhook endpoint"); ce != nil {
			return nil, ce
		}
		s.logger.Error("CreateWebhookEndpoint", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	return connect.NewResponse(&stillhousev1.CreateWebhookEndpointResponse{
		Endpoint: webhookEndpointToProto(row),
		Secret:   base64.RawURLEncoding.EncodeToString(raw),
	}), nil
}

func (s *WebhookService) SetWebhookEndpointEnabled(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetWebhookEndpointEnabledRequest],
) (*connect.Response[stillhousev1.SetWebhookEndpointEnabledResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var row endpointRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.SetWebhookEndpointEnabled(ctx, sqlcgen.SetWebhookEndpointEnabledParams{
			ID: id, Enabled: req.Msg.GetEnabled(),
		})
		if e != nil {
			return e
		}
		row = endpointRow{ID: r.ID, Url: r.Url, Kinds: r.Kinds, Enabled: r.Enabled,
			Description: r.Description, CreatedAt: r.CreatedAt.Time}
		return audit.Write(ctx, q, u.TenantID, u.ID, "webhook_endpoint", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{"enabled": req.Msg.GetEnabled()})
	}); err != nil {
		s.logger.Error("SetWebhookEndpointEnabled", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetWebhookEndpointEnabledResponse{
		Endpoint: webhookEndpointToProto(row),
	}), nil
}

func (s *WebhookService) DeleteWebhookEndpoint(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteWebhookEndpointRequest],
) (*connect.Response[stillhousev1.DeleteWebhookEndpointResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertNoLegalHold(ctx, q, "a webhook endpoint"); e != nil {
			return e
		}
		if e := q.DeleteWebhookEndpoint(ctx, id); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "webhook_endpoint", id.String(),
			sqlcgen.AuditActionDelete, nil)
	}); err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		s.logger.Error("DeleteWebhookEndpoint", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.DeleteWebhookEndpointResponse{}), nil
}

func (s *WebhookService) ListWebhookDeliveries(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListWebhookDeliveriesRequest],
) (*connect.Response[stillhousev1.ListWebhookDeliveriesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	limit := req.Msg.GetLimit()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := &stillhousev1.ListWebhookDeliveriesResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListWebhookDeliveries(ctx, limit)
		if e != nil {
			return e
		}
		for _, r := range rows {
			d := &stillhousev1.WebhookDelivery{
				Id:            r.ID.String(),
				Url:           r.Url,
				Kind:          webhookKindToProto(string(r.Kind)),
				Status:        string(r.Status),
				Attempts:      r.Attempts,
				LastError:     r.LastError,
				CreatedAt:     r.CreatedAt.Time.Format("2006-01-02 15:04"),
				NextAttemptAt: r.NextAttemptAt.Time.Format("2006-01-02 15:04"),
			}
			if r.LastStatusCode.Valid {
				d.LastStatusCode = r.LastStatusCode.Int32
			}
			if r.DeliveredAt.Valid {
				d.DeliveredAt = r.DeliveredAt.Time.Format("2006-01-02 15:04")
			}
			out.Deliveries = append(out.Deliveries, d)
		}
		return nil
	}); err != nil {
		s.logger.Error("ListWebhookDeliveries", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// endpointRow is the shape the three endpoint queries share. They return
// distinct generated structs because each casts kinds to text[], so the
// conversion takes the fields rather than a row type.
type endpointRow struct {
	ID          uuid.UUID
	Url         string
	Kinds       []string
	Enabled     bool
	Description string
	CreatedAt   time.Time
}

func webhookEndpointToProto(r endpointRow) *stillhousev1.WebhookEndpoint {
	out := &stillhousev1.WebhookEndpoint{
		Id:          r.ID.String(),
		Url:         r.Url,
		Enabled:     r.Enabled,
		Description: r.Description,
		CreatedAt:   r.CreatedAt.Format("2006-01-02"),
	}
	for _, k := range r.Kinds {
		out.Kinds = append(out.Kinds, webhookKindToProto(k))
	}
	return out
}

func webhookKindToProto(k string) stillhousev1.WebhookEventKind {
	switch k {
	case "b266_period_submitted":
		return stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_B266_PERIOD_SUBMITTED
	case "bottling_run_recorded":
		return stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_BOTTLING_RUN_RECORDED
	case "removal_recorded":
		return stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_REMOVAL_RECORDED
	case "production_gauge_recorded":
		return stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_PRODUCTION_GAUGE_RECORDED
	case "loss_recorded":
		return stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_LOSS_RECORDED
	}
	return stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_UNSPECIFIED
}

func webhookKindFromProto(k stillhousev1.WebhookEventKind) (sqlcgen.WebhookEventKind, error) {
	switch k {
	case stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_B266_PERIOD_SUBMITTED:
		return sqlcgen.WebhookEventKindB266PeriodSubmitted, nil
	case stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_BOTTLING_RUN_RECORDED:
		return sqlcgen.WebhookEventKindBottlingRunRecorded, nil
	case stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_REMOVAL_RECORDED:
		return sqlcgen.WebhookEventKindRemovalRecorded, nil
	case stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_PRODUCTION_GAUGE_RECORDED:
		return sqlcgen.WebhookEventKindProductionGaugeRecorded, nil
	case stillhousev1.WebhookEventKind_WEBHOOK_EVENT_KIND_LOSS_RECORDED:
		return sqlcgen.WebhookEventKindLossRecorded, nil
	}
	return "", errors.New("unknown event kind")
}
