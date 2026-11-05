// Package database owns the PostgreSQL connection pool (pgx/pgxpool). It is the
// only package that knows about the SQL driver. Repositories (added in a later
// phase) will depend on the *pgxpool.Pool it returns; HTTP handlers never do.
//
// Migrations are intentionally NOT run here — they are a separate, explicit step
// (see cmd/migrate and `make migrate-up`). The API assumes the schema is present.
package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
)

// New builds a connection pool from config, applies pool tuning, and verifies
// connectivity with a Ping before returning. The caller owns Close().
func New(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("database: parse DATABASE_URL: %w", err)
	}

	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.DBMaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("database: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}
	return pool, nil
}
