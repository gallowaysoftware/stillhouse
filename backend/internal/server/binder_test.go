package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// PLAN C2. Everything in the binder already existed in pieces —
// period-locked snapshots, the audit log, gauge determination paths, the
// instruments behind them, movement-level detail. Nobody had assembled
// them, so answering "show me how you arrived at line 3" meant exporting
// four things and explaining the join by hand.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.

type binderFixture struct {
	pool   *pgxpool.Pool
	q      *sqlcgen.Queries
	ctx    context.Context
	tenant sqlcgen.Tenant
	user   sqlcgen.User
}

func newBinderFixture(t *testing.T) *binderFixture {
	t.Helper()
	dsn := os.Getenv("STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN to run this test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	q := sqlcgen.New(pool)

	tenant, err := q.CreateTenant(ctx, sqlcgen.CreateTenantParams{
		Name:                    "Binder Distillery " + uuid.NewString()[:8],
		CraSpiritsLicenceNumber: "BINDER-" + uuid.NewString(),
		DefaultJurisdiction:     "CA-ON",
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID: tenant.ID, Email: "binder-" + uuid.NewString() + "@example.com",
		PasswordHash: "x", DisplayName: "Binder Owner", Role: sqlcgen.UserRoleOwner,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &binderFixture{pool: pool, q: q, ctx: ctx, tenant: tenant, user: user}
}

// period creates a period, optionally submitted with a snapshot.
func (f *binderFixture) period(t *testing.T, snapshot string) uuid.UUID {
	t.Helper()
	p, err := f.q.UpsertB266PeriodDraft(f.ctx, sqlcgen.UpsertB266PeriodDraftParams{
		TenantID:    f.tenant.ID,
		PeriodStart: pgtype.Date{Valid: true, Time: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		PeriodEnd:   pgtype.Date{Valid: true, Time: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)},
		DueOn:       pgtype.Date{Valid: true, Time: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}
	if snapshot != "" {
		if _, err := f.q.SubmitB266Period(f.ctx, sqlcgen.SubmitB266PeriodParams{
			ID: p.ID, Snapshot: []byte(snapshot),
			SubmittedBy:           uuid.NullUUID{UUID: f.user.ID, Valid: true},
			FilingAcknowledgement: "I have checked these figures against my own records.",
		}); err != nil {
			t.Fatalf("submit period: %v", err)
		}
	}
	return p.ID
}

func fileIn(files []binderFile, name string) []byte {
	for _, f := range files {
		if f.name == name {
			return f.body
		}
	}
	return nil
}

// The one rule that matters: a submitted period's figures come from the
// frozen snapshot, never recomputed. A binder that recalculated would
// print today's answer under the heading of what was filed.
func TestBinderReportsTheSnapshotNotAFreshComputation(t *testing.T) {
	f := newBinderFixture(t)
	// A snapshot with a figure that no live computation could produce:
	// this tenant has no movements at all.
	id := f.period(t, `{"bulkClosingLaa": 4242.5, "dutyPayableCad": 999.99,
		"dutyRatePerLaa": 14.117, "periodStart": "2026-06-01", "periodEnd": "2026-06-30"}`)

	files, _, err := buildB266Binder(f.ctx, f.pool, f.q, f.tenant.ID, id, f.user, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildB266Binder: %v", err)
	}
	ret := string(fileIn(files, "01-return.csv"))
	if !strings.Contains(ret, "4242.5000") {
		t.Errorf("the return does not carry the snapshot's closing balance:\n%s", ret)
	}
	if !strings.Contains(ret, "999.99") {
		t.Errorf("the return does not carry the snapshot's duty figure:\n%s", ret)
	}
	doc := string(fileIn(files, "binder.html"))
	if !strings.Contains(doc, "4242.5000") {
		t.Error("the document does not carry the snapshot's figures")
	}
	if !strings.Contains(doc, "have not been recomputed") {
		t.Error("the document does not say the figures are the frozen snapshot")
	}
}

// A draft has no snapshot, so there is nothing that was filed. Saying so
// is the only honest option: printing a live computation under "the return
// as filed" would be a lie in a document whose whole purpose is not to be.
func TestBinderRefusesToPresentADraftAsFiled(t *testing.T) {
	f := newBinderFixture(t)
	id := f.period(t, "")

	files, _, err := buildB266Binder(f.ctx, f.pool, f.q, f.tenant.ID, id, f.user, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildB266Binder: %v", err)
	}
	doc := string(fileIn(files, "binder.html"))
	if !strings.Contains(doc, "has not been submitted") {
		t.Error("the document does not say the period is a draft")
	}
	if strings.Contains(doc, "The return as filed") {
		t.Error("a draft period was presented under 'the return as filed'")
	}
	readme := string(fileIn(files, "README.txt"))
	if !strings.Contains(readme, "WARNING") {
		t.Error("README does not warn that this is a draft")
	}
}

// Every schedule is present, and the manifest hashes all of them.
func TestBinderCarriesEverySchedule(t *testing.T) {
	f := newBinderFixture(t)
	id := f.period(t, `{"periodStart": "2026-06-01", "periodEnd": "2026-06-30"}`)

	files, name, err := buildB266Binder(f.ctx, f.pool, f.q, f.tenant.ID, id, f.user, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildB266Binder: %v", err)
	}
	if !strings.HasPrefix(name, "stillhouse-b266-binder-2026-06-01-to-2026-06-30") {
		t.Errorf("bundle name: got %q", name)
	}
	for _, want := range []string{"README.txt", "binder.html", "01-return.csv", "manifest.txt"} {
		if fileIn(files, want) == nil {
			t.Errorf("the bundle has no %s", want)
		}
	}
	for _, tbl := range binderTables {
		body := fileIn(files, tbl.file)
		if body == nil {
			t.Errorf("the bundle has no %s", tbl.file)
			continue
		}
		// A schedule with no header is a schedule nobody can read, even
		// when it has no rows.
		if !strings.Contains(string(body), ",") {
			t.Errorf("%s has no CSV header", tbl.file)
		}
	}

	// The manifest names every file except itself — it is what hashes the
	// rest, so it cannot hash itself.
	manifest := string(fileIn(files, "manifest.txt"))
	for _, fl := range files {
		if fl.name == "manifest.txt" {
			continue
		}
		if !strings.Contains(manifest, fl.name) {
			t.Errorf("manifest does not list %s", fl.name)
		}
	}
	// It cannot hash itself, so no hash line may name it. Checked as a
	// hash line rather than as a substring: the manifest explains its own
	// absence in prose, and that sentence is not a listing.
	for _, line := range strings.Split(manifest, "\n") {
		if strings.HasSuffix(line, "  manifest.txt") {
			t.Errorf("the manifest hashes itself: %q", line)
		}
	}
}

// The schedules are period-bounded. A movement from the month before must
// not appear in this month's binder, or the evidence supports a figure it
// is not behind.
func TestBinderSchedulesAreBoundedByThePeriod(t *testing.T) {
	f := newBinderFixture(t)

	c, err := f.q.CreateBulkContainer(f.ctx, sqlcgen.CreateBulkContainerParams{
		TenantID: f.tenant.ID, Name: "Binder tank", Kind: sqlcgen.BulkContainerKindTank,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	for _, when := range []time.Time{
		time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC), // before
		time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC), // inside
		time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC),  // after
	} {
		if _, err := f.q.InsertBulkMovement(f.ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               f.tenant.ID,
			DestinationContainerID: uuid.NullUUID{UUID: c.ID, Valid: true},
			VolumeL:                100, AbvPct: 60, Laa: 60,
			Reason:        sqlcgen.BulkMovementReasonProductionGauge,
			ReferenceType: "binder-test",
			Notes:         when.Format("2006-01-02"),
			OccurredAt:    pgtype.Timestamptz{Valid: true, Time: when},
		}); err != nil {
			t.Fatalf("insert movement: %v", err)
		}
	}

	id := f.period(t, `{"periodStart": "2026-06-01", "periodEnd": "2026-06-30"}`)
	files, _, err := buildB266Binder(f.ctx, f.pool, f.q, f.tenant.ID, id, f.user, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildB266Binder: %v", err)
	}
	movements := string(fileIn(files, "02-bulk-movements.csv"))

	if !strings.Contains(movements, "2026-06-15") {
		t.Error("the movement inside the period is missing")
	}
	for _, outside := range []string{"2026-05-20", "2026-07-05"} {
		if strings.Contains(movements, outside) {
			t.Errorf("a movement from %s appears in a June binder", outside)
		}
	}
	// Ids resolved to names: an auditor reading a UUID learns nothing.
	if !strings.Contains(movements, "Binder tank") {
		t.Error("the schedule does not name the container")
	}
	if strings.Contains(movements, c.ID.String()) {
		t.Error("the schedule carries a raw container UUID where a name belongs")
	}
}

// The confirmation recorded at submit is reproduced, with its wording.
func TestBinderReproducesTheFilingConfirmation(t *testing.T) {
	f := newBinderFixture(t)
	id := f.period(t, `{"periodStart": "2026-06-01", "periodEnd": "2026-06-30"}`)

	files, _, err := buildB266Binder(f.ctx, f.pool, f.q, f.tenant.ID, id, f.user, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildB266Binder: %v", err)
	}
	doc := string(fileIn(files, "binder.html"))
	if !strings.Contains(doc, "I have checked these figures against my own records.") {
		t.Error("the document does not reproduce the confirmation wording")
	}
	if !strings.Contains(doc, "never filed anything with CRA") {
		t.Error("the document does not say Stillhouse never filed anything")
	}
}

// Filing blockers recorded on the snapshot are reproduced, not hidden. A
// binder that quietly dropped them would be worth less than one that did
// not.
func TestBinderReproducesFilingBlockers(t *testing.T) {
	f := newBinderFixture(t)
	id := f.period(t, `{"periodStart":"2026-06-01","periodEnd":"2026-06-30",
		"filingBlockers":["3 losses totalling 12.0000 LAA have no duty treatment."]}`)

	files, _, err := buildB266Binder(f.ctx, f.pool, f.q, f.tenant.ID, id, f.user, time.Now().UTC())
	if err != nil {
		t.Fatalf("buildB266Binder: %v", err)
	}
	doc := string(fileIn(files, "binder.html"))
	if !strings.Contains(doc, "no duty treatment") {
		t.Error("a blocker recorded at filing time was not reproduced")
	}
	if !strings.Contains(doc, "Outstanding at the time this period was filed") {
		t.Error("the blockers have no heading explaining what they are")
	}
}
