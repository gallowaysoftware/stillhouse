package rpc

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The outside bookkeeper: one person, one email address, an account at
// two distilleries. Under the install-wide UNIQUE this was simply
// impossible, and the second distillery to invite them got `internal`.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestOneEmailTwoDistilleries(t *testing.T) {
	a := newLedgerFixture(t)
	b := newLedgerFixture(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	const password = "one-password-two-distilleries"
	email := "bookkeeper-" + uuid.NewString() + "@example.com"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	for _, f := range []*ledgerFixture{a, b} {
		if _, err := f.q.CreateUser(f.ctx, sqlcgen.CreateUserParams{
			TenantID: f.tenant.ID, Email: email, PasswordHash: hash,
			DisplayName: "The Bookkeeper", Role: sqlcgen.UserRoleViewer,
		}); err != nil {
			t.Fatalf("create account at %s: %v", f.tenant.Name, err)
		}
	}

	// A second account at the *same* distillery is still a duplicate, and
	// says so rather than failing as an internal error.
	_, err = a.q.CreateUser(a.ctx, sqlcgen.CreateUserParams{
		TenantID: a.tenant.ID, Email: email, PasswordHash: hash,
		DisplayName: "Impostor", Role: sqlcgen.UserRoleViewer,
	})
	if err == nil {
		t.Error("two accounts with the same email at one distillery were allowed")
	} else if ce := classifyWriteErr(err, "missing"); ce == nil ||
		connect.CodeOf(ce) != connect.CodeAlreadyExists {
		t.Errorf("duplicate within a tenant classified as %v, want AlreadyExists", ce)
	}

	svc := NewAuthService(a.q, a.db, scs.New(), log, nil, "http://example.test/r?token=", false)

	t.Run("ambiguous login asks which distillery", func(t *testing.T) {
		ctx := loginCtx(t, svc)
		resp, err := svc.Login(ctx, connect.NewRequest(&stillhousev1.LoginRequest{
			Email: email, Password: password,
		}))
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		if resp.Msg.GetUser() != nil {
			t.Error("an ambiguous login signed the caller in anyway")
		}
		got := map[string]bool{}
		for _, c := range resp.Msg.GetChoices() {
			got[c.GetTenantId()] = true
		}
		if !got[a.tenant.ID.String()] || !got[b.tenant.ID.String()] {
			t.Errorf("choices %v do not name both distilleries", resp.Msg.GetChoices())
		}
	})

	t.Run("naming the distillery signs in", func(t *testing.T) {
		ctx := loginCtx(t, svc)
		resp, err := svc.Login(ctx, connect.NewRequest(&stillhousev1.LoginRequest{
			Email: email, Password: password, TenantId: b.tenant.ID.String(),
		}))
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		if len(resp.Msg.GetChoices()) != 0 {
			t.Error("a login that named a distillery still came back ambiguous")
		}
		if resp.Msg.GetTenant().GetId() != b.tenant.ID.String() {
			t.Errorf("signed in to %q, want %q",
				resp.Msg.GetTenant().GetId(), b.tenant.ID.String())
		}
	})

	t.Run("wrong password at a named distillery is refused", func(t *testing.T) {
		ctx := loginCtx(t, svc)
		_, err := svc.Login(ctx, connect.NewRequest(&stillhousev1.LoginRequest{
			Email: email, Password: "not the password", TenantId: b.tenant.ID.String(),
		}))
		if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("got %v, want Unauthenticated", err)
		}
	})

	t.Run("a distillery the caller has no account at is refused", func(t *testing.T) {
		ctx := loginCtx(t, svc)
		_, err := svc.Login(ctx, connect.NewRequest(&stillhousev1.LoginRequest{
			Email: email, Password: password, TenantId: uuid.NewString(),
		}))
		if err == nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("got %v, want Unauthenticated", err)
		}
	})
}

// loginCtx returns a context carrying a live scs session, which Login
// needs in order to write one. Each call gets its own, so the rate
// limiter and the session are not shared between subtests.
func loginCtx(t *testing.T, svc *AuthService) context.Context {
	t.Helper()
	ctx, err := svc.session.Load(context.Background(), "")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	return ctx
}
