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
}

func Load() (*Config, error) {
	c := &Config{
		Addr:        getenv("STILLHOUSE_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Dev:         os.Getenv("STILLHOUSE_DEV") == "1",
		StaticDir:   os.Getenv("STILLHOUSE_STATIC_DIR"),
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
