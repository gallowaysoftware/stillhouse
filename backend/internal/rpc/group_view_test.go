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

// newTenantSvc builds the service on the RLS-ENFORCED pool, which is what
// production gives it (server.go passes the stillhouse_app connection).
//
// An earlier version passed the admin pool and the copy tests failed in a
// way that looked like a bug in the code: the destination appeared to
// already hold the source's materials, because a superuser connection
// bypasses row-level security and `SELECT name FROM materials` returned
// every tenant's. The fixture, not the handler. Worth the comment because
// a test harness that quietly disables the boundary under test is the
// most expensive kind of green.
func newTenantSvc(t *testing.T, f *ledgerFixture) *TenantService {
	t.Helper()
	return NewTenantService(testdb.AppPool(t), f.q,
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

// PLAN H7's shared reference data, which turned out to be a copy rather
// than a share — and the difference is the design.
//
// Shared mutable reference data across licences means one licensee's edit
// changing another's records, and a material's extract fraction feeds a
// conversion efficiency which feeds a yield. Each licence owns what its
// own figures were computed from.

func seedMaterial(t *testing.T, f *ledgerFixture, name string) {
	t.Helper()
	if _, err := testdb.AdminPool(t).Exec(f.ctx,
		`INSERT INTO materials (tenant_id, name, kind, uom) VALUES ($1,$2,'grain','kg')`,
		f.tenant.ID, name); err != nil {
		t.Fatalf("seed material %s: %v", name, err)
	}
}

func materialNames(t *testing.T, f *ledgerFixture) map[string]bool {
	t.Helper()
	rows, err := testdb.AdminPool(t).Query(f.ctx,
		`SELECT name FROM materials WHERE tenant_id = $1`, f.tenant.ID)
	if err != nil {
		t.Fatalf("names: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[n] = true
	}
	return out
}

func twoAccounts(t *testing.T, a, b *ledgerFixture) (string, sqlcgen.User) {
	t.Helper()
	email := "copier-" + uuid.NewString() + "@example.com"
	hash, err := auth.HashPassword("a-long-enough-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	for _, f := range []*ledgerFixture{a, b} {
		if _, err := f.q.CreateUser(f.ctx, sqlcgen.CreateUserParams{
			TenantID: f.tenant.ID, Email: email, PasswordHash: hash,
			DisplayName: "Copier", Role: sqlcgen.UserRoleOwner,
		}); err != nil {
			t.Fatalf("create account: %v", err)
		}
	}
	return email, accountFor(t, a, email, a.tenant.ID)
}

func TestCopyReferenceData_CopiesAndDoesNotLink(t *testing.T) {
	src := newLedgerFixture(t)
	dst := newLedgerFixture(t)
	seedMaterial(t, src, "Copied Rye")
	_, me := twoAccounts(t, dst, src) // account at the destination

	svc := newTenantSvc(t, dst)
	resp, err := svc.CopyReferenceData(WithUser(dst.ctx, me),
		connect.NewRequest(&stillhousev1.CopyReferenceDataRequest{
			FromTenantId: src.tenant.ID.String(),
			What: []stillhousev1.CopyableReference{
				stillhousev1.CopyableReference_COPYABLE_REFERENCE_MATERIALS,
			},
		}))
	if err != nil {
		t.Fatalf("CopyReferenceData: %v", err)
	}
	if resp.Msg.GetMaterialsCopied() < 1 {
		t.Fatalf("nothing copied: %+v", resp.Msg)
	}
	if !materialNames(t, dst)["Copied Rye"] {
		t.Error("the material did not arrive at the destination")
	}
	if !strings.Contains(resp.Msg.GetNote(), "Copied, not linked") {
		t.Errorf("note does not state the distinction: %q", resp.Msg.GetNote())
	}

	// Editing here must not touch there. That is the whole reason this
	// copies rather than shares.
	if _, err := testdb.AdminPool(t).Exec(dst.ctx,
		`UPDATE materials SET extract_fraction = 0.99 WHERE tenant_id = $1 AND name = 'Copied Rye'`,
		dst.tenant.ID); err != nil {
		t.Fatalf("edit: %v", err)
	}
	var srcFraction *float64
	if err := testdb.AdminPool(t).QueryRow(src.ctx,
		`SELECT extract_fraction FROM materials WHERE tenant_id = $1 AND name = 'Copied Rye'`,
		src.tenant.ID).Scan(&srcFraction); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if srcFraction != nil && *srcFraction == 0.99 {
		t.Error("editing the copy changed the original — these are supposed to be separate records")
	}
}

// A name already here is left alone and reported. Overwriting would
// replace a definition this licensee's own figures were computed from.
func TestCopyReferenceData_DoesNotOverwriteWhatIsAlreadyHere(t *testing.T) {
	src := newLedgerFixture(t)
	dst := newLedgerFixture(t)
	seedMaterial(t, src, "Shared Name")
	seedMaterial(t, dst, "shared name") // same grain, different case
	_, me := twoAccounts(t, dst, src)

	svc := newTenantSvc(t, dst)
	resp, err := svc.CopyReferenceData(WithUser(dst.ctx, me),
		connect.NewRequest(&stillhousev1.CopyReferenceDataRequest{
			FromTenantId: src.tenant.ID.String(),
			What: []stillhousev1.CopyableReference{
				stillhousev1.CopyableReference_COPYABLE_REFERENCE_MATERIALS,
			},
		}))
	if err != nil {
		t.Fatalf("CopyReferenceData: %v", err)
	}
	var reported bool
	for _, s := range resp.Msg.GetSkipped() {
		if strings.EqualFold(s, "Shared Name") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("a name already here was not reported as skipped: %+v", resp.Msg.GetSkipped())
	}
	// And only one of them exists, not two spellings of one grain.
	names := materialNames(t, dst)
	if names["Shared Name"] && names["shared name"] {
		t.Error("both spellings exist — a materials list has become two lists")
	}
}

// The security property. Copying reads the source's material list, so it
// must refuse a licence the caller holds no account at — and refuse it
// the same way as one that does not exist.
func TestCopyReferenceData_RefusesALicenceYouDoNotHold(t *testing.T) {
	dst := newLedgerFixture(t)
	stranger := newLedgerFixture(t)
	seedMaterial(t, stranger, "Not Yours")

	email := "lonely-" + uuid.NewString() + "@example.com"
	hash, _ := auth.HashPassword("a-long-enough-password")
	if _, err := dst.q.CreateUser(dst.ctx, sqlcgen.CreateUserParams{
		TenantID: dst.tenant.ID, Email: email, PasswordHash: hash,
		DisplayName: "Lonely", Role: sqlcgen.UserRoleOwner,
	}); err != nil {
		t.Fatalf("account: %v", err)
	}
	me := accountFor(t, dst, email, dst.tenant.ID)
	svc := newTenantSvc(t, dst)

	_, errHeld := svc.CopyReferenceData(WithUser(dst.ctx, me),
		connect.NewRequest(&stillhousev1.CopyReferenceDataRequest{
			FromTenantId: stranger.tenant.ID.String(),
			What: []stillhousev1.CopyableReference{
				stillhousev1.CopyableReference_COPYABLE_REFERENCE_MATERIALS,
			},
		}))
	if errHeld == nil {
		t.Fatal("read another licensee's materials on the strength of knowing a tenant id")
	}
	if materialNames(t, dst)["Not Yours"] {
		t.Fatal("and copied them")
	}

	// A tenant that does not exist must answer identically, or this
	// distinguishes real licences from invented ones for anybody with a
	// session.
	_, errUnknown := svc.CopyReferenceData(WithUser(dst.ctx, me),
		connect.NewRequest(&stillhousev1.CopyReferenceDataRequest{
			FromTenantId: uuid.NewString(),
			What: []stillhousev1.CopyableReference{
				stillhousev1.CopyableReference_COPYABLE_REFERENCE_MATERIALS,
			},
		}))
	if errUnknown == nil {
		t.Fatal("accepted a tenant id that does not exist")
	}
	if connect.CodeOf(errHeld) != connect.CodeOf(errUnknown) ||
		errHeld.Error() != errUnknown.Error() {
		t.Errorf("distinguishable: not-yours=%v unknown=%v", errHeld, errUnknown)
	}
}
