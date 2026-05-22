package rpc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	limiter *SlidingWindowLimiter
}

func NewAuthService(q *sqlcgen.Queries, sm *scs.SessionManager, logger *slog.Logger) *AuthService {
	return &AuthService{
		q: q, session: sm, logger: logger,
		// 10 attempts / 60s per (remote_ip, email-lowercased) — typical
		// password-guessing attacks need many more attempts than that, but
		// a real user typo'ing won't hit it.
		limiter: NewSlidingWindowLimiter(10, 60*time.Second),
	}
}

// loginKey identifies a login attempt for rate-limiting. Combining IP +
// email means an attacker scanning many addresses against one IP gets
// throttled, AND an attacker spreading attempts across IPs against one
// email also gets throttled.
func loginKey(req connect.AnyRequest, email string) string {
	ip := clientIP(req.Header())
	return ip + "\x00" + strings.ToLower(email)
}

func clientIP(h http.Header) string {
	if v := h.Get("X-Forwarded-For"); v != "" {
		// First entry in XFF is the original client; trim whitespace.
		if i := strings.Index(v, ","); i >= 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := h.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	return "unknown"
}

func (s *AuthService) Login(
	ctx context.Context,
	req *connect.Request[stillhousev1.LoginRequest],
) (*connect.Response[stillhousev1.LoginResponse], error) {
	in := req.Msg
	if in.GetEmail() == "" || in.GetPassword() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email and password are required"))
	}

	rlKey := loginKey(req, in.GetEmail())
	if !s.limiter.Allow(rlKey) {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			errors.New("too many login attempts; try again in a minute"))
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
	s.limiter.Forget(rlKey) // legitimate user; reset their counter

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
