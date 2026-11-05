// Package config loads and validates all runtime configuration from the
// environment. It is the only package that reads configuration from the
// environment (os.Getenv) — every other package, including the standalone
// migration tool (via DatabaseURL), receives its configuration from here.
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
	Port        string // HTTP listen port (validated as a 1..65535 TCP port)
	AppEnv      string // "development" | "production" | ...

	// pgx pool tuning
	DBMaxConns        int32
	DBMinConns        int32
	DBMaxConnLifetime time.Duration
	DBMaxConnIdleTime time.Duration

	// server lifecycle
	ShutdownTimeout time.Duration
}

// DatabaseURL reads just the database connection string from the environment.
// The migration tool needs nothing else, so it calls this instead of loading the
// full Config — keeping all environment access inside this package.
func DatabaseURL() (string, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return "", fmt.Errorf("config: DATABASE_URL is required")
	}
	return url, nil
}

// Load reads configuration from the environment, applies defaults, and validates.
// A malformed integer value is a hard error (it is never silently defaulted).
func Load() (*Config, error) {
	l := &envLoader{}
	cfg := &Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		Port:              l.str("PORT", "8080"),
		AppEnv:            l.str("APP_ENV", "development"),
		DBMaxConns:        int32(l.intVal("DB_MAX_CONNS", 10)),
		DBMinConns:        int32(l.intVal("DB_MIN_CONNS", 0)),
		DBMaxConnLifetime: time.Duration(l.intVal("DB_MAX_CONN_LIFETIME_MINUTES", 60)) * time.Minute,
		DBMaxConnIdleTime: time.Duration(l.intVal("DB_MAX_CONN_IDLE_MINUTES", 30)) * time.Minute,
		ShutdownTimeout:   time.Duration(l.intVal("SHUTDOWN_TIMEOUT_SECONDS", 10)) * time.Second,
	}
	if l.err != nil {
		return nil, l.err
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

	port, err := strconv.Atoi(c.Port)
	if err != nil {
		return fmt.Errorf("config: PORT must be an integer, got %q", c.Port)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("config: PORT must be between 1 and 65535, got %d", port)
	}

	if c.DBMaxConns <= 0 {
		return fmt.Errorf("config: DB_MAX_CONNS must be > 0, got %d", c.DBMaxConns)
	}
	if c.DBMinConns < 0 {
		return fmt.Errorf("config: DB_MIN_CONNS must be >= 0, got %d", c.DBMinConns)
	}
	if c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("config: DB_MIN_CONNS (%d) must not exceed DB_MAX_CONNS (%d)", c.DBMinConns, c.DBMaxConns)
	}

	if c.DBMaxConnLifetime <= 0 {
		return fmt.Errorf("config: DB_MAX_CONN_LIFETIME_MINUTES must be > 0")
	}
	if c.DBMaxConnIdleTime <= 0 {
		return fmt.Errorf("config: DB_MAX_CONN_IDLE_MINUTES must be > 0")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("config: SHUTDOWN_TIMEOUT_SECONDS must be > 0")
	}
	return nil
}

// envLoader reads environment values and records the first parse error, so Load
// can fail with a clear message instead of silently falling back to a default.
type envLoader struct{ err error }

func (l *envLoader) str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (l *envLoader) intVal(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		if l.err == nil {
			l.err = fmt.Errorf("config: %s must be an integer, got %q", key, v)
		}
		return def
	}
	return n
}
