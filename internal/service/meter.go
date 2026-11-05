package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/money"
	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/repository"
)

// MeterService implements the billable-request flow: a single READ COMMITTED
// transaction that claims an idempotency key, enforces quota atomically, writes
// the usage ledger, and stores a replayable response.
type MeterService struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewMeterService(pool *pgxpool.Pool, logger *slog.Logger) *MeterService {
	return &MeterService{pool: pool, logger: logger}
}

// Generate processes one POST /v1/generate. It is safe under retries and
// concurrency: the unique (tenant_id, idempotency_key) index is the serialization
// point, and the counter + ledger + idempotency record commit or roll back together.
func (s *MeterService) Generate(ctx context.Context, tenantID, idemKey string, in GenerateInput) (Result, error) {
	reqHash := hashRequest(in)

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Result{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after a successful commit

	idemID, claimed, err := repository.ClaimIdempotencyKey(ctx, tx, tenantID, idemKey, reqHash)
	if err != nil {
		return Result{}, fmt.Errorf("claim idempotency: %w", err)
	}

	// --- Key already exists: replay or conflict, never re-execute. ---
	if !claimed {
		rec, err := repository.GetIdempotencyRecord(ctx, tx, tenantID, idemKey)
		if err != nil {
			return Result{}, fmt.Errorf("load idempotency: %w", err)
		}
		if rec.RequestHash != reqHash {
			j, _ := json.Marshal(errorBody("idempotency_conflict", "idempotency key reused with a different request body"))
			return Result{Status: 409, Body: j}, nil
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, fmt.Errorf("commit replay: %w", err)
		}
		return Result{Status: rec.ResponseStatus, Body: rec.ResponseBody}, nil
	}

	// --- We own this request. ---
	sub, found, err := repository.GetActiveSubscription(ctx, tx, tenantID)
	if err != nil {
		return Result{}, fmt.Errorf("load subscription: %w", err)
	}
	if !found || sub.Status != "active" {
		return s.finish(ctx, tx, idemID, 402, errorBody("payment_required", "no active subscription for tenant"))
	}

	limits, err := repository.GetPlanLimits(ctx, tx, sub.PlanID)
	if err != nil {
		return Result{}, fmt.Errorf("load plan limits: %w", err)
	}

	period := currentPeriod()
	totalTokens := in.InputTokens + in.CachedInputTokens + in.OutputTokens + in.ReasoningTokens

	if err := repository.EnsureCounter(ctx, tx, tenantID, period); err != nil {
		return Result{}, fmt.Errorf("ensure counter: %w", err)
	}

	ok, apiUsed, tokUsed, err := repository.TryConsumeQuota(ctx, tx, tenantID, period, 1, totalTokens, limits.APICallLimit, limits.AITokenLimit)
	if err != nil {
		return Result{}, fmt.Errorf("consume quota: %w", err)
	}
	if !ok {
		curAPI, curTok, _, err := repository.GetCounter(ctx, tx, tenantID, period)
		if err != nil {
			return Result{}, fmt.Errorf("read counter: %w", err)
		}
		body := map[string]any{
			"error":   "quota_exceeded",
			"message": "monthly usage quota exceeded",
			"quota":   quotaBlockFor(limits, curAPI, curTok),
		}
		return s.finish(ctx, tx, idemID, 429, body)
	}

	// Append the immutable ledger rows (one per dimension).
	if err := repository.InsertAPICallEvent(ctx, tx, tenantID, idemID); err != nil {
		return Result{}, fmt.Errorf("insert api_call event: %w", err)
	}
	if err := repository.InsertAITokenEvent(ctx, tx, tenantID, idemID, in.InputTokens, in.CachedInputTokens, in.OutputTokens, in.ReasoningTokens); err != nil {
		return Result{}, fmt.Errorf("insert ai_tokens event: %w", err)
	}

	cost := money.CostMicroUSD(1, money.TokenUsage{
		InputTokens:       in.InputTokens,
		CachedInputTokens: in.CachedInputTokens,
		OutputTokens:      in.OutputTokens,
		ReasoningTokens:   in.ReasoningTokens,
	})
	body := map[string]any{
		"status": "ok",
		"usage": map[string]any{
			"api_calls":           1,
			"input_tokens":        in.InputTokens,
			"cached_input_tokens": in.CachedInputTokens,
			"output_tokens":       in.OutputTokens,
			"reasoning_tokens":    in.ReasoningTokens,
		},
		"cost_micro_usd": cost,
		"quota":          quotaBlockFor(limits, apiUsed, tokUsed),
	}
	return s.finish(ctx, tx, idemID, 200, body)
}

// finish stores the response on the idempotency record (satisfying the deferred
// completeness trigger) and commits. Rejections (402/429) are stored too, so a
// retried rejected request replays the same response.
func (s *MeterService) finish(ctx context.Context, tx pgx.Tx, idemID string, status int, body any) (Result, error) {
	j, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("marshal response: %w", err)
	}
	if err := repository.CompleteIdempotency(ctx, tx, idemID, status, j); err != nil {
		return Result{}, fmt.Errorf("complete idempotency: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{Status: status, Body: j}, nil
}

// hashRequest hashes the NORMALIZED request (the struct marshals in a fixed field
// order), so semantically identical requests share a hash regardless of raw JSON
// formatting.
func hashRequest(in GenerateInput) string {
	j, _ := json.Marshal(in)
	sum := sha256.Sum256(j)
	return hex.EncodeToString(sum[:])
}
