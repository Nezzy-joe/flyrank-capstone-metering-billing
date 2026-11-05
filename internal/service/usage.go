package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/money"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/repository"
)

// ErrNoSubscription is returned when a tenant has no subscription to report on.
var ErrNoSubscription = errors.New("no active subscription")

// UsageService reports a tenant's current-period usage and cost.
type UsageService struct {
	pool *pgxpool.Pool
}

func NewUsageService(pool *pgxpool.Pool) *UsageService {
	return &UsageService{pool: pool}
}

// UsageView is the GET /v1/usage response.
type UsageView struct {
	Plan         string        `json:"plan"`
	Period       string        `json:"period"`
	APICalls     DimensionView `json:"api_calls"`
	AITokens     DimensionView `json:"ai_tokens"`
	CostMicroUSD int64         `json:"cost_micro_usd"`
}

// Usage returns usage for the tenant's current UTC calendar month. Everything is
// scoped by tenantID, so one tenant can never see another's usage.
func (u *UsageService) Usage(ctx context.Context, tenantID string) (UsageView, error) {
	sub, found, err := repository.GetActiveSubscription(ctx, u.pool, tenantID)
	if err != nil {
		return UsageView{}, err
	}
	if !found {
		return UsageView{}, ErrNoSubscription
	}

	limits, err := repository.GetPlanLimits(ctx, u.pool, sub.PlanID)
	if err != nil {
		return UsageView{}, err
	}

	period := currentPeriod()
	apiUsed, tokUsed, _, err := repository.GetCounter(ctx, u.pool, tenantID, period)
	if err != nil {
		return UsageView{}, err
	}

	// Cost is computed from the ledger's category totals (authoritative money math).
	totals, err := repository.SumTokenCategories(ctx, u.pool, tenantID, period)
	if err != nil {
		return UsageView{}, err
	}
	cost := money.CostMicroUSD(totals.APICalls, money.TokenUsage{
		InputTokens:       totals.InputTokens,
		CachedInputTokens: totals.CachedInputTokens,
		OutputTokens:      totals.OutputTokens,
		ReasoningTokens:   totals.ReasoningTokens,
	})

	return UsageView{
		Plan:         sub.PlanID,
		Period:       period,
		APICalls:     dimension(apiUsed, limits.APICallLimit),
		AITokens:     dimension(tokUsed, limits.AITokenLimit),
		CostMicroUSD: cost,
	}, nil
}
