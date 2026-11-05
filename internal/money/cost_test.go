package money

import "testing"

func TestCostMicroUSD_WorkedFixture(t *testing.T) {
	// input 1000 (uncached), cached 300, output 2500, reasoning 200.
	// Independently calculated (micro-USD, per-category floor):
	//   input:     1000 * 300000   / 1e6 = 300
	//   cached:     300 *  30000   / 1e6 =   9
	//   output:    2500 * 2500000  / 1e6 = 6250
	//   reasoning:  200 * 2500000  / 1e6 =  500
	//   api_call:     1 * 0              =   0
	//   total                            = 7059
	got := CostMicroUSD(1, TokenUsage{
		InputTokens: 1000, CachedInputTokens: 300, OutputTokens: 2500, ReasoningTokens: 200,
	})
	if got != 7059 {
		t.Fatalf("CostMicroUSD = %d, want 7059", got)
	}
}

func TestCachedCheaperThanInput(t *testing.T) {
	const n = 1_000_000
	input := CostMicroUSD(0, TokenUsage{InputTokens: n})
	cached := CostMicroUSD(0, TokenUsage{CachedInputTokens: n})
	if cached >= input {
		t.Fatalf("cached (%d) must be cheaper than input (%d)", cached, input)
	}
	if input != 300_000 || cached != 30_000 {
		t.Fatalf("rates wrong: input=%d cached=%d", input, cached)
	}
}

func TestReasoningBilledAtOutputRate(t *testing.T) {
	const n = 1_000_000
	output := CostMicroUSD(0, TokenUsage{OutputTokens: n})
	reasoning := CostMicroUSD(0, TokenUsage{ReasoningTokens: n})
	if output != reasoning {
		t.Fatalf("reasoning (%d) must equal output rate (%d)", reasoning, output)
	}
}

func TestCategoriesIndependent_NoDoubleCount(t *testing.T) {
	// The sum of individual category costs equals the combined cost — categories
	// are summed as money, never merged as tokens.
	u := TokenUsage{InputTokens: 111, CachedInputTokens: 222, OutputTokens: 333, ReasoningTokens: 444}
	combined := CostMicroUSD(0, u)
	parts := CostMicroUSD(0, TokenUsage{InputTokens: u.InputTokens}) +
		CostMicroUSD(0, TokenUsage{CachedInputTokens: u.CachedInputTokens}) +
		CostMicroUSD(0, TokenUsage{OutputTokens: u.OutputTokens}) +
		CostMicroUSD(0, TokenUsage{ReasoningTokens: u.ReasoningTokens})
	if combined != parts {
		t.Fatalf("combined %d != sum of parts %d", combined, parts)
	}
}
