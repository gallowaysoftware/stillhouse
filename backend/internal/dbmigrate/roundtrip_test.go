package dbmigrate_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/migrations"
)

// The upgrade runbook says a Stillhouse release can be rolled back, and
// every migration ships a .down.sql. This is the test that makes that a
// measured claim rather than a stated one: it walks the whole chain up,
// then all the way back down to nothing, then up again, in a throwaway
// database created for the purpose.
//
// What it catches is the ordinary way a down migration rots — a DROP that
// names a constraint the up half renamed, an ALTER that assumes a column
// still exists, a function left owned by a role the down half revoked. A
// broken down migration is invisible until the evening somebody needs it.
//
// It does *not* claim that rolling a schema backwards over data written
// by the newer version is safe. It isn't, and the runbook says to restore
// the backup instead. What this proves is narrower and still worth
// having: the down path executes, so pinning the previous image on a
// fresh or restored database works.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN (superuser —
// it creates and drops a database).
func TestMigrationsRoundTrip(t *testing.T) {
	adminDSN := os.Getenv("STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("set STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN to run this test")
	}
	ctx := context.Background()

	// A throwaway database, so the round trip cannot touch the dev data
	// or race the other DB-backed tests.
	dbName := "stillhouse_roundtrip_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{dbName}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch database: %v", err)
	}
	t.Cleanup(func() {
		// FORCE so a connection migrate left behind can't block the drop.
		if _, err := admin.Exec(ctx,
			`DROP DATABASE IF EXISTS `+pgx.Identifier{dbName}.Sanitize()+` WITH (FORCE)`); err != nil {
			t.Logf("drop scratch database %s: %v", dbName, err)
		}
		_ = admin.Close(ctx)
	})

	scratchDSN, err := swapDatabase(adminDSN, dbName)
	if err != nil {
		t.Fatalf("build scratch DSN: %v", err)
	}

	newMigrator := func(t *testing.T) *migrate.Migrate {
		t.Helper()
		src, err := iofs.New(migrations.FS, ".")
		if err != nil {
			t.Fatalf("iofs source: %v", err)
		}
		m, err := migrate.NewWithSourceInstance("iofs", src, scratchDSN)
		if err != nil {
			t.Fatalf("migrate init: %v", err)
		}
		return m
	}

	m := newMigrator(t)
	if err := m.Up(); err != nil {
		t.Fatalf("first up: %v", err)
	}
	top, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("version after up: %v", err)
	}
	if dirty {
		t.Fatalf("schema is dirty at version %d after a clean up", top)
	}
	if top < 30 {
		t.Fatalf("only reached version %d; the embedded migrations look wrong", top)
	}

	// Step down one at a time rather than calling Down(), so a failure
	// names the migration that broke rather than "somewhere in there".
	for v := top; v >= 1; v-- {
		if err := m.Steps(-1); err != nil {
			t.Fatalf("rolling back migration %d failed: %v\n"+
				"Its .down.sql does not undo its .up.sql. Fix it now — a down "+
				"migration is only ever reached on an evening nobody wanted.", v, err)
		}
	}
	if _, _, err := m.Version(); err != migrate.ErrNilVersion {
		got, _, _ := m.Version()
		t.Fatalf("after rolling everything back the schema is at version %d, want none", got)
	}
	if _, err := m.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	// And back up, on the schema the down path left behind. This is the
	// half that catches a down migration which "succeeds" while leaving a
	// type, role grant or function in place that the up half then trips
	// over.
	m2 := newMigrator(t)
	defer func() { _, _ = m2.Close() }()
	if err := m2.Up(); err != nil {
		t.Fatalf("second up, over the state the rollback left: %v", err)
	}
	again, dirty, err := m2.Version()
	if err != nil {
		t.Fatalf("version after second up: %v", err)
	}
	if dirty {
		t.Fatalf("schema is dirty at version %d after the second up", again)
	}
	if again != top {
		t.Errorf("second up reached version %d, first reached %d", again, top)
	}
}

// swapDatabase rewrites the database name in a postgres DSN, keeping
// everything else (host, credentials, sslmode) as given.
func swapDatabase(dsn, dbName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}
