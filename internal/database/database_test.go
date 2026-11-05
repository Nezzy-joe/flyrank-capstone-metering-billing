package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
)

// TestNew_Connectivity is an INTEGRATION test: it needs a reachable PostgreSQL.
// It is skipped unless TEST_DATABASE_URL is set, so `go test ./...` stays green
// offline. Run it with: make test-integration (after make db-up).
func TestNew_Connectivity(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the database connectivity integration test")
	}
	t.Setenv("DATABASE_URL", url)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping after New: %v", err)
	}
}
