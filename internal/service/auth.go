package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/apikey"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/repository"
)

// AuthService resolves a presented API key to its tenant.
type AuthService struct {
	pool *pgxpool.Pool
}

func NewAuthService(pool *pgxpool.Pool) *AuthService {
	return &AuthService{pool: pool}
}

// ResolveTenant hashes the raw key (SHA-256) and looks up the owning, non-revoked
// tenant. The raw key is never stored or logged.
func (a *AuthService) ResolveTenant(ctx context.Context, rawAPIKey string) (tenantID string, ok bool, err error) {
	hash := apikey.Hash(rawAPIKey)
	return repository.ResolveTenantByKeyHash(ctx, a.pool, hash)
}
