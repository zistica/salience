package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/envfile"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/provider"
	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/store"
)

// RunAdvise asks every configured LLM, for each prompt the user is losing,
// what the user's brand would need to demonstrate to earn the recommendation.
// Results are persisted to the advice table for later aggregation.
func RunAdvise(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("advise", flag.ContinueOnError)
	cfgPath := fs.String("config", "salience.json", "path to JSON config")
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	runID := fs.Int64("run", 0, "run id (0 = latest)")
	onlyPrompt := fs.String("prompt", "", "limit to a single prompt (default: every losing prompt)")
	minGap := fs.Float64("min-gap", 0.10, "consider a prompt losing if your gap is at least this many points below 0")
	maxCost := fs.Float64("max-cost", 0, "abort if estimated cost exceeds this many USD (0 = no cap)")
	yes := fs.Bool("yes", false, "skip the cost confirmation prompt")
	dryRun := fs.Bool("dry-run", false, "print the plan and exit without calling any provider")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = envfile.Load(".env")

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no database at %s", *dbPath)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	id := *runID
	if id == 0 {
		if id, err = st.LatestRunID(ctx); err != nil {
			return err
		}
		if id == 0 {
			return fmt.Errorf("no runs persisted")
		}
	}

	providers, err := buildProviders(cfg)
	if err != nil {
		return err
	}

	// Reuse the report builder to find losing prompts (gap < -minGap).
	meta, err := st.GetRun(ctx, id)
	if err != nil {
		return err
	}
	user, comps, err := report.LoadCompetitorsFromConfigJSON(ctx, meta.ConfigJSON)
	if err != nil {
		return err
	}
	samples, err := st.ListSamples(ctx, id)
	if err != nil {
		return err
	}
	rep := report.Build(id, meta, samples, user, comps)

	type loser struct {
		prompt      string
		winnerBrand string
	}
	var losers []loser
	for _, c := range rep.Cells {
		if c.Gap > -*minGap {
			continue
		}
		if *onlyPrompt != "" && c.Prompt != *onlyPrompt {
			continue
		}
		// Pick the winner = highest non-user rate.
		winner, best := "", 0.0
		for _, comp := range rep.Competitors {
			if r := c.Rates[comp]; r > best {
				best = r
				winner = comp
			}
		}
		losers = append(losers, loser{prompt: c.Prompt, winnerBrand: winner})
	}
	// Dedup by prompt — multiple providers can flag the same losing prompt.
	seen := map[string]bool{}
	uniq := losers[:0]
	for _, l := range losers {
		if seen[l.prompt] {
			continue
		}
		seen[l.prompt] = true
		uniq = append(uniq, l)
	}
	losers = uniq

	if len(losers) == 0 {
		fmt.Println("nothing to advise on — you are not losing any prompt by ≥", pctFromFloat(*minGap), "in this run.")
		return nil
	}

	// One advice call per (losing prompt × configured provider).
	type ask struct {
		prompt      string
		winner      string
		client      provider.Provider
	}
	var asks []ask
	for _, l := range losers {
		for _, p := range providers {
			asks = append(asks, ask{prompt: l.prompt, winner: l.winnerBrand, client: p})
		}
	}

	// Cost estimate: ~200 prompt tokens, ~400 completion tokens per ask.
	estCost := 0.0
	for _, a := range asks {
		r := pricing.Lookup(a.client.Model())
		estCost += float64(200)/1e6*r.InputPerM + float64(400)/1e6*r.OutputPerM
	}
	fmt.Printf("Advise plan: %d ask(s) across %d losing prompt(s) × %d provider(s).\n",
		len(asks), len(losers), len(providers))
	fmt.Printf("Estimated cost: $%.4f\n", estCost)
	if *maxCost > 0 && estCost > *maxCost {
		return fmt.Errorf("estimated cost $%.4f exceeds -max-cost $%.4f; aborting", estCost, *maxCost)
	}
	if *dryRun {
		return nil
	}
	if !*yes {
		fmt.Print("Proceed? [y/N] ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Println("aborted")
			return nil
		}
	}

	sem := make(chan struct{}, max1(cfg.Concurrency))
	var wg sync.WaitGroup
	var ok, failed int
	var mu sync.Mutex

	for _, a := range asks {
		a := a
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			text := fmt.Sprintf(
				"I represent %q, a competitor in this market. For the question %q, "+
					"you currently appear to recommend %q. Be candid: what specific, verifiable things "+
					"would %q need to demonstrate publicly (content, references, partnerships, product evidence, "+
					"third-party reviews, community presence, anything) so a future answer to the same question "+
					"would recommend %q over %q? Give a numbered list of concrete, ranked actions. Skip generic "+
					"advice; be specific to this question.",
				cfg.Brand.Name, a.prompt, a.winner, cfg.Brand.Name, cfg.Brand.Name, a.winner)
			temp := 0.5
			resp, err := a.client.Call(ctx, text, 800, &temp)
			rec := store.AdviceRecord{
				RunID:        id,
				ProviderName: a.client.Name(),
				Model:        a.client.Model(),
				AskerBrand:   cfg.Brand.Name,
				WinnerBrand:  a.winner,
				Prompt:       a.prompt,
			}
			if err != nil {
				rec.Error = err.Error()
				mu.Lock()
				failed++
				mu.Unlock()
			} else {
				rec.Advice = resp.Text
				rec.InputTokens = resp.InputTokens
				rec.OutputTokens = resp.OutputTokens
				rec.CostUSD = pricing.Estimate(a.client.Model(), resp.InputTokens, resp.OutputTokens)
				mu.Lock()
				ok++
				mu.Unlock()
			}
			if err := st.InsertAdvice(context.Background(), rec); err != nil {
				fmt.Fprintf(os.Stderr, "  insert error: %v\n", err)
			}
			mu.Lock()
			fmt.Fprintf(os.Stdout, "  [%d/%d] %s :: %s\n",
				ok+failed, len(asks), a.client.Name(), clip(a.prompt, 50))
			mu.Unlock()
		}()
	}
	wg.Wait()
	fmt.Printf("\nDone. %d ok, %d errored. Run `salience playbook` to combine with sources + explain.\n", ok, failed)
	return nil
}

func pctFromFloat(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

// sortStrings is a tiny helper used by the dashboard to render losing-prompt
// lists in a stable order.
func sortStrings(xs []string) []string {
	sort.Strings(xs)
	return xs
}

var _ = sortStrings
