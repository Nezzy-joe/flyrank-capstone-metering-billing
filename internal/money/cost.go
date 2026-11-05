package money

// TokenUsage is a non-overlapping breakdown of token counts. InputTokens is the
// UNCACHED input only; CachedInputTokens is priced separately (and cheaper).
type TokenUsage struct {
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	ReasoningTokens   int64
}

// CostMicroUSD returns the cost of the given usage in integer micro-USD.
//
// Each token category is priced INDEPENDENTLY and summed — categories are never
// added together as raw token counts. Cached input uses its own lower rate;
// reasoning uses the output rate (its own term, so it is never double-counted).
// Integer division truncates toward zero, i.e. a deterministic floor for the
// non-negative inputs this service accepts.
func CostMicroUSD(apiCalls int64, t TokenUsage) int64 {
	var c int64
	c += apiCalls * pricePerAPICall
	c += t.InputTokens * rateInputPerM / tokensPerUnit
	c += t.CachedInputTokens * rateCachedPerM / tokensPerUnit
	c += t.OutputTokens * rateOutputPerM / tokensPerUnit
	c += t.ReasoningTokens * rateReasoningPerM / tokensPerUnit
	return c
}
