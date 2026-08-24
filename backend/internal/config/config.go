// Package config loads runtime configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
)

type Config struct {
	Addr        string
	DatabaseURL string
	Dev         bool
	StaticDir   string // optional; if set, the server serves the SPA from this directory

	// AdminDatabaseURL is the superuser DSN. When set, the server runs
	// embedded migrations on startup and optionally rotates the app role
	// password before switching to DatabaseURL. Production deploys (Docker
	// compose stack) lean on this so the operator doesn't need to remember
	// a separate migrate step.
	AdminDatabaseURL string
	// AppRolePassword, when set together with AdminDatabaseURL, is applied
	// to the stillhouse_app role on startup via ALTER ROLE. Lets the
	// operator override the dev-default password baked into migration
	// 000010 without an out-of-band psql step.
	AppRolePassword string

	// SelfServeSignup opens tenant creation to the public. OFF by
	// default, and the default is the decision rather than a placeholder.
	//
	// CreateTenant is the bootstrap endpoint and refuses once one tenant
	// exists, which is what makes a single-distillery install safe to
	// expose. Turning this on removes that refusal, so an installation
	// reachable from the internet becomes one where anybody can create a
	// distillery. That is correct for a hosted service and wrong for the
	// box in somebody's rackhouse, and Stillhouse cannot tell which it is
	// running on — so the operator says.
	SelfServeSignup bool

	// BaseURL is the public origin where the app is reachable (no trailing
	// slash). Used to build absolute URLs in emails — password-reset
	// links etc. Defaults to http://localhost:8080 when empty so dev
	// emails are still clickable from the console.
	BaseURL string

	// AlcoholometricTablesPath points at the Canadian Alcoholometric
	// Tables 1980 as downloaded from CRA — the ZIP, the ALC_TAB.TXT
	// inside it, or a directory holding either. They aren't shipped with
	// Stillhouse (Crown material; commercial redistribution needs written
	// permission), so each operator supplies their own copy. Empty means
	// temperature correction is unavailable and the rest of the app runs
	// normally.
	AlcoholometricTablesPath string

	// TrustProxyHeaders says a reverse proxy in front of this server sets
	// X-Forwarded-For / X-Real-IP and strips any the client sent. Only
	// then are those headers safe to rate-limit on; otherwise any caller
	// can pick a fresh identity per request and never be throttled.
	TrustProxyHeaders bool
}

// defaultAlcoholometricTablesPath is where the compose stack mounts the
// operator's copy. Defaulting to it means the documented deploy needs no
// extra environment variable at all — drop the file in the data directory
// and it's found.
const defaultAlcoholometricTablesPath = "/data/alcoholometric-tables"

func Load() (*Config, error) {
	c := &Config{
		Addr:             getenv("STILLHOUSE_ADDR", ":8080"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		Dev:              os.Getenv("STILLHOUSE_DEV") == "1",
		StaticDir:        os.Getenv("STILLHOUSE_STATIC_DIR"),
		AdminDatabaseURL: os.Getenv("ADMIN_DATABASE_URL"),
		AppRolePassword:  os.Getenv("STILLHOUSE_APP_PASSWORD"),
		BaseURL:          os.Getenv("STILLHOUSE_BASE_URL"),

		AlcoholometricTablesPath: getenv("STILLHOUSE_ALCOHOLOMETRIC_TABLES",
			defaultAlcoholometricTablesPath),
		TrustProxyHeaders: os.Getenv("STILLHOUSE_TRUST_PROXY_HEADERS") == "1",
		SelfServeSignup:   os.Getenv("STILLHOUSE_SELF_SERVE_SIGNUP") == "1",
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *Config) String() string {
	return fmt.Sprintf("Config{Addr=%s Dev=%t}", c.Addr, c.Dev)
}
