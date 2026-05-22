package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

type AuthService struct {
	q       *sqlcgen.Queries
	session *scs.SessionManager
	logger  *slog.Logger
}

func NewAuthService(q *sqlcgen.Queries, sm *scs.SessionManager, logger *slog.Logger) *AuthService {
	return &AuthService{q: q, session: sm, logger: logger}
}

func (s *AuthService) Login(
	ctx context.Context,
	req *connect.Request[stillhousev1.LoginRequest],
) (*connect.Response[stillhousev1.LoginResponse], error) {
	in := req.Msg
	if in.GetEmail() == "" || in.GetPassword() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email and password are required"))
	}

	u, err := s.q.GetUserByEmail(ctx, in.GetEmail())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
		}
		s.logger.Error("login: user lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if err := auth.VerifyPassword(in.GetPassword(), u.PasswordHash); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}

	t, err := s.q.GetTenantByID(ctx, u.TenantID)
	if err != nil {
		s.logger.Error("login: tenant lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	if err := s.session.RenewToken(ctx); err != nil {
		s.logger.Error("login: renew token", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	s.session.Put(ctx, "user_id", u.ID.String())
	s.session.Put(ctx, "tenant_id", u.TenantID.String())

	return connect.NewResponse(&stillhousev1.LoginResponse{
		User:   userToProto(u),
		Tenant: tenantToProto(t),
	}), nil
}

func (s *AuthService) Logout(
	ctx context.Context,
	req *connect.Request[stillhousev1.LogoutRequest],
) (*connect.Response[stillhousev1.LogoutResponse], error) {
	if err := s.session.Destroy(ctx); err != nil {
		s.logger.Error("logout: destroy session", "err", err)
	}
	return connect.NewResponse(&stillhousev1.LogoutResponse{}), nil
}
