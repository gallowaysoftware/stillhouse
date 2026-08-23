// Command server runs the Stillhouse backend.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alcoholometry"
	"github.com/gallowaysoftware/stillhouse/backend/internal/config"
	"github.com/gallowaysoftware/stillhouse/backend/internal/dbmigrate"
	"github.com/gallowaysoftware/stillhouse/backend/internal/server"
	"github.com/gallowaysoftware/stillhouse/backend/internal/version"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	logger.Info("stillhouse starting",
		"version", version.Version, "commit", version.Commit, "built", version.BuildDate,
		"release", version.IsRelease())

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	logger.Info("config loaded", "cfg", cfg.String())

	// Production deploys point ADMIN_DATABASE_URL at the superuser DSN so
	// the server can apply migrations + (optionally) rotate the app role
	// password before switching to its own less-privileged DATABASE_URL.
	// In dev we leave ADMIN_DATABASE_URL unset; the Makefile drives
	// `migrate up` separately.
	if cfg.AdminDatabaseURL != "" {
		if err := dbmigrate.Up(cfg.AdminDatabaseURL, logger); err != nil {
			logger.Error("migrate", "err", err)
			os.Exit(1)
		}
		if cfg.AppRolePassword != "" {
			if err := rotateAppPassword(cfg.AdminDatabaseURL, cfg.AppRolePassword, logger); err != nil {
				logger.Error("rotate app password", "err", err)
				os.Exit(1)
			}
		}
	}

	loadAlcoholometricTables(cfg.AlcoholometricTablesPath, logger)

	srv, err := server.New(cfg, logger)
	if err != nil {
		logger.Error("server init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			logger.Error("listen failed", "err", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

// rotateAppPassword issues ALTER ROLE stillhouse_app PASSWORD against the
// admin DSN. Used at boot so the operator can drop a new password in .env
// without having to psql into the container.
//
// Quoted with pq's "$$...$$" dollar-quoting to dodge any quoting games
// from the supplied password; embedding $$ in the password is the one case
// it can't handle and is rejected up front.
func rotateAppPassword(adminDSN, newPassword string, logger *slog.Logger) error {
	if containsDollarQuote(newPassword) {
		return errors.New("STILLHOUSE_APP_PASSWORD contains the forbidden sequence '$$'")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }() // one-shot admin conn; the process is exiting either way
	_, err = conn.Exec(ctx, "ALTER ROLE stillhouse_app PASSWORD $$"+newPassword+"$$")
	if err != nil {
		return err
	}
	logger.Info("stillhouse_app password rotated")
	return nil
}

func containsDollarQuote(s string) bool {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '$' && s[i+1] == '$' {
			return true
		}
	}
	return false
}

// loadAlcoholometricTables reads the operator's copy of the Canadian
// Alcoholometric Tables, if they've supplied one.
//
// A missing or unreadable file is deliberately not fatal. Everything
// except temperature correction works without it, and a distillery that
// has just pulled a new image should not find its whole system down
// because of a mount typo — it should find one clear line in the log and
// a banner in Settings.
func loadAlcoholometricTables(path string, logger *slog.Logger) {
	if path == "" {
		logger.Warn("alcoholometric tables not configured; temperature correction is unavailable",
			"how", alcoholometry.ErrNotLoaded.Error())
		return
	}
	if err := alcoholometry.Load(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.Warn("alcoholometric tables not installed; temperature correction is unavailable",
				"looked_in", path,
				"how", "download the ZIP from CRA and put it there — see the deploy guide")
			return
		}
		logger.Error("alcoholometric tables could not be read; temperature correction is unavailable",
			"path", path, "err", err)
		return
	}
	logger.Info("alcoholometric tables loaded",
		"file", alcoholometry.SourceName(), "rows", alcoholometry.RowCount())
}
