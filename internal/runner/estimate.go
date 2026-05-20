package runner

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/provider"
)

// avgCompletionTokens is the rough mean number of completion tokens we
// assume per call when no real data is available yet. Chosen to be a
// reasonable upper bound for a search-grounded recommendation answer.
const avgCompletionTokens = 350

// PerProviderEstimate is the cost breakdown for one provider before bench
// runs.
type PerProviderEstimate struct {
	Name           string
	Model          string
	Calls          int
	InputTokens    int
	OutputTokens   int
	EstimatedUSD   float64
	PriceFound     bool
}

// Estimate predicts cost based on planned call count and a rough token
// budget. Prompts contribute their actual length (≈4 chars/token);
// completions assume avgCompletionTokens.
//
// Total cost is dominated by completion tokens in practice, so this is a
// useful sanity check even though it's approximate.
func Estimate(cfg *config.Config, providers []provider.Provider) ([]PerProviderEstimate, float64) {
	promptTokens := 0
	for _, p := range cfg.Prompts {
		promptTokens += approxTokens(p)
	}

	regionsCount := len(cfg.Regions)
	if regionsCount == 0 {
		regionsCount = 1
	}

	var out []PerProviderEstimate
	total := 0.0
	for _, prov := range providers {
		calls := len(cfg.Prompts) * cfg.SamplesPer * regionsCount
		input := promptTokens * cfg.SamplesPer * regionsCount
		output := calls * avgCompletionTokens
		rate := pricing.Lookup(prov.Model())
		cost := pricing.Estimate(prov.Model(), input, output)
		out = append(out, PerProviderEstimate{
			Name:         prov.Name(),
			Model:        prov.Model(),
			Calls:        calls,
			InputTokens:  input,
			OutputTokens: output,
			EstimatedUSD: cost,
			PriceFound:   rate.InputPerM > 0 || rate.OutputPerM > 0,
		})
		total += cost
	}
	return out, total
}

// approxTokens is the classic "1 token ≈ 4 characters" GPT heuristic, adapted
// to count runes so it works for CJK text too.
func approxTokens(s string) int {
	return utf8.RuneCountInString(s)/4 + 1
}

// PrintEstimate writes a human-readable preview to out.
func PrintEstimate(out io.Writer, ests []PerProviderEstimate, total float64) {
	var b strings.Builder
	b.WriteString("Cost preview:\n")
	for _, e := range ests {
		note := ""
		if !e.PriceFound {
			note = " (price unknown — using fallback model)"
		}
		fmt.Fprintf(&b, "  %-20s %-30s %4d calls ≈ $%.4f%s\n",
			e.Name, e.Model, e.Calls, e.EstimatedUSD, note)
	}
	fmt.Fprintf(&b, "  %s\n", strings.Repeat("─", 60))
	fmt.Fprintf(&b, "  Total estimate: $%.4f\n", total)
	_, _ = out.Write([]byte(b.String()))
}
