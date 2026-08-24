package rpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

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

	// selfServeSignup removes CreateTenant's bootstrap refusal. Off by
	// default; see config.SelfServeSignup.
	selfServeSignup   bool
	trustProxyHeaders bool
	signupLimiter     *SlidingWindowLimiter
}

func NewTenantService(pool *pgxpool.Pool, q *sqlcgen.Queries, logger *slog.Logger) *TenantService {
	return &TenantService{
		pool: pool, q: q, db: tenantdb.New(pool), logger: logger,
		// Ten new distilleries an hour from one address is far more than
		// a real signup rate and far less than a useful attack.
		signupLimiter: NewSlidingWindowLimiter(10, time.Hour),
	}
}

// WithSelfServeSignup opens tenant creation to the public. Off unless
// called, and the caller is cmd/server reading the operator's config —
// see config.SelfServeSignup.
func (s *TenantService) WithSelfServeSignup(on, trustProxy bool) *TenantService {
	s.selfServeSignup = on
	s.trustProxyHeaders = trustProxy
	return s
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

	// The bootstrap guard, and the one thing self-serve signup changes.
	//
	// CreateTenant is public because a fresh install has nobody to
	// authenticate as. Refusing once a tenant exists is what makes that
	// safe: the endpoint is open for exactly as long as it is useless to
	// an attacker. Self-serve signup removes that refusal by design, so
	// it is off unless the operator turned it on — see config.
	// SelfServeSignup for why Stillhouse cannot decide this itself.
	if !s.selfServeSignup {
		count, err := s.q.CountTenants(ctx)
		if err != nil {
			s.logger.Error("CreateTenant: count", "err", err)
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
		if count > 0 {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("a tenant already exists; this install is already bootstrapped"))
		}
	} else {
		// An open write endpoint needs a budget. Without one, signup is a
		// way to fill somebody's disk from a laptop.
		if !s.signupLimiter.Allow(clientIP(req, s.trustProxyHeaders)) {
			return nil, connect.NewError(connect.CodeResourceExhausted,
				errors.New("too many distilleries created from here; try again later"))
		}
		// And a licence number is not a free-text field once anybody can
		// type one: two tenants claiming one licence would each file a
		// return CRA reads as the same licensee's.
		if taken, err := s.q.LicenceNumberInUse(ctx, in.GetCraSpiritsLicenceNumber()); err != nil {
			s.logger.Error("CreateTenant: licence check", "err", err)
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		} else if taken {
			return nil, connect.NewError(connect.CodeAlreadyExists,
				errors.New("a distillery with that spirits licence number already exists here"))
		}
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
		// Every tenant has exactly one default location, and internal/db
		// asserts it across the whole database. cmd/seed and the invite
		// signup both did this; CreateTenant never has, so a distillery
		// bootstrapped through the RPC started life failing that
		// invariant — invisible until self-serve signup made this path
		// easy to reach.
		//
		// The GUC has to be set here rather than by WithTenantTx, because
		// the tenant does not exist until the statement above: locations
		// FORCES row-level security, so the insert is refused outright
		// without it. Same shape as stage 138's audit lesson, arriving in
		// the one transaction that cannot set the id up front.
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.current_tenant_id', $1, true)", t.ID.String()); err != nil {
			return err
		}
		if _, err := qtx.CreateDefaultLocation(ctx, sqlcgen.CreateDefaultLocationParams{
			TenantID: t.ID, Name: t.Name,
		}); err != nil {
			return err
		}
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
