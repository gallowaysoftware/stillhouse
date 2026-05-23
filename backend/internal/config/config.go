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
}

func Load() (*Config, error) {
	c := &Config{
		Addr:             getenv("STILLHOUSE_ADDR", ":8080"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		Dev:              os.Getenv("STILLHOUSE_DEV") == "1",
		StaticDir:        os.Getenv("STILLHOUSE_STATIC_DIR"),
		AdminDatabaseURL: os.Getenv("ADMIN_DATABASE_URL"),
		AppRolePassword:  os.Getenv("STILLHOUSE_APP_PASSWORD"),
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
