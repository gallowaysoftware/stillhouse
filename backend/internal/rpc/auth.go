package rpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// PasswordResetMailer narrows the mailer.Mailer surface to just what
// AuthService needs. Lets server.go pass the same Mailer here without
// AuthService depending on the package.
type PasswordResetMailer interface {
	SendPasswordReset(ctx context.Context, to, displayName, resetURL string) error
}

type AuthService struct {
	q *sqlcgen.Queries
	// tdb is needed by ResetPassword, which revokes the user's API
	// tokens. api_tokens is under row-level security, so that UPDATE
	// matches nothing at all without a tenant context — silently, since
	// "revoked no tokens" is a legitimate outcome for a user who has
	// none. Found by driving the live server: a token minted before a
	// password reset still authenticated after it.
	tdb          *tenantdb.DB
	session      *scs.SessionManager
	logger       *slog.Logger
	limiter      *SlidingWindowLimiter
	resetLimiter *SlidingWindowLimiter
	// emailLimiter throttles attempts against a single account regardless
	// of where they come from. The primary limiter keys on address AND
	// email, so anything that varies the address — a botnet, or a forged
	// header on a deployment that trusts them — sidesteps it entirely.
	emailLimiter *SlidingWindowLimiter
	// trustProxyHeaders says a trusted reverse proxy sets X-Forwarded-For.
	// Off by default: trusting it unconditionally makes the limiter a
	// formality.
	trustProxyHeaders bool
	mailer            PasswordResetMailer
	resetURLPrefix    string // e.g. https://stillhouse.example.com/reset-password?token=
}

func NewAuthService(q *sqlcgen.Queries, tdb *tenantdb.DB, sm *scs.SessionManager, logger *slog.Logger, mailer PasswordResetMailer, resetURLPrefix string, trustProxyHeaders bool) *AuthService {
	return &AuthService{
		q: q, tdb: tdb, session: sm, logger: logger, mailer: mailer, resetURLPrefix: resetURLPrefix,
		trustProxyHeaders: trustProxyHeaders,
		// 10 attempts / 60s per (remote_ip, email-lowercased) — typical
		// password-guessing attacks need many more attempts than that, but
		// a real user typo'ing won't hit it.
		limiter: NewSlidingWindowLimiter(10, 60*time.Second),
		// Password-reset requests are rate-limited per IP only (the email
		// isn't useful for keying since we don't tell the caller whether
		// it's registered).
		resetLimiter: NewSlidingWindowLimiter(5, 5*time.Minute),
		// 30 attempts / 15 min against one account from anywhere. Well
		// above a real user fumbling a password, well below useful for
		// guessing one.
		emailLimiter: NewSlidingWindowLimiter(30, 15*time.Minute),
	}
}

// loginKey identifies a login attempt for rate-limiting. Combining IP +
// email means an attacker scanning many addresses against one IP gets
// throttled, AND an attacker spreading attempts across IPs against one
// email also gets throttled.
func loginKey(req connect.AnyRequest, email string, trustProxy bool) string {
	return clientIP(req, trustProxy) + "\x00" + strings.ToLower(email)
}

// clientIP identifies the caller for rate-limiting purposes.
//
// Forwarded headers are only consulted when the deployment says a trusted
// proxy sets them. They used to be trusted unconditionally, which made the
// limiter a formality: any client could send a fresh X-Forwarded-For per
// request and never be throttled. That is not hypothetical here — the
// production compose file gives the app its own LAN address, so the
// reverse proxy that would normally overwrite the header can simply be
// stepped around.
//
// The peer address can't be forged over TCP, so it is the default.
func clientIP(req connect.AnyRequest, trustProxy bool) string {
	if trustProxy {
		h := req.Header()
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
	}
	if addr := req.Peer().Addr; addr != "" {
		// Strip the port so repeated attempts from one host share a key.
		if host, _, err := net.SplitHostPort(addr); err == nil {
			return host
		}
		return addr
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

	rlKey := loginKey(req, in.GetEmail(), s.trustProxyHeaders)
	if !s.limiter.Allow(rlKey) {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			errors.New("too many login attempts; try again in a minute"))
	}
	// And a per-account budget, so spreading attempts across addresses
	// doesn't buy an attacker an unlimited number of guesses at one login.
	if !s.emailLimiter.Allow(strings.ToLower(in.GetEmail())) {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			errors.New("too many login attempts for this account; try again later"))
	}

	// An email address can hold an account at more than one distillery
	// (migration 000035), so this is a set, not a row.
	candidates, err := s.q.ListUsersByEmail(ctx, in.GetEmail())
	if err != nil {
		s.logger.Error("login: user lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// If the request named a distillery, only that account is in play.
	// Filtering before verification rather than after keeps the work
	// bounded and means a wrong tenant_id reads as bad credentials rather
	// than as a probe that returns something different.
	if want := in.GetTenantId(); want != "" {
		tenantID, err := uuid.Parse(want)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid tenant_id"))
		}
		filtered := candidates[:0]
		for _, c := range candidates {
			if c.TenantID == tenantID {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}

	// Verify against every candidate. One password may be right at one
	// distillery and wrong at another; the matches are the accounts this
	// caller has actually proven they hold.
	var matched []sqlcgen.User
	for _, c := range candidates {
		if err := auth.VerifyPassword(in.GetPassword(), c.PasswordHash); err == nil {
			matched = append(matched, c)
		}
	}
	if len(matched) == 0 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}

	if len(matched) > 1 {
		// Ambiguous, and only reachable by someone who just proved they
		// hold every account listed — so naming the distilleries tells
		// them nothing they did not already know. No session is created;
		// they pick one and log in again with tenant_id set.
		choices := make([]*stillhousev1.TenantChoice, 0, len(matched))
		for _, m := range matched {
			t, err := s.q.GetTenantByID(ctx, m.TenantID)
			if err != nil {
				s.logger.Error("login: tenant lookup", "err", err, "tenant_id", m.TenantID)
				return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
			}
			choices = append(choices, &stillhousev1.TenantChoice{
				TenantId:   t.ID.String(),
				TenantName: t.Name,
			})
		}
		s.limiter.Forget(rlKey)
		s.emailLimiter.Forget(strings.ToLower(in.GetEmail()))
		return connect.NewResponse(&stillhousev1.LoginResponse{Choices: choices}), nil
	}

	u := matched[0]

	// Second factor, after the password and only after it. Checking it
	// first — or reporting "MFA required" before the password is right —
	// would turn the login form into an oracle for which accounts have
	// one.
	mfaRequired, err := s.verifySecondFactor(ctx, u, in.GetTotpCode(), in.GetRecoveryCode())
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		s.logger.Error("login: second factor", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if mfaRequired {
		// No session, no user, no tenant. The client shows a code field
		// and comes back; nothing about the account is disclosed beyond
		// the fact that the password was right, which the caller
		// supplied.
		s.limiter.Forget(rlKey)
		return connect.NewResponse(&stillhousev1.LoginResponse{MfaRequired: true}), nil
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
	// Records when this session authenticated, so a later password change
	// can tell it apart from one minted before. See SessionSurvivesRevocation.
	StampSessionAuth(s.session, ctx, time.Now())
	s.limiter.Forget(rlKey)
	s.emailLimiter.Forget(strings.ToLower(in.GetEmail())) // legitimate user; reset their counter

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

// RequestPasswordReset always returns success — we never tell the caller
// whether the email was found. On a hit we generate a 32-byte token, store
// its SHA-256 in the DB, and email the plaintext token to the user.
func (s *AuthService) RequestPasswordReset(
	ctx context.Context,
	req *connect.Request[stillhousev1.RequestPasswordResetRequest],
) (*connect.Response[stillhousev1.RequestPasswordResetResponse], error) {
	if !s.resetLimiter.Allow(clientIP(req, s.trustProxyHeaders)) {
		// Quietly succeed even when rate-limited so the caller can't probe
		// for the limit boundary.
		return connect.NewResponse(&stillhousev1.RequestPasswordResetResponse{}), nil
	}
	email := strings.ToLower(strings.TrimSpace(req.Msg.GetEmail()))
	if email == "" {
		return connect.NewResponse(&stillhousev1.RequestPasswordResetResponse{}), nil
	}
	// One address can hold an account at several distilleries, and the
	// person asking has no way to say which one they mean — they are
	// locked out of it. So every account under the address gets its own
	// token and its own email, each naming the distillery it is for.
	users, err := s.q.ListUsersByEmail(ctx, email)
	if err != nil {
		s.logger.Error("password reset: user lookup", "err", err)
		return connect.NewResponse(&stillhousev1.RequestPasswordResetResponse{}), nil
	}
	for _, u := range users {
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			s.logger.Error("password reset: rand", "err", err)
			return connect.NewResponse(&stillhousev1.RequestPasswordResetResponse{}), nil
		}
		token := base64.RawURLEncoding.EncodeToString(tokenBytes)
		hash := sha256.Sum256([]byte(token))
		if _, err := s.q.CreatePasswordResetToken(ctx, sqlcgen.CreatePasswordResetTokenParams{
			TokenHash: hash[:],
			UserID:    u.ID,
			ExpiresAt: pgtype.Timestamptz{Valid: true, Time: time.Now().Add(1 * time.Hour)},
		}); err != nil {
			s.logger.Error("password reset: token insert", "err", err, "user_id", u.ID)
			continue
		}
		if s.mailer == nil {
			continue
		}
		// The display name in the email is qualified by distillery when
		// there is more than one, so two otherwise identical emails are
		// tellable apart by the person holding both accounts.
		name := u.DisplayName
		if len(users) > 1 {
			if t, err := s.q.GetTenantByID(ctx, u.TenantID); err == nil {
				name = u.DisplayName + " (" + t.Name + ")"
			}
		}
		if err := s.mailer.SendPasswordReset(ctx, u.Email, name, s.resetURLPrefix+token); err != nil {
			s.logger.Warn("password reset email failed", "err", err, "to", u.Email)
		}
	}
	return connect.NewResponse(&stillhousev1.RequestPasswordResetResponse{}), nil
}

func (s *AuthService) ResetPassword(
	ctx context.Context,
	req *connect.Request[stillhousev1.ResetPasswordRequest],
) (*connect.Response[stillhousev1.ResetPasswordResponse], error) {
	token := req.Msg.GetToken()
	newPassword := req.Msg.GetNewPassword()
	if token == "" || len(newPassword) < 12 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("token and a password of at least 12 characters are required"))
	}
	hash := sha256.Sum256([]byte(token))
	row, err := s.q.ConsumePasswordResetToken(ctx, hash[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("invalid or expired token"))
		}
		s.logger.Error("password reset: consume", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	pwHash, err := auth.HashPassword(newPassword)
	if err != nil {
		s.logger.Error("password reset: hash", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	// UpdateUserPassword sets sessions_revoked_at in the same statement,
	// so every session this user has anywhere is now dead. Nothing is
	// re-stamped here: the reset flow is anonymous by construction, there
	// is no session of ours to preserve, and locking out whoever else
	// held one is the entire point.
	if _, err := s.q.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		ID: row.UserID, PasswordHash: pwHash,
	}); err != nil {
		s.logger.Error("password reset: update", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	// API tokens go too. This is the recovery path — the one a person
	// reaches for when they think their password has leaked — and a token
	// minted with the leaked password outliving the reset is precisely
	// the hole being closed. A token the user still wants is one Issue
	// away; a token an attacker minted is not worth the convenience.
	//
	// Through WithTenantTx, because api_tokens is under RLS: run without
	// a tenant context and the UPDATE matches nothing and reports
	// success, which is how this shipped broken the first time.
	user, err := s.q.GetUserByID(ctx, row.UserID)
	if err != nil {
		s.logger.Error("password reset: user lookup", "err", err, "user_id", row.UserID)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	var revoked int
	if err := s.tdb.WithTenantTx(ctx, user.TenantID,
		func(ctx context.Context, q *sqlcgen.Queries) error {
			rows, err := q.RevokeAllAPITokensForUser(ctx, row.UserID)
			revoked = len(rows)
			return err
		}); err != nil {
		s.logger.Error("password reset: revoke tokens", "err", err, "user_id", row.UserID)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if revoked > 0 {
		s.logger.Info("password reset revoked api tokens",
			"user_id", row.UserID, "count", revoked)
	}
	return connect.NewResponse(&stillhousev1.ResetPasswordResponse{}), nil
}
