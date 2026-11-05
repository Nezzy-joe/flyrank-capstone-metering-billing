// Package service holds the business rules and owns transaction boundaries. It
// composes repository calls; it never speaks HTTP and never writes SQL directly.
package service

import (
	"time"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/repository"
)

// GenerateInput is the validated, normalized token breakdown for one billable
// request. InputTokens is UNCACHED input only.
type GenerateInput struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
}

// Result is the HTTP outcome a caller should return. Body is the exact JSON that
// was stored for idempotent replay, so replays are byte-identical.
type Result struct {
	Status int
	Body   []byte
}

// DimensionView is one quota dimension in API responses.
type DimensionView struct {
	Used      int64 `json:"used"`
	Remaining int64 `json:"remaining"`
	Limit     int64 `json:"limit"`
}

// currentPeriod is the UTC calendar month, matching usage_counters.period.
func currentPeriod() string { return time.Now().UTC().Format("2006-01") }

func errorBody(code, msg string) map[string]any {
	return map[string]any{"error": code, "message": msg}
}

func dimension(used, limit int64) DimensionView {
	rem := limit - used
	if rem < 0 {
		rem = 0
	}
	return DimensionView{Used: used, Remaining: rem, Limit: limit}
}

func quotaBlockFor(limits repository.PlanLimits, apiUsed, tokUsed int64) map[string]any {
	return map[string]any{
		"api_calls": dimension(apiUsed, limits.APICallLimit),
		"ai_tokens": dimension(tokUsed, limits.AITokenLimit),
	}
}
