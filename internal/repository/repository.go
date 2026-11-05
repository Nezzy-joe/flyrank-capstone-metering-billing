// Package repository owns all SQL. Every function takes a Querier, which both
// *pgxpool.Pool and pgx.Tx satisfy, so the same queries run inside or outside a
// transaction. No SQL lives anywhere else (services compose these; handlers never
// call them directly).
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is the subset of pgx used here; *pgxpool.Pool and pgx.Tx both satisfy it.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Subscription is a tenant's current plan + status.
type Subscription struct {
	PlanID string
	Status string
}

// PlanLimits are the monthly entitlements for a plan.
type PlanLimits struct {
	PlanID       string
	APICallLimit int64
	AITokenLimit int64
}

// IdempotencyRecord is a stored request claim + its replayable response.
type IdempotencyRecord struct {
	ID             string
	RequestHash    string
	ResponseStatus int
	ResponseBody   []byte
}

// TokenTotals is a per-period category breakdown summed from the ledger.
type TokenTotals struct {
	APICalls          int64
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	ReasoningTokens   int64
}

// ResolveTenantByKeyHash returns the tenant id for a non-revoked API-key hash.
func ResolveTenantByKeyHash(ctx context.Context, q Querier, keyHash string) (tenantID string, found bool, err error) {
	err = q.QueryRow(ctx,
		`SELECT t.id::text
		   FROM tenant_api_keys k
		   JOIN tenants t ON t.id = k.tenant_id
		  WHERE k.key_hash = $1 AND k.revoked_at IS NULL`,
		keyHash).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return tenantID, true, nil
}

// GetActiveSubscription returns the tenant's single subscription row.
func GetActiveSubscription(ctx context.Context, q Querier, tenantID string) (Subscription, bool, error) {
	var s Subscription
	err := q.QueryRow(ctx,
		`SELECT plan_id, status FROM subscriptions WHERE tenant_id = $1`,
		tenantID).Scan(&s.PlanID, &s.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, false, nil
	}
	if err != nil {
		return Subscription{}, false, err
	}
	return s, true, nil
}

// GetPlanLimits returns a plan's monthly limits.
func GetPlanLimits(ctx context.Context, q Querier, planID string) (PlanLimits, error) {
	var p PlanLimits
	err := q.QueryRow(ctx,
		`SELECT id, api_call_limit, ai_token_limit FROM plans WHERE id = $1`,
		planID).Scan(&p.PlanID, &p.APICallLimit, &p.AITokenLimit)
	return p, err
}

// ClaimIdempotencyKey inserts the claim and returns (id, true) to the winner.
// On conflict it returns ("", false) without error — the key already exists.
func ClaimIdempotencyKey(ctx context.Context, q Querier, tenantID, key, requestHash string) (id string, claimed bool, err error) {
	err = q.QueryRow(ctx,
		`INSERT INTO idempotency_keys (tenant_id, idempotency_key, request_hash)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		 RETURNING id::text`,
		tenantID, key, requestHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// GetIdempotencyRecord fetches an existing claim (for replay / hash comparison).
func GetIdempotencyRecord(ctx context.Context, q Querier, tenantID, key string) (IdempotencyRecord, error) {
	var r IdempotencyRecord
	err := q.QueryRow(ctx,
		`SELECT id::text, request_hash, COALESCE(response_status, 0), response_body
		   FROM idempotency_keys
		  WHERE tenant_id = $1 AND idempotency_key = $2`,
		tenantID, key).Scan(&r.ID, &r.RequestHash, &r.ResponseStatus, &r.ResponseBody)
	return r, err
}

// CompleteIdempotency stores the response on the claim before the tx commits
// (the deferred trigger trg_idem_require_complete requires this at commit).
func CompleteIdempotency(ctx context.Context, q Querier, id string, status int, body []byte) error {
	_, err := q.Exec(ctx,
		`UPDATE idempotency_keys
		    SET response_status = $2, response_body = $3, completed_at = now()
		  WHERE id = $1`,
		id, status, string(body))
	return err
}

// EnsureCounter creates the (tenant, period) counter row if absent.
func EnsureCounter(ctx context.Context, q Querier, tenantID, period string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO usage_counters (tenant_id, period) VALUES ($1, $2)
		 ON CONFLICT (tenant_id, period) DO NOTHING`,
		tenantID, period)
	return err
}

// TryConsumeQuota atomically increments the counter only if BOTH dimensions stay
// within their limits. ok=false means the quota would be exceeded (no change made).
func TryConsumeQuota(ctx context.Context, q Querier, tenantID, period string, apiCalls, tokens, apiLimit, tokenLimit int64) (ok bool, apiUsed, tokUsed int64, err error) {
	err = q.QueryRow(ctx,
		`UPDATE usage_counters
		    SET api_calls_used = api_calls_used + $3,
		        ai_tokens_used = ai_tokens_used + $4,
		        updated_at = now()
		  WHERE tenant_id = $1 AND period = $2
		    AND api_calls_used + $3 <= $5
		    AND ai_tokens_used + $4 <= $6
		 RETURNING api_calls_used, ai_tokens_used`,
		tenantID, period, apiCalls, tokens, apiLimit, tokenLimit).Scan(&apiUsed, &tokUsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, 0, nil
	}
	if err != nil {
		return false, 0, 0, err
	}
	return true, apiUsed, tokUsed, nil
}

// GetCounter returns current usage for (tenant, period); found=false if no row.
func GetCounter(ctx context.Context, q Querier, tenantID, period string) (apiUsed, tokUsed int64, found bool, err error) {
	err = q.QueryRow(ctx,
		`SELECT api_calls_used, ai_tokens_used FROM usage_counters WHERE tenant_id = $1 AND period = $2`,
		tenantID, period).Scan(&apiUsed, &tokUsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return apiUsed, tokUsed, true, nil
}

// InsertAPICallEvent appends the api_call ledger row (quantity fixed at 1).
func InsertAPICallEvent(ctx context.Context, q Querier, tenantID, idempotencyKeyID string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO usage_events (tenant_id, idempotency_key_id, type, quantity)
		 VALUES ($1, $2, 'api_call', 1)`,
		tenantID, idempotencyKeyID)
	return err
}

// InsertAITokenEvent appends the ai_tokens ledger row with its category breakdown.
func InsertAITokenEvent(ctx context.Context, q Querier, tenantID, idempotencyKeyID string, input, cached, output, reasoning int64) error {
	_, err := q.Exec(ctx,
		`INSERT INTO usage_events
		   (tenant_id, idempotency_key_id, type, input_tokens, cached_input_tokens, output_tokens, reasoning_tokens)
		 VALUES ($1, $2, 'ai_tokens', $3, $4, $5, $6)`,
		tenantID, idempotencyKeyID, input, cached, output, reasoning)
	return err
}

// SumTokenCategories aggregates the ledger for a tenant's period (for cost + audit).
func SumTokenCategories(ctx context.Context, q Querier, tenantID, period string) (TokenTotals, error) {
	var t TokenTotals
	err := q.QueryRow(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN type = 'api_call' THEN quantity ELSE 0 END), 0),
		   COALESCE(SUM(input_tokens), 0),
		   COALESCE(SUM(cached_input_tokens), 0),
		   COALESCE(SUM(output_tokens), 0),
		   COALESCE(SUM(reasoning_tokens), 0)
		 FROM usage_events
		 WHERE tenant_id = $1 AND to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM') = $2`,
		tenantID, period).Scan(&t.APICalls, &t.InputTokens, &t.CachedInputTokens, &t.OutputTokens, &t.ReasoningTokens)
	return t, err
}
