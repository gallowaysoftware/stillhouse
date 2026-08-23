package rpc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

type UserService struct {
	q *sqlcgen.Queries
	// session is needed by ChangeMyPassword only: writing a password
	// revokes every session the user has, and the caller's own session
	// then has to be re-stamped so the person doing the right thing
	// isn't the one signed out for it.
	session *scs.SessionManager
	logger  *slog.Logger
}

func NewUserService(q *sqlcgen.Queries, session *scs.SessionManager, logger *slog.Logger) *UserService {
	return &UserService{q: q, session: session, logger: logger}
}

func (s *UserService) GetMe(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetMeRequest],
) (*connect.Response[stillhousev1.GetMeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	t, err := s.q.GetTenantByID(ctx, u.TenantID)
	if err != nil {
		s.logger.Error("GetMe: tenant lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetMeResponse{
		User:   userToProto(u),
		Tenant: tenantToProto(t),
	}), nil
}

// CreateUser is owner-only. The server generates a random initial password
// and returns it in plaintext exactly once — the calling owner must deliver
// it to the new user through a secure channel; it is not stored anywhere.
func (s *UserService) CreateUser(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateUserRequest],
) (*connect.Response[stillhousev1.CreateUserResponse], error) {
	caller, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if caller.Role != sqlcgen.UserRoleOwner {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only owners can create users"))
	}
	in := req.Msg
	if in.GetEmail() == "" || in.GetDisplayName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email and display_name are required"))
	}
	role, err := userRoleToDB(in.GetRole())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	pw := generateInitialPassword()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		s.logger.Error("CreateUser: hash", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	created, err := s.q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID:     caller.TenantID,
		Email:        in.GetEmail(),
		PasswordHash: hash,
		DisplayName:  in.GetDisplayName(),
		Role:         role,
	})
	if err != nil {
		s.logger.Error("CreateUser", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if err := audit.Write(ctx, s.q, caller.TenantID, caller.ID, "user", created.ID.String(),
		sqlcgen.AuditActionCreate, map[string]any{
			"email": created.Email, "role": string(created.Role), "display_name": created.DisplayName,
		}); err != nil {
		s.logger.Warn("CreateUser: audit", "err", err)
	}
	return connect.NewResponse(&stillhousev1.CreateUserResponse{
		User:            userToProto(created),
		InitialPassword: pw,
	}), nil
}

// ChangeMyPassword updates the calling user's password after verifying
// the current one.
//
// Changing a password takes something away: UpdateUserPassword writes
// users.sessions_revoked_at in the same statement, which kills every
// session this user holds anywhere — the laptop left at the distillery,
// the phone that was stolen, whoever else was signed in as them. The
// caller's own session is then re-stamped with exactly that watermark so
// they stay signed in; doing the safe thing should not log you out.
//
// API tokens are deliberately *not* revoked here. The caller proved they
// know the current password, so this is routine hygiene rather than
// compromise recovery, and silently killing a rackhouse tablet's MCP
// token would be a surprise. RevokeAllMyAPITokens sits next to this form
// for when it is compromise recovery — and ResetPassword, the flow
// someone reaches for when they think their password has leaked, revokes
// tokens without being asked.
func (s *UserService) ChangeMyPassword(
	ctx context.Context,
	req *connect.Request[stillhousev1.ChangeMyPasswordRequest],
) (*connect.Response[stillhousev1.ChangeMyPasswordResponse], error) {
	caller, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetCurrentPassword() == "" || in.GetNewPassword() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("current_password and new_password are required"))
	}
	if len(in.GetNewPassword()) < 12 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("new_password must be at least 12 characters"))
	}
	if err := auth.VerifyPassword(in.GetCurrentPassword(), caller.PasswordHash); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("current password is incorrect"))
	}
	hash, err := auth.HashPassword(in.GetNewPassword())
	if err != nil {
		s.logger.Error("ChangeMyPassword: hash", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	updated, err := s.q.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		ID:           caller.ID,
		PasswordHash: hash,
	})
	if err != nil {
		s.logger.Error("ChangeMyPassword: update", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	// Re-stamp this session with the watermark the UPDATE just wrote, so
	// it is not-before rather than before it. Every other session the
	// user has authenticated earlier and is now dead.
	if s.session != nil && updated.SessionsRevokedAt.Valid {
		StampSessionAuth(s.session, ctx, updated.SessionsRevokedAt.Time)
	}
	if err := audit.Write(ctx, s.q, caller.TenantID, caller.ID, "user", caller.ID.String(),
		sqlcgen.AuditActionUpdate, map[string]any{
			"event":            "password_changed",
			"sessions_revoked": true,
		}); err != nil {
		s.logger.Warn("ChangeMyPassword: audit", "err", err)
	}
	return connect.NewResponse(&stillhousev1.ChangeMyPasswordResponse{}), nil
}

func (s *UserService) ListUsers(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListUsersRequest],
) (*connect.Response[stillhousev1.ListUsersResponse], error) {
	caller, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	rows, err := s.q.ListUsersForTenant(ctx, caller.TenantID)
	if err != nil {
		s.logger.Error("ListUsers", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.User, 0, len(rows))
	for _, u := range rows {
		out = append(out, userToProto(u))
	}
	return connect.NewResponse(&stillhousev1.ListUsersResponse{Users: out}), nil
}

func generateInitialPassword() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		// Fallback so we never return an empty password; entropy collapse
		// here would be a major system issue we'd see elsewhere.
		return "stillhouse-temporary-please-change"
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func userRoleToDB(r stillhousev1.UserRole) (sqlcgen.UserRole, error) {
	switch r {
	case stillhousev1.UserRole_USER_ROLE_OWNER:
		return sqlcgen.UserRoleOwner, nil
	case stillhousev1.UserRole_USER_ROLE_OPERATOR, stillhousev1.UserRole_USER_ROLE_UNSPECIFIED:
		return sqlcgen.UserRoleOperator, nil
	case stillhousev1.UserRole_USER_ROLE_VIEWER:
		return sqlcgen.UserRoleViewer, nil
	}
	return "", errors.New("invalid user role")
}
