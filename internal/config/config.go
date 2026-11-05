// Package config loads and validates all runtime configuration from the
// environment. It is the ONLY package that reads os.Getenv; every other package
// receives a typed *Config so configuration is explicit and testable.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the typed, validated runtime configuration.
type Config struct {
	DatabaseURL string // required, e.g. postgres://user:pass@host:5432/db?sslmode=disable
	Port        string // HTTP listen port
	AppEnv      string // "development" | "production" | ...

	// pgx pool tuning
	DBMaxConns        int32
	DBMinConns        int32
	DBMaxConnLifetime time.Duration
	DBMaxConnIdleTime time.Duration

	// server lifecycle
	ShutdownTimeout time.Duration
}

// Load reads configuration from the environment, applies defaults, and validates.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		Port:              getenvDefault("PORT", "8080"),
		AppEnv:            getenvDefault("APP_ENV", "development"),
		DBMaxConns:        int32(getenvInt("DB_MAX_CONNS", 10)),
		DBMinConns:        int32(getenvInt("DB_MIN_CONNS", 0)),
		DBMaxConnLifetime: time.Duration(getenvInt("DB_MAX_CONN_LIFETIME_MINUTES", 60)) * time.Minute,
		DBMaxConnIdleTime: time.Duration(getenvInt("DB_MAX_CONN_IDLE_MINUTES", 30)) * time.Minute,
		ShutdownTimeout:   time.Duration(getenvInt("SHUTDOWN_TIMEOUT_SECONDS", 10)) * time.Second,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL is required")
	}
	if c.DBMaxConns <= 0 {
		return fmt.Errorf("config: DB_MAX_CONNS must be > 0, got %d", c.DBMaxConns)
	}
	if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("config: DB_MIN_CONNS must be between 0 and DB_MAX_CONNS (%d), got %d", c.DBMaxConns, c.DBMinConns)
	}
	return nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
