// Package money holds the cost model. All money is integer micro-USD
// (1 USD = 1,000,000 micro-USD); there is no floating-point arithmetic anywhere.
//
// Pricing is pinned here (in code), NOT in the plans table — plans hold quota
// entitlements, this file holds the money math.
//
// Provider : Google Gemini API
// Model    : Gemini 2.5 Flash
// Tier     : Standard
// Source   : https://aistudio.google.com/docs/pricing
// Verified : 2026-11-05
// Rates (per 1,000,000 tokens):
//
//	input (text)            $0.30
//	cached input (caching)  $0.03   (cheaper than uncached input)
//	output                  $2.50
//	reasoning / thinking    billed at the OUTPUT rate (not a separate category)
//
// Gemini bills by tokens, not per API call, so the per-API-call price is 0.
package money

const (
	// MicroUSDPerUSD is the fixed-point scale: 1 USD = 1,000,000 micro-USD.
	MicroUSDPerUSD = 1_000_000

	tokensPerUnit = 1_000_000 // rates are expressed per this many tokens

	rateInputPerM     = 300_000        // $0.30 / 1M tokens, in micro-USD
	rateCachedPerM    = 30_000         // $0.03 / 1M tokens
	rateOutputPerM    = 2_500_000      // $2.50 / 1M tokens
	rateReasoningPerM = rateOutputPerM // reasoning billed at the output rate
	pricePerAPICall   = 0              // Gemini has no per-call fee
)
