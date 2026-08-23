package rpc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/secrets"
	"github.com/gallowaysoftware/stillhouse/backend/internal/totp"
)

// withSecretKey installs a usable encryption key for the duration of a
// test. The package reads its key once from the environment, so this
// reaches past that rather than fighting the sync.Once — what is under
// test here is the MFA flow, not the key loading, which secrets_test
// covers.
func withSecretKey(t *testing.T) {
	t.Helper()
	if secrets.Configured() {
		return
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	secrets.SetForTest(t, a)
}

// The whole point of a second factor, end to end: enrol, confirm, and
// then find that the password alone no longer signs you in.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestSecondFactorGatesLogin(t *testing.T) {
	withSecretKey(t)
	f := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewAuthService(f.q, f.db, scs.New(), log, nil, "http://example.test/r?token=", false)

	// Give the fixture user a password we know.
	const password = "a-long-enough-password"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := f.q.UpdateUserPassword(f.ctx, sqlcgen.UpdateUserPasswordParams{
		ID: f.user.ID, PasswordHash: hash,
	}); err != nil {
		t.Fatalf("set password: %v", err)
	}
	user, err := f.q.GetUserByID(f.ctx, f.user.ID)
	if err != nil {
		t.Fatalf("re-read user: %v", err)
	}
	authed := WithUser(f.ctx, user)

	login := func(t *testing.T, code, recovery string) (*stillhousev1.LoginResponse, error) {
		t.Helper()
		ctx, err := svc.session.Load(context.Background(), "")
		if err != nil {
			t.Fatalf("load session: %v", err)
		}
		resp, err := svc.Login(ctx, connect.NewRequest(&stillhousev1.LoginRequest{
			Email: user.Email, Password: password,
			TotpCode: code, RecoveryCode: recovery,
			TenantId: f.tenant.ID.String(),
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg, nil
	}

	// Before enrolment, the password is enough.
	if msg, err := login(t, "", ""); err != nil {
		t.Fatalf("login before enrolment: %v", err)
	} else if msg.GetMfaRequired() {
		t.Fatal("an account with no second factor was asked for one")
	}

	begin, err := svc.BeginMFAEnrolment(authed, connect.NewRequest(&stillhousev1.BeginMFAEnrolmentRequest{}))
	if err != nil {
		t.Fatalf("BeginMFAEnrolment: %v", err)
	}
	secret, err := totp.DecodeSecret(begin.Msg.GetSecret())
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}

	// An unconfirmed enrolment must not gate anything: someone who
	// scanned a code and closed the tab has not set up a second factor.
	if msg, err := login(t, "", ""); err != nil {
		t.Fatalf("login during pending enrolment: %v", err)
	} else if msg.GetMfaRequired() {
		t.Fatal("an unconfirmed enrolment locked the account")
	}

	// Confirmation requires a code the app actually produces.
	if _, err := svc.ConfirmMFAEnrolment(authed, connect.NewRequest(
		&stillhousev1.ConfirmMFAEnrolmentRequest{Code: "000000"})); err == nil {
		t.Error("enrolment was confirmed with a wrong code")
	}
	confirm, err := svc.ConfirmMFAEnrolment(authed, connect.NewRequest(
		&stillhousev1.ConfirmMFAEnrolmentRequest{Code: totp.Code(secret, totp.Step(time.Now()))}))
	if err != nil {
		t.Fatalf("ConfirmMFAEnrolment: %v", err)
	}
	recoveryCodes := confirm.Msg.GetRecoveryCodes()
	if len(recoveryCodes) != recoveryCodeCount {
		t.Fatalf("got %d recovery codes, want %d", len(recoveryCodes), recoveryCodeCount)
	}

	t.Run("password alone no longer signs in", func(t *testing.T) {
		msg, err := login(t, "", "")
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		if !msg.GetMfaRequired() {
			t.Fatal("the password alone still signed in after MFA was enabled")
		}
		if msg.GetUser() != nil || msg.GetTenant() != nil {
			t.Error("an MFA-required response carried the user or tenant")
		}
	})

	t.Run("a wrong code is refused", func(t *testing.T) {
		if _, err := login(t, "000000", ""); err == nil {
			t.Fatal("a wrong code signed in")
		} else if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("got %v, want Unauthenticated", connect.CodeOf(err))
		}
	})

	t.Run("the right code signs in, once", func(t *testing.T) {
		// One step ahead of the code that confirmed enrolment. Within the
		// accepted window, but past the step already consumed — a code
		// spent on confirmation is spent, which is the replay guard doing
		// its job rather than a quirk to work around.
		step := totp.Step(time.Now()) + 1
		code := totp.Code(secret, step)
		msg, err := login(t, code, "")
		if err != nil {
			t.Fatalf("login with a valid code: %v", err)
		}
		if msg.GetMfaRequired() || msg.GetUser() == nil {
			t.Fatal("a valid code did not complete the sign-in")
		}
		// The replay guard. Without it a code read over a shoulder stays
		// good for the rest of its ninety-second window, which is the
		// one attack a second factor is meant to make expensive.
		if _, err := login(t, code, ""); err == nil {
			t.Fatal("the same code was accepted twice")
		}
	})

	t.Run("a recovery code works once and then does not", func(t *testing.T) {
		rc := recoveryCodes[0]
		msg, err := login(t, "", rc)
		if err != nil {
			t.Fatalf("login with a recovery code: %v", err)
		}
		if msg.GetUser() == nil {
			t.Fatal("a recovery code did not complete the sign-in")
		}
		if _, err := login(t, "", rc); err == nil {
			t.Error("a recovery code was accepted twice")
		}
		// Case and hyphens are presentation, not content.
		msg2, err := login(t, "", strings.ToLower(strings.ReplaceAll(recoveryCodes[1], "-", "")))
		if err != nil {
			t.Fatalf("login with a reformatted recovery code: %v", err)
		}
		if msg2.GetUser() == nil {
			t.Fatal("a reformatted recovery code was rejected")
		}
	})

	t.Run("status reports what is left", func(t *testing.T) {
		st, err := svc.MFAStatus(authed, connect.NewRequest(&stillhousev1.MFAStatusRequest{}))
		if err != nil {
			t.Fatalf("MFAStatus: %v", err)
		}
		if !st.Msg.GetEnabled() {
			t.Error("status does not report MFA as enabled")
		}
		if got := st.Msg.GetRecoveryCodesRemaining(); got != recoveryCodeCount-2 {
			t.Errorf("recovery codes remaining %d, want %d", got, recoveryCodeCount-2)
		}
	})

	t.Run("disabling requires the password", func(t *testing.T) {
		if _, err := svc.DisableMFA(authed, connect.NewRequest(
			&stillhousev1.DisableMFARequest{CurrentPassword: "wrong"})); err == nil {
			t.Fatal("MFA was disabled without the password")
		}
		if _, err := svc.DisableMFA(authed, connect.NewRequest(
			&stillhousev1.DisableMFARequest{CurrentPassword: password})); err != nil {
			t.Fatalf("DisableMFA: %v", err)
		}
		msg, err := login(t, "", "")
		if err != nil {
			t.Fatalf("login after disable: %v", err)
		}
		if msg.GetMfaRequired() {
			t.Error("MFA is still demanded after being disabled")
		}
	})
}
