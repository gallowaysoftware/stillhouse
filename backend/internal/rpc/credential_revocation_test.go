package rpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// newSessionCtx returns a context carrying a live scs session, so the
// watermark logic can be exercised the way a request exercises it.
func newSessionCtx(t *testing.T) (*scs.SessionManager, context.Context) {
	t.Helper()
	sm := scs.New()
	ctx, err := sm.Load(context.Background(), "")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	return sm, ctx
}

func userWithWatermark(at time.Time, set bool) sqlcgen.User {
	return sqlcgen.User{
		ID:                uuid.New(),
		SessionsRevokedAt: pgtype.Timestamptz{Time: at, Valid: set},
	}
}

// The watermark decides which sessions die. Each case here is a session
// state a real deployment will produce.
func TestSessionSurvivesRevocation(t *testing.T) {
	now := time.Now().UTC()

	t.Run("no watermark leaves every session alone", func(t *testing.T) {
		sm, ctx := newSessionCtx(t)
		StampSessionAuth(sm, ctx, now.Add(-time.Hour))
		if !SessionSurvivesRevocation(sm, ctx, userWithWatermark(time.Time{}, false)) {
			t.Error("session died with no revocation on record")
		}
	})

	t.Run("session older than the watermark dies", func(t *testing.T) {
		sm, ctx := newSessionCtx(t)
		StampSessionAuth(sm, ctx, now.Add(-time.Hour))
		if SessionSurvivesRevocation(sm, ctx, userWithWatermark(now, true)) {
			t.Error("a session that authenticated before the password change survived it")
		}
	})

	t.Run("session newer than the watermark stands", func(t *testing.T) {
		sm, ctx := newSessionCtx(t)
		StampSessionAuth(sm, ctx, now.Add(time.Minute))
		if !SessionSurvivesRevocation(sm, ctx, userWithWatermark(now, true)) {
			t.Error("a session created after the password change was killed by it")
		}
	})

	t.Run("session stamped exactly at the watermark stands", func(t *testing.T) {
		// This is ChangeMyPassword re-stamping its own session with the
		// value the UPDATE returned. If the comparison were strict, the
		// person who just changed their password would be signed out by
		// their own action.
		sm, ctx := newSessionCtx(t)
		StampSessionAuth(sm, ctx, now)
		if !SessionSurvivesRevocation(sm, ctx, userWithWatermark(now, true)) {
			t.Error("the caller's own re-stamped session was revoked by its own change")
		}
	})

	t.Run("unstamped session dies once anything is revoked", func(t *testing.T) {
		// A session issued before stage 154 carries no stamp. It was
		// minted under the password being replaced, so it must not
		// outlive it — the safe direction for an unknown.
		sm, ctx := newSessionCtx(t)
		if SessionSurvivesRevocation(sm, ctx, userWithWatermark(now, true)) {
			t.Error("a session with no authentication stamp survived a revocation")
		}
	})
}

// The database half: writing a password must set the watermark in the
// same statement, because a caller that updates the hash and forgets is
// exactly the bug being fixed.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestUpdateUserPasswordSetsTheWatermark(t *testing.T) {
	f := newLedgerFixture(t)
	if f.user.SessionsRevokedAt.Valid {
		t.Fatal("a freshly created user already has a revocation watermark")
	}
	before := time.Now().Add(-time.Second)
	updated, err := f.q.UpdateUserPassword(f.ctx, sqlcgen.UpdateUserPasswordParams{
		ID: f.user.ID, PasswordHash: "new-hash",
	})
	if err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	if !updated.SessionsRevokedAt.Valid {
		t.Fatal("writing a password left sessions_revoked_at unset — every existing session survives")
	}
	if updated.SessionsRevokedAt.Time.Before(before) {
		t.Errorf("watermark %v predates the update", updated.SessionsRevokedAt.Time)
	}
}

// A token past its expiry must stop authenticating, and the check has to
// live in the keyhole function rather than in Go, because the keyhole is
// the only path a bearer token is resolved through.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestExpiredTokensDoNotAuthenticate(t *testing.T) {
	f := newLedgerFixture(t)
	issue := func(t *testing.T, name string, expiresAt pgtype.Timestamptz) string {
		t.Helper()
		plaintext, hash, err := generateAPIToken()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if _, err := f.q.CreateAPIToken(f.ctx, sqlcgen.CreateAPITokenParams{
			TokenHash: hash, TenantID: f.tenant.ID, UserID: f.user.ID,
			Name: name, ExpiresAt: expiresAt,
		}); err != nil {
			t.Fatalf("create token: %v", err)
		}
		return plaintext
	}

	live := issue(t, "live", pgtype.Timestamptz{Valid: true, Time: time.Now().Add(time.Hour)})
	dead := issue(t, "expired", pgtype.Timestamptz{Valid: true, Time: time.Now().Add(-time.Hour)})
	forever := issue(t, "no expiry", pgtype.Timestamptz{})

	if _, err := LookupBearer(f.ctx, f.q, live); err != nil {
		t.Errorf("an unexpired token was refused: %v", err)
	}
	if _, err := LookupBearer(f.ctx, f.q, forever); err != nil {
		t.Errorf("a token with no expiry was refused: %v", err)
	}
	if _, err := LookupBearer(f.ctx, f.q, dead); err == nil {
		t.Error("an expired token still authenticates")
	}
}

// Revoking everything is the action that sits next to the password form.
// It has to take out every live token the caller holds and report an
// honest count.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestRevokeAllMyAPITokens(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewAPITokenService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	for i := 0; i < 3; i++ {
		if _, err := svc.IssueAPIToken(f.ctx, connect.NewRequest(
			&stillhousev1.IssueAPITokenRequest{Name: "tok"})); err != nil {
			t.Fatalf("issue: %v", err)
		}
	}
	resp, err := svc.RevokeAllMyAPITokens(f.ctx, connect.NewRequest(
		&stillhousev1.RevokeAllMyAPITokensRequest{}))
	if err != nil {
		t.Fatalf("RevokeAllMyAPITokens: %v", err)
	}
	if got := resp.Msg.GetRevokedCount(); got != 3 {
		t.Errorf("revoked %d tokens, want 3", got)
	}
	// Idempotent, and the count stays honest: nothing left to revoke.
	again, err := svc.RevokeAllMyAPITokens(f.ctx, connect.NewRequest(
		&stillhousev1.RevokeAllMyAPITokensRequest{}))
	if err != nil {
		t.Fatalf("second RevokeAllMyAPITokens: %v", err)
	}
	if got := again.Msg.GetRevokedCount(); got != 0 {
		t.Errorf("second sweep revoked %d tokens, want 0", got)
	}

	list, err := svc.ListAPITokens(f.ctx, connect.NewRequest(&stillhousev1.ListAPITokensRequest{}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, tok := range list.Msg.GetTokens() {
		if tok.GetRevokedAt() == nil {
			t.Errorf("token %q survived the sweep", tok.GetName())
		}
	}
}

// A token issued with no explicit lifetime must still get one. The
// default is the difference between a credential that ages out and one
// that sits in a script forever.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestIssuedTokensExpireByDefault(t *testing.T) {
	f := newLedgerFixture(t)
	svc := NewAPITokenService(f.db, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	resp, err := svc.IssueAPIToken(f.ctx, connect.NewRequest(
		&stillhousev1.IssueAPITokenRequest{Name: "default lifetime"}))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	exp := resp.Msg.GetToken().GetExpiresAt()
	if exp == nil {
		t.Fatal("a token issued with no lifetime given never expires")
	}
	want := time.Now().AddDate(0, 0, defaultTokenLifetimeDays)
	if d := exp.AsTime().Sub(want); d > time.Minute || d < -time.Minute {
		t.Errorf("default expiry %v is not ~%d days out", exp.AsTime(), defaultTokenLifetimeDays)
	}

	never, err := svc.IssueAPIToken(f.ctx, connect.NewRequest(
		&stillhousev1.IssueAPITokenRequest{Name: "standing", NeverExpires: true}))
	if err != nil {
		t.Fatalf("issue never-expires: %v", err)
	}
	if never.Msg.GetToken().GetExpiresAt() != nil {
		t.Error("never_expires produced a token with an expiry")
	}

	if _, err := svc.IssueAPIToken(f.ctx, connect.NewRequest(
		&stillhousev1.IssueAPITokenRequest{Name: "too long", ExpiresInDays: maxTokenLifetimeDays + 1},
	)); err == nil {
		t.Error("a lifetime beyond the cap was accepted")
	}
}

// A password reset must take the user's API tokens with it. This is the
// flow someone reaches for when they believe their password has leaked,
// and a token minted with that password outliving the reset is the
// original hole.
//
// The first cut of this shipped broken and a unit test would not have
// caught it: api_tokens is under row-level security, so the revoking
// UPDATE ran with no tenant context, matched zero rows, and returned
// success. Driving the live server found it — a token issued before the
// reset still authenticated after it. This test pins the fix by asserting
// on the token rather than on the call's error.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestPasswordResetRevokesAPITokens(t *testing.T) {
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	auth := NewAuthService(f.q, f.db, scs.New(), log, nil, "http://example.test/r?token=", false)
	tokens := NewAPITokenService(f.db, log)

	issued, err := tokens.IssueAPIToken(f.ctx, connect.NewRequest(
		&stillhousev1.IssueAPITokenRequest{Name: "pre-reset"}))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	plaintext := issued.Msg.GetPlaintext()
	if _, err := LookupBearer(f.ctx, f.q, plaintext); err != nil {
		t.Fatalf("freshly issued token does not authenticate: %v", err)
	}

	// Mint a reset token the way RequestPasswordReset does.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand: %v", err)
	}
	resetToken := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(resetToken))
	if _, err := f.q.CreatePasswordResetToken(f.ctx, sqlcgen.CreatePasswordResetTokenParams{
		TokenHash: sum[:],
		UserID:    f.user.ID,
		ExpiresAt: pgtype.Timestamptz{Valid: true, Time: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("create reset token: %v", err)
	}

	if _, err := auth.ResetPassword(f.ctx, connect.NewRequest(&stillhousev1.ResetPasswordRequest{
		Token: resetToken, NewPassword: "a-sufficiently-long-password",
	})); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	if _, err := LookupBearer(f.ctx, f.q, plaintext); err == nil {
		t.Error("a token minted before the password reset still authenticates after it")
	}

	// And the sessions watermark moved, so every session dies too.
	after, err := f.q.GetUserByID(f.ctx, f.user.ID)
	if err != nil {
		t.Fatalf("re-read user: %v", err)
	}
	if !after.SessionsRevokedAt.Valid {
		t.Error("password reset left sessions_revoked_at unset")
	}
}
