package config

import "testing"

func TestLoad(t *testing.T) {
	t.Run("missing DATABASE_URL is rejected", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for missing DATABASE_URL, got nil")
		}
	})

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

	t.Run("invalid pool bounds rejected", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("DB_MAX_CONNS", "5")
		t.Setenv("DB_MIN_CONNS", "9")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for DB_MIN_CONNS > DB_MAX_CONNS")
		}
	})
}
