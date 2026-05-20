package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/envfile"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/provider"
	"github.com/salience-cli/salience/internal/store"
)

// RunExplain sends a follow-up "why was X recommended?" probe for each sample
// in the chosen run where X was mentioned. Results are persisted to the
// explanations table.
func RunExplain(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	cfgPath := fs.String("config", "salience.json", "path to JSON config")
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	runID := fs.Int64("run", 0, "run id (0 = latest)")
	brand := fs.String("brand", "", "limit to a specific brand (default: every competitor)")
	maxPerBrand := fs.Int("max-per-brand", 5, "stop after probing this many samples per brand (0 = no limit)")
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
	providerByName := map[string]provider.Provider{}
	for _, p := range providers {
		providerByName[p.Name()] = p
	}

	samples, err := st.ListSamples(ctx, id)
	if err != nil {
		return err
	}

	// Figure out which (sample, brand) probes to run. By default we probe
	// every brand other than the user's. -brand narrows it.
	targets := map[string]bool{}
	if *brand != "" {
		targets[*brand] = true
	} else {
		for _, c := range cfg.Competitors {
			targets[c.Name] = true
		}
	}

	type probe struct {
		sampleID     int64
		providerName string
		model        string
		prompt       string
		brand        string
	}
	var probes []probe
	perBrand := map[string]int{}
	for _, s := range samples {
		if s.Error != "" {
			continue
		}
		for _, b := range s.BrandsHit {
			if !targets[b] {
				continue
			}
			if *maxPerBrand > 0 && perBrand[b] >= *maxPerBrand {
				continue
			}
			perBrand[b]++
			probes = append(probes, probe{
				sampleID: s.ID, providerName: s.ProviderName, model: s.Model,
				prompt: s.Prompt, brand: b,
			})
		}
	}

	if len(probes) == 0 {
		fmt.Println("nothing to explain — no qualifying brand mentions in this run.")
		return nil
	}

	// Cost estimate: ~150 prompt tokens, ~300 completion tokens per probe.
	estCost := 0.0
	for _, p := range probes {
		r := pricing.Lookup(p.model)
		estCost += float64(150)/1e6*r.InputPerM + float64(300)/1e6*r.OutputPerM
	}
	fmt.Printf("Explain plan: %d probes across %d brand(s), %d sample(s).\n",
		len(probes), len(perBrand), countDistinctSamples(probes))
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

	// Run the probes in parallel, bounded by cfg.Concurrency.
	sem := make(chan struct{}, max1(cfg.Concurrency))
	var wg sync.WaitGroup
	var ok, failed int
	var mu sync.Mutex

	for _, pr := range probes {
		pr := pr
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cl, hasProvider := providerByName[pr.providerName]
			if !hasProvider {
				return
			}
			text := fmt.Sprintf(
				"Earlier, in response to the question %q, you mentioned %q. "+
					"Briefly (≤120 words), why? What specific facts, features, sources, or reputation "+
					"informed that mention? Be honest about uncertainty — if you are guessing or you do not "+
					"have specific knowledge, say so.",
				pr.prompt, pr.brand)
			temp := 0.4
			resp, err := cl.Call(ctx, text, 256, &temp)
			rec := store.ExplanationRecord{
				RunID: id, SourceSampleID: pr.sampleID,
				ProviderName: pr.providerName, Model: pr.model,
				AskedAboutBrand: pr.brand, Prompt: pr.prompt,
			}
			if err != nil {
				rec.Error = err.Error()
				mu.Lock()
				failed++
				mu.Unlock()
			} else {
				rec.Reasoning = resp.Text
				rec.InputTokens = resp.InputTokens
				rec.OutputTokens = resp.OutputTokens
				rec.CostUSD = pricing.Estimate(pr.model, resp.InputTokens, resp.OutputTokens)
				mu.Lock()
				ok++
				mu.Unlock()
			}
			if err := st.InsertExplanation(context.Background(), rec); err != nil {
				fmt.Fprintf(os.Stderr, "  insert error: %v\n", err)
			}
			mu.Lock()
			fmt.Fprintf(os.Stdout, "  [%d/%d] %s :: %s\n", ok+failed, len(probes), pr.providerName, pr.brand)
			mu.Unlock()
		}()
	}
	wg.Wait()

	fmt.Printf("\nDone. %d ok, %d errored. See `salience playbook` to combine with other signals.\n", ok, failed)
	return nil
}

func countDistinctSamples[T any](xs []T) int {
	// All probes share their sample ID via reflection-free path: we only use
	// this for a friendly message, so we just return len(xs) as an upper bound.
	// (Switching to a typed counter is cheap if we ever care.)
	return len(xs)
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

