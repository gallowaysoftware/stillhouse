package rpc

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/secrets"
	"github.com/gallowaysoftware/stillhouse/backend/internal/totp"
)

// PLAN H7. Stage 155 made one email able to hold an account at two
// distilleries; what was missing was moving between them without signing
// out.
//
// The tests below are almost entirely about one thing. Login verifies the
// password against each candidate account separately, because "one
// password may be right at one distillery and wrong at another" — so a
// session at one proves nothing whatsoever about another. A switch that
// skipped re-verification would be an authentication bypass wearing the
// costume of a convenience feature.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

type switchFixture struct {
	a, b  *ledgerFixture
	svc   *AuthService
	email string
	passA string
	passB string
}

func newSwitchFixture(t *testing.T, samePassword bool) *switchFixture {
	t.Helper()
	f := &switchFixture{
		a:     newLedgerFixture(t),
		b:     newLedgerFixture(t),
		email: "switcher-" + uuid.NewString() + "@example.com",
		passA: "password-at-distillery-a",
		passB: "password-at-distillery-b",
	}
	if samePassword {
		f.passB = f.passA
	}
	mk := func(lf *ledgerFixture, pass string, role sqlcgen.UserRole) {
		t.Helper()
		hash, err := auth.HashPassword(pass)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if _, err := lf.q.CreateUser(lf.ctx, sqlcgen.CreateUserParams{
			TenantID: lf.tenant.ID, Email: f.email, PasswordHash: hash,
			DisplayName: "Switcher", Role: role,
		}); err != nil {
			t.Fatalf("create account: %v", err)
		}
	}
	// Deliberately different roles: switching must carry the role held at
	// the destination, not the one held at the origin.
	mk(f.a, f.passA, sqlcgen.UserRoleOwner)
	mk(f.b, f.passB, sqlcgen.UserRoleViewer)

	f.svc = NewAuthService(f.a.q, f.a.db, scs.New(),
		slog.New(slog.NewTextHandler(os.Stderr, nil)), nil, "http://example.test/r?token=", false)
	return f
}

// signIn logs in to one distillery and returns the session context.
func (f *switchFixture) signIn(t *testing.T, lf *ledgerFixture, pass string) (context.Context, *stillhousev1.User) {
	t.Helper()
	ctx := loginCtx(t, f.svc)
	resp, err := f.svc.Login(ctx, connect.NewRequest(&stillhousev1.LoginRequest{
		Email: f.email, Password: pass, TenantId: lf.tenant.ID.String(),
	}))
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if resp.Msg.GetUser() == nil {
		t.Fatalf("Login did not sign in: %+v", resp.Msg)
	}
	return ctx, resp.Msg.GetUser()
}

// userFromLogin turns the login response into the sqlcgen.User the RPC
// layer attaches to the context, the way the session middleware does.
func userFromLogin(t *testing.T, f *switchFixture, u *stillhousev1.User) sqlcgen.User {
	t.Helper()
	id, err := uuid.Parse(u.GetId())
	if err != nil {
		t.Fatalf("parse user id: %v", err)
	}
	got, err := f.a.q.GetUserByID(f.a.ctx, id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return got
}

// The feature working: same person, right password at the destination.
func TestSwitchDistillery_MovesTheSessionAndTheRole(t *testing.T) {
	f := newSwitchFixture(t, false)
	ctx, signedIn := f.signIn(t, f.a, f.passA)

	// The switcher lists both, and says which one we are on.
	me := userFromLogin(t, f, signedIn)
	list, err := f.svc.ListMyDistilleries(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.ListMyDistilleriesRequest{}))
	if err != nil {
		t.Fatalf("ListMyDistilleries: %v", err)
	}
	if len(list.Msg.GetDistilleries()) != 2 {
		t.Fatalf("distilleries: got %d, want 2", len(list.Msg.GetDistilleries()))
	}
	var currents int
	for _, d := range list.Msg.GetDistilleries() {
		if d.GetCurrent() {
			currents++
			if d.GetTenantId() != f.a.tenant.ID.String() {
				t.Errorf("current is %s, want A", d.GetTenantId())
			}
		}
	}
	if currents != 1 {
		t.Errorf("current flag set on %d distilleries, want exactly 1", currents)
	}

	resp, err := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			TenantId: f.b.tenant.ID.String(), Password: f.passB,
		}))
	if err != nil {
		t.Fatalf("SwitchDistillery: %v", err)
	}
	if resp.Msg.GetTenant().GetId() != f.b.tenant.ID.String() {
		t.Errorf("landed on %s, want B", resp.Msg.GetTenant().GetId())
	}
	// The role at B, not the role at A. Carrying the origin's role across
	// would hand an owner's reach to a viewer account.
	if got := resp.Msg.GetUser().GetRole(); got != stillhousev1.UserRole_USER_ROLE_VIEWER {
		t.Errorf("role after switch: got %v, want the VIEWER held at B", got)
	}
	// And the session now points at B.
	if got := f.svc.session.GetString(ctx, "tenant_id"); got != f.b.tenant.ID.String() {
		t.Errorf("session tenant_id is %s, want B", got)
	}
}

// The bypass this feature could have been. A session at A must not open B
// when B's password is different — which is the whole reason Login
// verifies per account.
func TestSwitchDistillery_WrongPasswordForTargetIsRefused(t *testing.T) {
	f := newSwitchFixture(t, false)
	ctx, signedIn := f.signIn(t, f.a, f.passA)
	me := userFromLogin(t, f, signedIn)

	_, err := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			// A's password, which is not B's.
			TenantId: f.b.tenant.ID.String(), Password: f.passA,
		}))
	if err == nil {
		t.Fatal("a session at A opened B using A's password — that is an authentication bypass")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("code: got %v, want unauthenticated", connect.CodeOf(err))
	}
	if got := f.svc.session.GetString(ctx, "tenant_id"); got != f.a.tenant.ID.String() {
		t.Errorf("a refused switch moved the session anyway: %s", got)
	}
}

// An empty password must not be treated as "already authenticated".
func TestSwitchDistillery_EmptyPasswordIsRefused(t *testing.T) {
	f := newSwitchFixture(t, true) // same password at both: still not enough
	ctx, signedIn := f.signIn(t, f.a, f.passA)
	me := userFromLogin(t, f, signedIn)

	_, err := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			TenantId: f.b.tenant.ID.String(),
		}))
	if err == nil {
		t.Fatal("switched with no password at all")
	}
	if got := f.svc.session.GetString(ctx, "tenant_id"); got != f.a.tenant.ID.String() {
		t.Errorf("session moved: %s", got)
	}
}

// A distillery this address holds no account at must answer exactly as a
// wrong password does. Otherwise an authenticated session is an oracle for
// which distilleries an address is registered at.
func TestSwitchDistillery_UnknownTenantLooksLikeBadCredentials(t *testing.T) {
	f := newSwitchFixture(t, false)
	other := newLedgerFixture(t) // exists, but this email has no account there
	ctx, signedIn := f.signIn(t, f.a, f.passA)
	me := userFromLogin(t, f, signedIn)

	_, errNoAccount := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			TenantId: other.tenant.ID.String(), Password: f.passA,
		}))
	_, errWrongPass := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			TenantId: f.b.tenant.ID.String(), Password: "definitely-not-it",
		}))

	if errNoAccount == nil || errWrongPass == nil {
		t.Fatal("one of the two refusals did not happen")
	}
	if connect.CodeOf(errNoAccount) != connect.CodeOf(errWrongPass) {
		t.Errorf("distinguishable: no-account=%v wrong-password=%v — the switcher is an oracle",
			connect.CodeOf(errNoAccount), connect.CodeOf(errWrongPass))
	}
	if errNoAccount.Error() != errWrongPass.Error() {
		t.Errorf("distinguishable messages: %q vs %q", errNoAccount, errWrongPass)
	}
}

// Switching to where you already are is a no-op worth refusing plainly,
// rather than a password prompt that achieves nothing.
func TestSwitchDistillery_SameTenantIsRefused(t *testing.T) {
	f := newSwitchFixture(t, false)
	ctx, signedIn := f.signIn(t, f.a, f.passA)
	me := userFromLogin(t, f, signedIn)

	_, err := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			TenantId: f.a.tenant.ID.String(), Password: f.passA,
		}))
	if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("switching to the current distillery: got %v, want invalid_argument", err)
	}
}

// The most security-sensitive property here, and the one a convenience
// feature is most likely to skip: the SECOND FACTOR belongs to the target
// account, not to the session.
//
// If account B has MFA and account A does not, a switch that let an
// A-session into B on a password alone would be a route into an
// MFA-protected account without satisfying its MFA. The session's own
// authentication says nothing about B.
func TestSwitchDistillery_TargetsSecondFactorIsRequired(t *testing.T) {
	withSecretKey(t)
	f := newSwitchFixture(t, false)
	ctx, signedIn := f.signIn(t, f.a, f.passA)
	me := userFromLogin(t, f, signedIn)

	// Enrol and confirm a second factor on the account at B only.
	bUser := accountAt(t, f, f.b)
	secret := make([]byte, 20)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	sealed, err := secrets.Seal(secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := f.a.q.UpsertUserTOTP(f.a.ctx, sqlcgen.UpsertUserTOTPParams{
		UserID: bUser.ID, SecretSealed: sealed,
	}); err != nil {
		t.Fatalf("enrol: %v", err)
	}
	if _, err := f.a.q.ConfirmUserTOTP(f.a.ctx, sqlcgen.ConfirmUserTOTPParams{
		UserID: bUser.ID, LastUsedStep: pgtype.Int8{},
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// Right password, no code: must not switch.
	resp, err := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			TenantId: f.b.tenant.ID.String(), Password: f.passB,
		}))
	if err != nil {
		t.Fatalf("SwitchDistillery: %v", err)
	}
	if !resp.Msg.GetMfaRequired() {
		t.Fatal("switched into an MFA-protected account without a code — the session's own auth says nothing about the target")
	}
	if resp.Msg.GetTenant() != nil {
		t.Error("a switch that demanded MFA also returned a tenant")
	}
	if got := f.svc.session.GetString(ctx, "tenant_id"); got != f.a.tenant.ID.String() {
		t.Errorf("the session moved despite MFA being required: %s", got)
	}

	// With a valid code it goes through.
	ok, err := f.svc.SwitchDistillery(WithUser(ctx, me),
		connect.NewRequest(&stillhousev1.SwitchDistilleryRequest{
			TenantId: f.b.tenant.ID.String(), Password: f.passB,
			TotpCode: totp.Code(secret, totp.Step(time.Now())),
		}))
	if err != nil {
		t.Fatalf("SwitchDistillery with code: %v", err)
	}
	if ok.Msg.GetTenant().GetId() != f.b.tenant.ID.String() {
		t.Errorf("did not land on B with a valid code: %+v", ok.Msg)
	}
}

// accountAt returns the sqlcgen.User row for this fixture's email at the
// given distillery.
func accountAt(t *testing.T, f *switchFixture, lf *ledgerFixture) sqlcgen.User {
	t.Helper()
	all, err := f.a.q.ListUsersByEmail(f.a.ctx, f.email)
	if err != nil {
		t.Fatalf("ListUsersByEmail: %v", err)
	}
	for _, u := range all {
		if u.TenantID == lf.tenant.ID {
			return u
		}
	}
	t.Fatalf("no account at %s", lf.tenant.Name)
	return sqlcgen.User{}
}
