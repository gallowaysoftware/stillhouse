// Package dbmigrate wraps golang-migrate against the embedded migrations FS.
// Called by the server on boot when ADMIN_DATABASE_URL is set, so production
// deploys auto-apply schema changes on container restart and the operator
// doesn't need to remember a separate migrate step.
package dbmigrate

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/migrations"
)

// Up applies all pending migrations against the given superuser DSN. No-op
// if the DB is already at the latest version. Returns nil for ErrNoChange
// because "no migrations to apply" is the success case on every restart.
func Up(adminDSN string, logger *slog.Logger) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, adminDSN)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer func() {
		// Close releases the DB connection migrate opened. We don't care
		// about the source error; iofs doesn't open any resources.
		if _, err := m.Close(); err != nil {
			logger.Warn("migrate close", "err", err)
		}
	}()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("migrate version: %w", err)
	}
	logger.Info("migrations applied", "version", v, "dirty", dirty)
	return nil
}
