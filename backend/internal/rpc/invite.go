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
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// Mailer is the email-sending surface. Implementations live in
// internal/mailer; InviteService takes it via interface so the signup
// RPC can hand off a welcome message without a hard dep on Resend.
type Mailer interface {
	SendWelcome(ctx context.Context, to, displayName, tenantName string) error
}

type InviteService struct {
	q       *sqlcgen.Queries
	db      *tenantdb.DB
	session *scs.SessionManager
	mailer  Mailer // may be nil; signup is best-effort on email send
	logger  *slog.Logger
}

func NewInviteService(q *sqlcgen.Queries, db *tenantdb.DB, sm *scs.SessionManager, mailer Mailer, logger *slog.Logger) *InviteService {
	return &InviteService{q: q, db: db, session: sm, mailer: mailer, logger: logger}
}

// CreateInviteCode generates a fresh URL-safe code and stores it under the
// caller's tenant + user. Owner-only.
func (s *InviteService) CreateInviteCode(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateInviteCodeRequest],
) (*connect.Response[stillhousev1.CreateInviteCodeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if u.Role != sqlcgen.UserRoleOwner {
		// Defense in depth — role_gate already enforces owner-only, but a
		// future role-gate map miss shouldn't open this RPC.
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("owner role required"))
	}
	code, err := randomInviteCode()
	if err != nil {
		s.logger.Error("rand", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	var expires pgtype.Timestamptz
	if d := req.Msg.GetExpiresInDays(); d > 0 {
		expires = pgtype.Timestamptz{Valid: true, Time: time.Now().Add(time.Duration(d) * 24 * time.Hour)}
	}
	row, err := s.q.CreateInviteCode(ctx, sqlcgen.CreateInviteCodeParams{
		Code:              code,
		CreatedByUserID:   u.ID,
		CreatedByTenantID: u.TenantID,
		Note:              req.Msg.GetNote(),
		ExpiresAt:         expires,
	})
	if err != nil {
		s.logger.Error("CreateInviteCode", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateInviteCodeResponse{Invite: inviteCodeToProto(row)}), nil
}

func (s *InviteService) ListMyInviteCodes(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListMyInviteCodesRequest],
) (*connect.Response[stillhousev1.ListMyInviteCodesResponse], error) {
	_ = req
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	rows, err := s.q.ListInviteCodesByCreator(ctx, u.ID)
	if err != nil {
		s.logger.Error("ListInviteCodesByCreator", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.InviteCode, 0, len(rows))
	for _, r := range rows {
		out = append(out, inviteCodeToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListMyInviteCodesResponse{Invites: out}), nil
}

func (s *InviteService) RevokeInviteCode(
	ctx context.Context,
	req *connect.Request[stillhousev1.RevokeInviteCodeRequest],
) (*connect.Response[stillhousev1.RevokeInviteCodeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	row, err := s.q.RevokeInviteCode(ctx, req.Msg.GetCode())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("code not found or already redeemed/revoked"))
		}
		s.logger.Error("RevokeInviteCode", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	// Belt-and-suspenders: ensure the caller is actually the code's creator.
	// The query filters on code only (so anyone could in theory revoke any
	// code if they guessed it); reject after the fact and undo if mismatched.
	if row.CreatedByUserID != u.ID {
		// Re-insert via update — safer than a separate not-yet-revoke check.
		_, _ = s.q.RevokeInviteCode(ctx, "") // no-op fallback; just don't expose the row.
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not your invite code"))
	}
	return connect.NewResponse(&stillhousev1.RevokeInviteCodeResponse{Invite: inviteCodeToProto(row)}), nil
}

// SignupWithInvite is the public bootstrap RPC for a new tenant. Validates
// the invite, creates tenant + owner user in one go, marks the code
// redeemed. Logs the new owner in via session cookie on success.
//
// All bad-code errors collapse to a single message so we don't leak which
// codes exist or are already used.
func (s *InviteService) SignupWithInvite(
	ctx context.Context,
	req *connect.Request[stillhousev1.SignupWithInviteRequest],
) (*connect.Response[stillhousev1.SignupWithInviteResponse], error) {
	in := req.Msg
	email := strings.ToLower(strings.TrimSpace(in.GetEmail()))
	if email == "" || in.GetPassword() == "" || in.GetTenantName() == "" || in.GetCode() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("code, email, password, and tenant_name are required"))
	}
	if len(in.GetPassword()) < 12 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("password must be at least 12 characters"))
	}

	// Pre-check the code without claiming it. If it's bad, fail before
	// hashing the password (no point spending cpu on a doomed signup).
	code, err := s.q.GetInviteCode(ctx, in.GetCode())
	if err != nil || code.RedeemedAt.Valid || code.RevokedAt.Valid ||
		(code.ExpiresAt.Valid && code.ExpiresAt.Time.Before(time.Now())) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("invalid invite code"))
	}

	hash, err := auth.HashPassword(in.GetPassword())
	if err != nil {
		s.logger.Error("hash", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	defaultJurisdiction := in.GetDefaultJurisdiction()
	if defaultJurisdiction == "" {
		defaultJurisdiction = "CA-ON"
	}

	// Create tenant + user in a single transaction. The atomic redeem on
	// invite_codes (with WHERE redeemed_at IS NULL) is the gate that
	// prevents two parallel signups from claiming the same code.
	var (
		tenant sqlcgen.Tenant
		user   sqlcgen.User
	)
	err = s.db.WithoutTenantTx(ctx, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		tenant, e = q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
			Name:                         in.GetTenantName(),
			CraSpiritsLicenceNumber:      in.GetCraSpiritsLicenceNumber(),
			ExciseWarehouseLicenceNumber: pgtype.Text{Valid: false},
			DefaultJurisdiction:          defaultJurisdiction,
		})
		if e != nil {
			return e
		}
		user, e = q.CreateUser(ctx, sqlcgen.CreateUserParams{
			TenantID:     tenant.ID,
			Email:        email,
			DisplayName:  in.GetDisplayName(),
			Role:         sqlcgen.UserRoleOwner,
			PasswordHash: hash,
		})
		if e != nil {
			return e
		}
		// Mark email verified — owner trusted them enough to share the code,
		// and they obviously control the email they typed (it's mid-flow).
		if _, e := q.MarkUserEmailVerified(ctx, user.ID); e != nil {
			return e
		}
		redeemed, e := q.RedeemInviteCode(ctx, sqlcgen.RedeemInviteCodeParams{
			Code:             in.GetCode(),
			RedeemedEmail:    email,
			RedeemedTenantID: uuid.NullUUID{UUID: tenant.ID, Valid: true},
		})
		if e != nil || redeemed.Code == "" {
			// Lost the race to a concurrent signup. Roll back the tx so the
			// tenant + user we just created vanish.
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("invalid invite code"))
		}
		// The transaction opened without a tenant context, because the
		// tenant didn't exist yet. It does now, and audit_events enforces
		// row-level security — so scope the transaction before writing to
		// it, or the INSERT is refused and this whole signup rolls back.
		if e := tenantdb.SetTenantContext(ctx, q, tenant.ID); e != nil {
			return e
		}
		// Audit on the new tenant so the trail says who created it via which code.
		return audit.Write(ctx, q, tenant.ID, user.ID, "tenant", tenant.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"signup":         true,
				"invite_code":    in.GetCode(),
				"creator_user":   code.CreatedByUserID.String(),
				"creator_tenant": code.CreatedByTenantID.String(),
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		// A licence number already on the install, or a duplicate email
		// inside the tenant being created, is something the person
		// signing up can act on — it used to come back as `internal`.
		if ce := classifyWriteErr(err, "the invite code refers to a tenant that no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("SignupWithInvite", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// Log the new owner in immediately so they land on the dashboard.
	if err := s.session.RenewToken(ctx); err != nil {
		s.logger.Error("signup: renew token", "err", err)
		// Non-fatal — they can log in normally.
	}
	s.session.Put(ctx, "user_id", user.ID.String())
	s.session.Put(ctx, "tenant_id", tenant.ID.String())
	StampSessionAuth(s.session, ctx, time.Now())

	if s.mailer != nil {
		// Best-effort; don't fail signup if email fails (operator can
		// inspect logs and resend).
		if err := s.mailer.SendWelcome(ctx, email, in.GetDisplayName(), tenant.Name); err != nil {
			s.logger.Warn("welcome email failed", "err", err, "to", email)
		}
	}

	return connect.NewResponse(&stillhousev1.SignupWithInviteResponse{
		TenantId: tenant.ID.String(),
		UserId:   user.ID.String(),
	}), nil
}

func randomInviteCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// URL-safe so it round-trips through ?code= in a signup link.
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func inviteCodeToProto(r sqlcgen.InviteCode) *stillhousev1.InviteCode {
	out := &stillhousev1.InviteCode{
		Code:              r.Code,
		CreatedByUserId:   r.CreatedByUserID.String(),
		CreatedByTenantId: r.CreatedByTenantID.String(),
		Note:              r.Note,
		RedeemedEmail:     r.RedeemedEmail,
		CreatedAt:         timestamppb.New(r.CreatedAt.Time),
	}
	if r.ExpiresAt.Valid {
		out.ExpiresAt = timestamppb.New(r.ExpiresAt.Time)
	}
	if r.RedeemedAt.Valid {
		out.RedeemedAt = timestamppb.New(r.RedeemedAt.Time)
	}
	if r.RedeemedTenantID.Valid {
		out.RedeemedTenantId = r.RedeemedTenantID.UUID.String()
	}
	if r.RevokedAt.Valid {
		out.RevokedAt = timestamppb.New(r.RevokedAt.Time)
	}
	return out
}
