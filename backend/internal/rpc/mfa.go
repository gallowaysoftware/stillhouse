package rpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/secrets"
	"github.com/gallowaysoftware/stillhouse/backend/internal/totp"
)

// recoveryCodeCount is how many are issued at confirmation. Ten is
// enough to survive a lost phone and a couple of fumbles without
// becoming a list somebody stops treating as a credential.
const recoveryCodeCount = 10

// totpSkew accepts the neighbouring steps — ninety seconds of tolerance
// in total. That is the allowance for a phone whose clock has drifted
// and for a person still typing when the window turns; it is not
// tolerance for a code somebody wrote down, which is what the replay
// guard is for.
const totpSkew = 1

// MFAStatus reports what the calling user has set up, and whether the
// install can offer it at all.
func (s *AuthService) MFAStatus(
	ctx context.Context,
	_ *connect.Request[stillhousev1.MFAStatusRequest],
) (*connect.Response[stillhousev1.MFAStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out := &stillhousev1.MFAStatusResponse{Available: secrets.Configured()}
	if !out.Available {
		out.UnavailableReason = secrets.ConfigErr().Error()
	}
	row, err := s.q.GetUserTOTP(ctx, u.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewResponse(out), nil
		}
		s.logger.Error("MFAStatus", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out.Enabled = row.ConfirmedAt.Valid
	out.Pending = !row.ConfirmedAt.Valid
	if out.Enabled {
		n, err := s.q.CountUnusedTOTPRecoveryCodes(ctx, u.ID)
		if err != nil {
			s.logger.Error("MFAStatus: recovery codes", "err", err)
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
		out.RecoveryCodesRemaining = n
	}
	return connect.NewResponse(out), nil
}

// BeginMFAEnrolment mints a secret and hands back the enrolment URI.
// Nothing is enforced yet — see ConfirmMFAEnrolment.
func (s *AuthService) BeginMFAEnrolment(
	ctx context.Context,
	_ *connect.Request[stillhousev1.BeginMFAEnrolmentRequest],
) (*connect.Response[stillhousev1.BeginMFAEnrolmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	// Refuse rather than store a second factor in the clear. Same
	// discipline as the alcoholometric tables: the feature that cannot
	// be done correctly says so instead of doing it approximately.
	if !secrets.Configured() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, secrets.ConfigErr())
	}

	secret, err := totp.NewSecret()
	if err != nil {
		s.logger.Error("BeginMFAEnrolment: rand", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	sealed, err := secrets.Seal(secret)
	if err != nil {
		s.logger.Error("BeginMFAEnrolment: seal", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// The upsert refuses to overwrite a *confirmed* row, so a working
	// second factor cannot be replaced by starting a new enrolment. No
	// rows back means one is already set up.
	if _, err := s.q.UpsertUserTOTP(ctx, sqlcgen.UpsertUserTOTPParams{
		UserID: u.ID, SecretSealed: sealed,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("two-factor authentication is already set up; disable it before enrolling again"))
		}
		s.logger.Error("BeginMFAEnrolment: store", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	issuer := "Stillhouse"
	if t, err := s.q.GetTenantByID(ctx, u.TenantID); err == nil && t.Name != "" {
		// Named per distillery, because a phone holding accounts for two
		// of them shows only this label to tell them apart.
		issuer = "Stillhouse " + t.Name
	}
	return connect.NewResponse(&stillhousev1.BeginMFAEnrolmentResponse{
		EnrolmentUri: totp.EnrolmentURI(issuer, u.Email, secret),
		Secret:       totp.EncodeSecret(secret),
	}), nil
}

// ConfirmMFAEnrolment turns the second factor on, but only once the app
// has produced a code that matches. Enrolling in one step means a
// mistyped secret locks the account at the next sign-in — the failure
// lands on the person who did everything right.
func (s *AuthService) ConfirmMFAEnrolment(
	ctx context.Context,
	req *connect.Request[stillhousev1.ConfirmMFAEnrolmentRequest],
) (*connect.Response[stillhousev1.ConfirmMFAEnrolmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	row, err := s.q.GetUserTOTP(ctx, u.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("start enrolment first"))
		}
		s.logger.Error("ConfirmMFAEnrolment: read", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if row.ConfirmedAt.Valid {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("two-factor authentication is already set up"))
	}
	secret, err := secrets.Open(row.SecretSealed)
	if err != nil {
		s.logger.Error("ConfirmMFAEnrolment: open", "err", err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	step, valid := totp.Validate(secret, req.Msg.GetCode(), time.Now(), totpSkew)
	if !valid {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("that code doesn't match — check the app's clock and try the next one"))
	}
	if _, err := s.q.ConfirmUserTOTP(ctx, sqlcgen.ConfirmUserTOTPParams{
		UserID: u.ID, LastUsedStep: pgtype.Int8{Int64: step, Valid: true},
	}); err != nil {
		s.logger.Error("ConfirmMFAEnrolment: confirm", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	codes, err := s.issueRecoveryCodes(ctx, u.ID)
	if err != nil {
		s.logger.Error("ConfirmMFAEnrolment: recovery codes", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	s.logger.Info("mfa enabled", "user_id", u.ID)
	return connect.NewResponse(&stillhousev1.ConfirmMFAEnrolmentResponse{RecoveryCodes: codes}), nil
}

// DisableMFA turns the second factor off. It requires the current
// password: a second factor that an already-hijacked session can switch
// off silently has not added a factor.
func (s *AuthService) DisableMFA(
	ctx context.Context,
	req *connect.Request[stillhousev1.DisableMFARequest],
) (*connect.Response[stillhousev1.DisableMFAResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if err := auth.VerifyPassword(req.Msg.GetCurrentPassword(), u.PasswordHash); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("current password is incorrect"))
	}
	if err := s.q.DeleteTOTPRecoveryCodes(ctx, u.ID); err != nil {
		s.logger.Error("DisableMFA: recovery codes", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if err := s.q.DeleteUserTOTP(ctx, u.ID); err != nil {
		s.logger.Error("DisableMFA", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	s.logger.Info("mfa disabled", "user_id", u.ID)
	return connect.NewResponse(&stillhousev1.DisableMFAResponse{}), nil
}

// issueRecoveryCodes replaces any existing codes with a fresh set and
// returns the plaintext, which is shown once. Only the SHA-256 is
// stored, same as every other credential here.
func (s *AuthService) issueRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if err := s.q.DeleteTOTPRecoveryCodes(ctx, userID); err != nil {
		return nil, err
	}
	out := make([]string, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(normaliseRecoveryCode(code)))
		if err := s.q.CreateTOTPRecoveryCode(ctx, sqlcgen.CreateTOTPRecoveryCodeParams{
			CodeHash: sum[:], UserID: userID,
		}); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, nil
}

// newRecoveryCode returns something a person can read off paper and type
// back. Crockford-ish base32 from the standard alphabet, hyphenated,
// with 50 bits of entropy — far beyond guessing, and short enough to
// write down without a mistake.
func newRecoveryCode() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return s[:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16], nil
}

// normaliseRecoveryCode makes typing forgiving: case and hyphens are
// presentation, not content.
func normaliseRecoveryCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
}

// verifySecondFactor is the login-time check. It returns nil when the
// account has no second factor, or when the supplied code satisfies it.
//
// Everything about the shape here is deliberate. The check runs only
// after the password has already been verified, so a wrong password
// never reveals whether an account has MFA at all. The replay guard
// refuses a step at or below the last one accepted, which is what stops
// a code being reused inside its ninety-second window. And a recovery
// code is consumed by the UPDATE rather than by a read-then-write, so
// two tabs submitting the same one cannot both succeed.
func (s *AuthService) verifySecondFactor(
	ctx context.Context, u sqlcgen.User, code, recovery string,
) (mfaRequired bool, err error) {
	row, err := s.q.GetUserTOTP(ctx, u.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // no second factor on this account
		}
		return false, err
	}
	// An enrolment that was started and never confirmed must not gate a
	// login. Someone who scanned a code and closed the tab has not set
	// up a second factor, and locking them out for it would be our bug.
	if !row.ConfirmedAt.Valid {
		return false, nil
	}

	if rc := strings.TrimSpace(recovery); rc != "" {
		sum := sha256.Sum256([]byte(normaliseRecoveryCode(rc)))
		if _, err := s.q.ConsumeTOTPRecoveryCode(ctx, sqlcgen.ConsumeTOTPRecoveryCodeParams{
			CodeHash: sum[:], UserID: u.ID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return true, connect.NewError(connect.CodeUnauthenticated,
					errors.New("that recovery code is not valid, or has already been used"))
			}
			return true, err
		}
		s.logger.Info("mfa satisfied by recovery code", "user_id", u.ID)
		return false, nil
	}

	if strings.TrimSpace(code) == "" {
		return true, nil // ask for it
	}
	secret, err := secrets.Open(row.SecretSealed)
	if err != nil {
		return true, err
	}
	step, valid := totp.Validate(secret, code, time.Now(), totpSkew)
	if !valid {
		return true, connect.NewError(connect.CodeUnauthenticated,
			errors.New("that code is not valid"))
	}
	if row.LastUsedStep.Valid && step <= row.LastUsedStep.Int64 {
		return true, connect.NewError(connect.CodeUnauthenticated,
			fmt.Errorf("that code has already been used; wait for the next one"))
	}
	if err := s.q.RecordTOTPStep(ctx, sqlcgen.RecordTOTPStepParams{
		UserID: u.ID, LastUsedStep: pgtype.Int8{Int64: step, Valid: true},
	}); err != nil {
		return true, err
	}
	return false, nil
}
