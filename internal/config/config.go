// Package config loads and validates all runtime configuration from the
// environment. It is the only package that reads configuration from the
// environment (os.Getenv) — every other package, including the standalone
// migration tool (via DatabaseURL), receives its configuration from here.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

	// Stripe (TEST MODE ONLY). Empty disables the corresponding feature.
	StripeSecretKey     string
	StripeWebhookSecret string
	StripeProPriceID    string
	PublicBaseURL       string // base URL for Checkout success/cancel redirects

	// Background reconciliation job.
	ReconcileInterval  time.Duration // 0 disables the job
	ReconcileBatchSize int
}

// ReconcileEnabled reports whether the background reconciliation job should run.
func (c *Config) ReconcileEnabled() bool { return c.ReconcileInterval > 0 }

// StripeCheckoutEnabled reports whether Checkout can be served.
func (c *Config) StripeCheckoutEnabled() bool {
	return c.StripeSecretKey != "" && c.StripeProPriceID != ""
}

// StripeWebhookEnabled reports whether webhooks can be verified.
func (c *Config) StripeWebhookEnabled() bool { return c.StripeWebhookSecret != "" }

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

// Sane upper bounds. They keep the service configured within reason AND prevent
// overflow when casting to int32 (pool sizes) or multiplying into a time.Duration
// (minutes/seconds -> nanoseconds). All are well within those type ranges.
const (
	maxPoolConns      = 10000  // int32-safe
	maxDurationUnits  = 525600 // minutes in a year; *time.Minute stays int64-safe
	maxShutdownSecond = 86400  // one day
)

// Load reads configuration from the environment, applies defaults, and validates.
// A malformed integer value is a hard error (it is never silently defaulted), and
// every integer is range-checked before it is cast, so no cast can overflow.
func Load() (*Config, error) {
	l := &envLoader{}
	databaseURL := os.Getenv("DATABASE_URL")
	port := l.str("PORT", "8080")
	appEnv := l.str("APP_ENV", "development")
	maxConns := l.intVal("DB_MAX_CONNS", 10)
	minConns := l.intVal("DB_MIN_CONNS", 0)
	lifetimeMin := l.intVal("DB_MAX_CONN_LIFETIME_MINUTES", 60)
	idleMin := l.intVal("DB_MAX_CONN_IDLE_MINUTES", 30)
	shutdownSec := l.intVal("SHUTDOWN_TIMEOUT_SECONDS", 10)
	reconcileSec := l.intVal("RECONCILE_INTERVAL_SECONDS", 3600)
	reconcileBatch := l.intVal("RECONCILE_BATCH_SIZE", 100)
	if l.err != nil {
		return nil, l.err
	}

	// Range-check raw ints BEFORE any int32/Duration conversion.
	if databaseURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required")
	}
	if err := checkPort(port); err != nil {
		return nil, err
	}
	if err := checkRange("DB_MAX_CONNS", maxConns, 1, maxPoolConns); err != nil {
		return nil, err
	}
	if err := checkRange("DB_MIN_CONNS", minConns, 0, maxConns); err != nil {
		return nil, err
	}
	if err := checkRange("DB_MAX_CONN_LIFETIME_MINUTES", lifetimeMin, 1, maxDurationUnits); err != nil {
		return nil, err
	}
	if err := checkRange("DB_MAX_CONN_IDLE_MINUTES", idleMin, 1, maxDurationUnits); err != nil {
		return nil, err
	}
	if err := checkRange("SHUTDOWN_TIMEOUT_SECONDS", shutdownSec, 1, maxShutdownSecond); err != nil {
		return nil, err
	}
	if err := checkRange("RECONCILE_INTERVAL_SECONDS", reconcileSec, 0, 604800); err != nil { // 0 disables; max 1 week
		return nil, err
	}
	if err := checkRange("RECONCILE_BATCH_SIZE", reconcileBatch, 1, 100000); err != nil {
		return nil, err
	}

	stripeSecret := os.Getenv("STRIPE_SECRET_KEY")
	// Refuse live keys outright — this service is test mode only.
	if stripeSecret != "" && !strings.HasPrefix(stripeSecret, "sk_test_") {
		return nil, fmt.Errorf("config: STRIPE_SECRET_KEY must be a test-mode key (sk_test_...)")
	}

	return &Config{
		DatabaseURL:         databaseURL,
		Port:                port,
		AppEnv:              appEnv,
		DBMaxConns:          int32(maxConns),
		DBMinConns:          int32(minConns),
		DBMaxConnLifetime:   time.Duration(lifetimeMin) * time.Minute,
		DBMaxConnIdleTime:   time.Duration(idleMin) * time.Minute,
		ShutdownTimeout:     time.Duration(shutdownSec) * time.Second,
		StripeSecretKey:     stripeSecret,
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripeProPriceID:    os.Getenv("STRIPE_PRICE_PRO"),
		PublicBaseURL:       getenvDefault("PUBLIC_BASE_URL", "http://localhost:8080"),
		ReconcileInterval:   time.Duration(reconcileSec) * time.Second,
		ReconcileBatchSize:  reconcileBatch,
	}, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func checkPort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("config: PORT must be an integer, got %q", port)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("config: PORT must be between 1 and 65535, got %d", n)
	}
	return nil
}

func checkRange(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("config: %s must be between %d and %d, got %d", name, lo, hi, v)
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
