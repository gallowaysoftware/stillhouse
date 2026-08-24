package rpc

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/auth"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/testdb"
)

// PLAN H7's group view. Two constraints, and both are about not producing
// a plausible wrong number.
//
// A B266 is filed per licence, so a figure spanning two is not a line on
// either. And what somebody may see at each licence is what their account
// THERE may see — a group view that pooled the reads would be a way to
// read past a role.
//
// Needs STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

func newTenantSvc(t *testing.T, f *ledgerFixture) *TenantService {
	t.Helper()
	return NewTenantService(testdb.AdminPool(t), f.q,
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

// The shape of the answer: rows per licence, each carrying its own licence
// number, and totals that say what they are.
func TestGroupView_RowsPerLicenceAndLabelledTotals(t *testing.T) {
	a := newLedgerFixture(t)
	b := newLedgerFixture(t)

	email := "group-" + uuid.NewString() + "@example.com"
	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	for _, f := range []*ledgerFixture{a, b} {
		if _, err := f.q.CreateUser(f.ctx, sqlcgen.CreateUserParams{
			TenantID: f.tenant.ID, Email: email, PasswordHash: hash,
			DisplayName: "Group Owner", Role: sqlcgen.UserRoleOwner,
		}); err != nil {
			t.Fatalf("create account: %v", err)
		}
	}
	me := accountFor(t, a, email, a.tenant.ID)

	svc := newTenantSvc(t, a)
	resp, err := svc.GroupView(WithUser(a.ctx, me), connect.NewRequest(&stillhousev1.GroupViewRequest{}))
	if err != nil {
		t.Fatalf("GroupView: %v", err)
	}
	m := resp.Msg
	if len(m.GetEntities()) != 2 {
		t.Fatalf("entities: got %d, want 2: %+v", len(m.GetEntities()), m.GetEntities())
	}
	seen := map[string]bool{}
	for _, e := range m.GetEntities() {
		if e.GetCraSpiritsLicenceNumber() == "" {
			t.Errorf("%s carries no licence number — that is what makes these separate returns",
				e.GetTenantName())
		}
		seen[e.GetTenantId()] = true
	}
	if !seen[a.tenant.ID.String()] || !seen[b.tenant.ID.String()] {
		t.Errorf("both licences should appear: %+v", m.GetEntities())
	}

	// The caution is the product of the screen, not decoration.
	if !strings.Contains(m.GetCaution(), "not a line on any of them") {
		t.Errorf("caution does not disclaim the totals: %q", m.GetCaution())
	}
	if !strings.Contains(m.GetCaution(), "two licences are two filers") {
		t.Errorf("caution does not state the rule: %q", m.GetCaution())
	}
}

// The security property. A group view must never show a licence the
// caller does not hold an account at — membership IS holding an account,
// which is why there is no group table anybody could be added to.
func TestGroupView_ShowsOnlyLicencesYouHoldAnAccountAt(t *testing.T) {
	a := newLedgerFixture(t)
	stranger := newLedgerFixture(t) // exists, caller has no account there

	email := "solo-" + uuid.NewString() + "@example.com"
	hash, _ := auth.HashPassword("a-long-enough-password")
	if _, err := a.q.CreateUser(a.ctx, sqlcgen.CreateUserParams{
		TenantID: a.tenant.ID, Email: email, PasswordHash: hash,
		DisplayName: "Solo", Role: sqlcgen.UserRoleOwner,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	me := accountFor(t, a, email, a.tenant.ID)

	svc := newTenantSvc(t, a)
	resp, err := svc.GroupView(WithUser(a.ctx, me), connect.NewRequest(&stillhousev1.GroupViewRequest{}))
	if err != nil {
		t.Fatalf("GroupView: %v", err)
	}
	for _, e := range resp.Msg.GetEntities() {
		if e.GetTenantId() == stranger.tenant.ID.String() {
			t.Fatal("a group view showed a licence the caller holds no account at")
		}
	}
	if len(resp.Msg.GetEntities()) != 1 {
		t.Errorf("entities: got %d, want just the one they hold", len(resp.Msg.GetEntities()))
	}
}

// The role at each licence is reported, and it is the role held THERE.
// An owner at one distillery is not an owner at another, and a screen
// that implied otherwise would be a way to read past a role.
func TestGroupView_ReportsTheRoleHeldAtEachLicence(t *testing.T) {
	a := newLedgerFixture(t)
	b := newLedgerFixture(t)

	email := "mixed-" + uuid.NewString() + "@example.com"
	hash, _ := auth.HashPassword("a-long-enough-password")
	if _, err := a.q.CreateUser(a.ctx, sqlcgen.CreateUserParams{
		TenantID: a.tenant.ID, Email: email, PasswordHash: hash,
		DisplayName: "Mixed", Role: sqlcgen.UserRoleOwner,
	}); err != nil {
		t.Fatalf("A: %v", err)
	}
	// Only a viewer at B.
	if _, err := b.q.CreateUser(b.ctx, sqlcgen.CreateUserParams{
		TenantID: b.tenant.ID, Email: email, PasswordHash: hash,
		DisplayName: "Mixed", Role: sqlcgen.UserRoleViewer,
	}); err != nil {
		t.Fatalf("B: %v", err)
	}
	me := accountFor(t, a, email, a.tenant.ID)

	svc := newTenantSvc(t, a)
	resp, err := svc.GroupView(WithUser(a.ctx, me), connect.NewRequest(&stillhousev1.GroupViewRequest{}))
	if err != nil {
		t.Fatalf("GroupView: %v", err)
	}
	roles := map[string]string{}
	for _, e := range resp.Msg.GetEntities() {
		roles[e.GetTenantId()] = e.GetYourRole()
	}
	if roles[a.tenant.ID.String()] != "owner" {
		t.Errorf("role at A: %q, want owner", roles[a.tenant.ID.String()])
	}
	if roles[b.tenant.ID.String()] != "viewer" {
		t.Errorf("role at B: %q, want viewer — holding an owner's account at A does not make them one at B",
			roles[b.tenant.ID.String()])
	}
}

// accountFor returns the sqlcgen.User for one email at one tenant.
func accountFor(t *testing.T, f *ledgerFixture, email string, tenantID uuid.UUID) sqlcgen.User {
	t.Helper()
	all, err := f.q.ListUsersByEmail(f.ctx, email)
	if err != nil {
		t.Fatalf("ListUsersByEmail: %v", err)
	}
	for _, u := range all {
		if u.TenantID == tenantID {
			return u
		}
	}
	t.Fatalf("no account for %s at %s", email, tenantID)
	return sqlcgen.User{}
}
