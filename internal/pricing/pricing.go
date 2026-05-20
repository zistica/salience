// Package pricing holds a small per-model USD pricing table and a helper to
// estimate the cost of a single request given token counts.
package pricing

import "strings"

// Rate is a per-million-token price pair for input and output tokens.
type Rate struct {
	InputPerM  float64
	OutputPerM float64
}

// table is the maintained list. Prices are USD per 1,000,000 tokens and are
// approximations intended only for budgeting; they are not authoritative.
var table = map[string]Rate{
	// OpenAI
	"gpt-4o":              {InputPerM: 2.50, OutputPerM: 10.00},
	"gpt-4o-mini":         {InputPerM: 0.15, OutputPerM: 0.60},
	"gpt-4-turbo":         {InputPerM: 10.00, OutputPerM: 30.00},
	"gpt-4.1":             {InputPerM: 2.00, OutputPerM: 8.00},
	"gpt-4.1-mini":        {InputPerM: 0.40, OutputPerM: 1.60},
	"o3-mini":             {InputPerM: 1.10, OutputPerM: 4.40},

	// Anthropic — Claude 3.x
	"claude-3-5-sonnet-latest":   {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-sonnet-20241022": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-haiku-latest":    {InputPerM: 0.80, OutputPerM: 4.00},
	"claude-3-opus-latest":       {InputPerM: 15.00, OutputPerM: 75.00},

	// Anthropic — Claude 4.x family (current generation)
	"claude-haiku-4-5":  {InputPerM: 1.00, OutputPerM: 5.00},
	"claude-sonnet-4-5": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-sonnet-4-6": {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-opus-4-5":   {InputPerM: 15.00, OutputPerM: 75.00},
	"claude-opus-4-7":   {InputPerM: 15.00, OutputPerM: 75.00},

	// Perplexity (sonar family — includes built-in web search; per-call
	// search fees are not modeled here, only the token rates).
	"sonar":               {InputPerM: 1.00, OutputPerM: 1.00},
	"sonar-pro":           {InputPerM: 3.00, OutputPerM: 15.00},
	"sonar-reasoning":     {InputPerM: 1.00, OutputPerM: 5.00},
	"sonar-reasoning-pro": {InputPerM: 2.00, OutputPerM: 8.00},
}

// Lookup returns the recorded rate for an exact model id, falling back to
// prefix matching so that snapshot variants ("gpt-4o-2024-08-06") match the
// family entry. A zero Rate is returned when nothing matches.
func Lookup(model string) Rate {
	if r, ok := table[model]; ok {
		return r
	}
	model = strings.ToLower(model)
	for k, r := range table {
		lk := strings.ToLower(k)
		if strings.HasPrefix(model, lk) || strings.HasPrefix(lk, model) {
			return r
		}
	}
	return Rate{}
}

// Estimate returns the USD cost for a request with the given token counts.
func Estimate(model string, inputTokens, outputTokens int) float64 {
	r := Lookup(model)
	in := float64(inputTokens) / 1_000_000.0 * r.InputPerM
	out := float64(outputTokens) / 1_000_000.0 * r.OutputPerM
	return in + out
}
