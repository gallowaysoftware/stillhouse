package rpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// ListMyDistilleries names the accounts this email holds. PLAN H7.
//
// It discloses to a signed-in session that the same address has an
// account elsewhere. That is a deliberate and bounded disclosure: the
// caller has already authenticated as this address, an account created
// under someone's address is a thing they are entitled to know about, and
// the list is useless without the target's own password. It does not
// disclose anything to anyone who is not already signed in as this user.
func (s *AuthService) ListMyDistilleries(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListMyDistilleriesRequest],
) (*connect.Response[stillhousev1.ListMyDistilleriesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	accounts, err := s.q.ListUsersByEmail(ctx, u.Email)
	if err != nil {
		s.logger.Error("ListMyDistilleries: lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.ListMyDistilleriesResponse{}
	for _, a := range accounts {
		t, err := s.q.GetTenantByID(ctx, a.TenantID)
		if err != nil {
			s.logger.Error("ListMyDistilleries: tenant", "err", err, "tenant_id", a.TenantID)
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
		out.Distilleries = append(out.Distilleries, &stillhousev1.MyDistillery{
			TenantId:   t.ID.String(),
			TenantName: t.Name,
			Current:    a.TenantID == u.TenantID,
			Role:       string(a.Role),
		})
	}
	return connect.NewResponse(out), nil
}

// SwitchDistillery re-authenticates an existing session against another of
// the caller's accounts.
//
// Everything security-relevant about Login applies here unchanged, because
// this IS a login — it just keeps the browser where it is instead of
// bouncing through the sign-in page. In particular the password is checked
// against the TARGET account, never against the one already signed in: an
// email may hold accounts with different passwords, so a session at one
// proves nothing about another.
func (s *AuthService) SwitchDistillery(
	ctx context.Context,
	req *connect.Request[stillhousev1.SwitchDistilleryRequest],
) (*connect.Response[stillhousev1.SwitchDistilleryResponse], error) {
	cur, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetPassword() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("password is required"))
	}
	tenantID, err := uuid.Parse(in.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid tenant_id"))
	}
	if tenantID == cur.TenantID {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("already signed in to that distillery"))
	}

	// Same per-account budget as Login. Without it, an authenticated
	// session would be an unmetered oracle for guessing the password of a
	// sibling account.
	if !s.emailLimiter.Allow(strings.ToLower(cur.Email)) {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			errors.New("too many attempts for this account; try again later"))
	}

	accounts, err := s.q.ListUsersByEmail(ctx, cur.Email)
	if err != nil {
		s.logger.Error("SwitchDistillery: lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	var target *sqlcgen.User
	for i := range accounts {
		if accounts[i].TenantID == tenantID {
			target = &accounts[i]
			break
		}
	}
	// A tenant this address holds no account at, and a wrong password,
	// answer identically. Otherwise the switcher becomes a way to ask
	// which distilleries a given address is registered at.
	if target == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}
	if err := auth.VerifyPassword(in.GetPassword(), target.PasswordHash); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}

	// The target's second factor, not the current session's. Skipping it
	// because the session already exists would make switching a route
	// into an MFA-protected account without its MFA.
	mfaRequired, err := s.verifySecondFactor(ctx, *target, in.GetTotpCode(), in.GetRecoveryCode())
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		s.logger.Error("SwitchDistillery: second factor", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if mfaRequired {
		// Nothing has changed; the session is still on the old account.
		return connect.NewResponse(&stillhousev1.SwitchDistilleryResponse{MfaRequired: true}), nil
	}

	t, err := s.q.GetTenantByID(ctx, target.TenantID)
	if err != nil {
		s.logger.Error("SwitchDistillery: tenant", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// New token for the new identity, exactly as Login does. Reusing the
	// old one would leave a token that was minted for one account
	// carrying another — which is the session-fixation shape, and also
	// leaves the old tenant's id valid in anything that cached it.
	if err := s.session.RenewToken(ctx); err != nil {
		s.logger.Error("SwitchDistillery: renew token", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	s.session.Put(ctx, "user_id", target.ID.String())
	s.session.Put(ctx, "tenant_id", target.TenantID.String())
	StampSessionAuth(s.session, ctx, time.Now())
	s.emailLimiter.Forget(strings.ToLower(cur.Email))

	return connect.NewResponse(&stillhousev1.SwitchDistilleryResponse{
		User:   userToProto(*target),
		Tenant: tenantToProto(t),
	}), nil
}
