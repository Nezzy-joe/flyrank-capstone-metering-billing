package config

import "testing"

func TestLoad_Valid(t *testing.T) {
	t.Run("defaults applied when optionals unset", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
		t.Setenv("PORT", "")
		t.Setenv("APP_ENV", "")
		t.Setenv("DB_MAX_CONNS", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Port != "8080" {
			t.Errorf("Port default = %q, want 8080", cfg.Port)
		}
		if cfg.AppEnv != "development" {
			t.Errorf("AppEnv default = %q, want development", cfg.AppEnv)
		}
		if cfg.DBMaxConns != 10 {
			t.Errorf("DBMaxConns default = %d, want 10", cfg.DBMaxConns)
		}
	})

	t.Run("overrides respected", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("PORT", "9999")
		t.Setenv("APP_ENV", "production")
		t.Setenv("DB_MAX_CONNS", "25")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Port != "9999" || cfg.AppEnv != "production" || cfg.DBMaxConns != 25 {
			t.Errorf("overrides not applied: %+v", cfg)
		}
	})
}

// TestLoad_Invalid covers malformed and out-of-range configuration. Each case
// must produce an error rather than a silently-defaulted value.
func TestLoad_Invalid(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"missing DATABASE_URL", map[string]string{"DATABASE_URL": ""}},
		{"malformed DB_MAX_CONNS", map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONNS": "not-a-number"}},
		{"malformed PORT", map[string]string{"DATABASE_URL": "postgres://x", "PORT": "abc"}},
		{"PORT below range", map[string]string{"DATABASE_URL": "postgres://x", "PORT": "0"}},
		{"PORT above range", map[string]string{"DATABASE_URL": "postgres://x", "PORT": "70000"}},
		{"DB_MAX_CONNS not positive", map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONNS": "0"}},
		{"DB_MIN_CONNS negative", map[string]string{"DATABASE_URL": "postgres://x", "DB_MIN_CONNS": "-1"}},
		{"DB_MIN_CONNS exceeds max", map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONNS": "5", "DB_MIN_CONNS": "9"}},
		{"non-positive lifetime", map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONN_LIFETIME_MINUTES": "0"}},
		{"non-positive idle", map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONN_IDLE_MINUTES": "0"}},
		{"non-positive shutdown", map[string]string{"DATABASE_URL": "postgres://x", "SHUTDOWN_TIMEOUT_SECONDS": "0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestDatabaseURL(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://x")
		got, err := DatabaseURL()
		if err != nil || got != "postgres://x" {
			t.Fatalf("got %q err %v, want postgres://x nil", got, err)
		}
	})
	t.Run("missing errors", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		if _, err := DatabaseURL(); err == nil {
			t.Fatal("expected error for missing DATABASE_URL")
		}
	})
}
