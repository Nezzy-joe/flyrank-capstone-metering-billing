// Command seed creates demo data: one tenant, its Free subscription, and a usable
// API key. It is safe to run repeatedly — the tenant and subscription are
// find-or-create, so no duplicates are made. Each run issues a NEW API key and
// prints the raw value exactly once (it is never written to a file or committed).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/apikey"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
)

const demoTenantName = "Demo Tenant"

func main() {
	if err := run(); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run() error {
	dbURL, err := config.DatabaseURL()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	rawKey, hash, prefix, err := apikey.Generate()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Find-or-create the demo tenant (idempotent).
	var tenantID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM tenants WHERE name = $1 LIMIT 1`, demoTenantName).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id::text`, demoTenantName).Scan(&tenantID); err != nil {
			return fmt.Errorf("create tenant: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("find tenant: %w", err)
	}

	// Ensure exactly one Free, active subscription (idempotent).
	if _, err = tx.Exec(ctx,
		`INSERT INTO subscriptions (tenant_id, plan_id, status) VALUES ($1, 'free', 'active')
		 ON CONFLICT (tenant_id) DO NOTHING`, tenantID); err != nil {
		return fmt.Errorf("ensure subscription: %w", err)
	}

	// Issue a new API key (only the hash is stored).
	if _, err = tx.Exec(ctx,
		`INSERT INTO tenant_api_keys (tenant_id, key_hash, key_prefix) VALUES ($1, $2, $3)`,
		tenantID, hash, prefix); err != nil {
		return fmt.Errorf("create api key: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	fmt.Println("Demo tenant ready.")
	fmt.Println("  tenant_id:", tenantID)
	fmt.Println("  plan:      free (active)")
	fmt.Println("  API key (shown once — copy it now, it is not stored anywhere):")
	fmt.Println("    " + rawKey)
	return nil
}
