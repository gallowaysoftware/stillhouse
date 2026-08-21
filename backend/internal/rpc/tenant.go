package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type TenantService struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
	// db is the tenant-scoped path. `tenants` itself is not RLS-scoped, so
	// most of this service reads through q directly — but `audit_events`
	// FORCES row-level security, so any write worth an audit row has to go
	// through a transaction that sets the tenant GUC first. That is stage
	// 138's lesson: without it the audit insert is refused and the whole
	// operation rolls back behind a 500, invisibly in dev.
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewTenantService(pool *pgxpool.Pool, q *sqlcgen.Queries, logger *slog.Logger) *TenantService {
	return &TenantService{pool: pool, q: q, db: tenantdb.New(pool), logger: logger}
}

// CreateTenant is the bootstrap endpoint. Calls succeed only when no tenant
// exists; once one does, every call is rejected. This protects an exposed
// self-host install from drive-by takeover after the operator has set it up.
func (s *TenantService) CreateTenant(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateTenantRequest],
) (*connect.Response[stillhousev1.CreateTenantResponse], error) {
	in := req.Msg
	if in.GetName() == "" || in.GetCraSpiritsLicenceNumber() == "" || in.GetDefaultJurisdiction() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("name, cra_spirits_licence_number, default_jurisdiction are required"))
	}
	if in.GetOwnerEmail() == "" || in.GetOwnerPassword() == "" || in.GetOwnerDisplayName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("owner_email, owner_password, owner_display_name are required"))
	}

	count, err := s.q.CountTenants(ctx)
	if err != nil {
		s.logger.Error("CreateTenant: count", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if count > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("a tenant already exists; this install is already bootstrapped"))
	}

	hash, err := auth.HashPassword(in.GetOwnerPassword())
	if err != nil {
		s.logger.Error("CreateTenant: hash", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	var created sqlcgen.Tenant
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		qtx := s.q.WithTx(tx)
		t, err := qtx.CreateTenant(ctx, sqlcgen.CreateTenantParams{
			Name:                         in.GetName(),
			CraSpiritsLicenceNumber:      in.GetCraSpiritsLicenceNumber(),
			ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
			DefaultJurisdiction:          in.GetDefaultJurisdiction(),
		})
		if err != nil {
			return err
		}
		created = t
		_, err = qtx.CreateUser(ctx, sqlcgen.CreateUserParams{
			TenantID:     t.ID,
			Email:        in.GetOwnerEmail(),
			PasswordHash: hash,
			DisplayName:  in.GetOwnerDisplayName(),
			Role:         sqlcgen.UserRoleOwner,
		})
		return err
	})
	if err != nil {
		s.logger.Error("CreateTenant: tx", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	return connect.NewResponse(&stillhousev1.CreateTenantResponse{
		Tenant: tenantToProto(created),
	}), nil
}

func (s *TenantService) GetTenant(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetTenantRequest],
) (*connect.Response[stillhousev1.GetTenantResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	t, err := s.q.GetTenantByID(ctx, u.TenantID)
	if err != nil {
		s.logger.Error("GetTenant", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetTenantResponse{
		Tenant: tenantToProto(t),
	}), nil
}

func (s *TenantService) UpdateTenant(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateTenantRequest],
) (*connect.Response[stillhousev1.UpdateTenantResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetName() == "" || in.GetCraSpiritsLicenceNumber() == "" || in.GetDefaultJurisdiction() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("name, cra_spirits_licence_number, default_jurisdiction are required"))
	}
	ewl := pgtype.Text{Valid: false}
	if v := in.GetExciseWarehouseLicenceNumber(); v != "" {
		ewl = pgtype.Text{String: v, Valid: true}
	}
	t, err := s.q.UpdateTenant(ctx, sqlcgen.UpdateTenantParams{
		ID:                           u.TenantID,
		Name:                         in.GetName(),
		CraSpiritsLicenceNumber:      in.GetCraSpiritsLicenceNumber(),
		ExciseWarehouseLicenceNumber: ewl,
		DefaultJurisdiction:          in.GetDefaultJurisdiction(),
	})
	if err != nil {
		s.logger.Error("UpdateTenant", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateTenantResponse{
		Tenant: tenantToProto(t),
	}), nil
}

// DeleteMyTenant hard-deletes the caller's tenant. Owner-only; requires
// the caller to retype the tenant name as a confirmation. Cascading FKs
// wipe every dependent row.
func (s *TenantService) DeleteMyTenant(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteMyTenantRequest],
) (*connect.Response[stillhousev1.DeleteMyTenantResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if u.Role != sqlcgen.UserRoleOwner {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("owner role required"))
	}
	t, err := s.q.GetTenantByID(ctx, u.TenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("tenant not found"))
		}
		s.logger.Error("DeleteMyTenant: lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if req.Msg.GetConfirmName() != t.Name {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("confirm_name must exactly match the tenant name"))
	}
	if err := s.q.DeleteTenant(ctx, u.TenantID); err != nil {
		s.logger.Error("DeleteMyTenant: delete", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	s.logger.Warn("tenant deleted",
		"tenant_id", u.TenantID.String(),
		"tenant_name", t.Name,
		"by_user", u.ID.String())
	return connect.NewResponse(&stillhousev1.DeleteMyTenantResponse{}), nil
}
