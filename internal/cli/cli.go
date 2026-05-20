// Package cli implements the three salience subcommands: init, bench, report.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/envfile"
	"github.com/salience-cli/salience/internal/provider"
	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/runner"
	"github.com/salience-cli/salience/internal/store"
)

// RunInit writes a starter config + .env into the current directory.
func RunInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "directory to write starter files into")
	force := fs.Bool("force", false, "overwrite existing files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(*dir, "salience.json")
	envPath := filepath.Join(*dir, ".env")
	if err := writeIfAbsent(cfgPath, []byte(config.StarterJSON), *force); err != nil {
		return err
	}
	if err := writeIfAbsent(envPath, []byte(config.StarterEnv), *force); err != nil {
		return err
	}
	fmt.Printf("wrote %s\nwrote %s\n", cfgPath, envPath)
	fmt.Println("next: fill in API keys in .env, edit salience.json, then run `salience bench`.")
	return nil
}

func writeIfAbsent(path string, data []byte, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; pass -force to overwrite", path)
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// RunBench executes the benchmark and persists results.
func RunBench(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	cfgPath := fs.String("config", "salience.json", "path to JSON config")
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	resume := fs.Bool("resume", false, "resume the most recent unfinished run")
	maxCost := fs.Float64("max-cost", 0, "abort if estimated total cost exceeds this many USD (0 = no cap)")
	yes := fs.Bool("yes", false, "skip the cost-preview confirmation prompt")
	dryRun := fs.Bool("dry-run", false, "print the cost preview and exit without calling any provider")
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

	providers, err := buildProviders(cfg)
	if err != nil {
		return err
	}

	// Cost preview, budget cap, and confirmation gate.
	ests, total := runner.Estimate(cfg, providers)
	runner.PrintEstimate(os.Stdout, ests, total)
	if *maxCost > 0 && total > *maxCost {
		return fmt.Errorf("estimated cost $%.4f exceeds -max-cost $%.4f; aborting", total, *maxCost)
	}
	if *dryRun {
		fmt.Println("(dry-run: no calls made)")
		return nil
	}
	if !*yes {
		fmt.Print("Proceed? [y/N] ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("aborted")
			return nil
		}
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	var resumeID int64
	if *resume {
		resumeID, err = st.LatestUnfinishedRunID(ctx)
		if err != nil {
			return err
		}
		if resumeID == 0 {
			fmt.Println("no unfinished run to resume; starting a new run")
		}
	}

	r := &runner.Runner{
		Cfg:       cfg,
		Providers: providers,
		Store:     st,
		Out:       os.Stdout,
	}
	runID, err := r.Run(ctx, resumeID)
	if err != nil {
		return err
	}
	fmt.Printf("run #%d persisted to %s\n", runID, *dbPath)
	return nil
}

// RunReport renders a report from persisted data.
func RunReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	format := fs.String("format", "markdown", "output format: markdown or html")
	outPath := fs.String("out", "", "output file path (empty = stdout)")
	runID := fs.Int64("run", 0, "run id to report on (0 = latest)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		// No data yet — emit a friendly placeholder so smoke tests succeed.
		msg := fmt.Sprintf("# Salience report\n\nNo data found at %s. Run `salience bench` first.\n", *dbPath)
		return writeOutput(*outPath, msg)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	id := *runID
	if id == 0 {
		id, err = st.LatestRunID(ctx)
		if err != nil {
			return err
		}
		if id == 0 {
			msg := "# Salience report\n\nDatabase contains no runs yet. Run `salience bench` first.\n"
			return writeOutput(*outPath, msg)
		}
	}

	meta, err := st.GetRun(ctx, id)
	if err != nil {
		return fmt.Errorf("load run #%d: %w", id, err)
	}
	user, brands, err := report.LoadBrandsFromConfigJSON(ctx, meta.ConfigJSON)
	if err != nil {
		return fmt.Errorf("decode embedded config: %w", err)
	}
	samples, err := st.ListSamples(ctx, id)
	if err != nil {
		return err
	}
	data := report.BuildWithBrands(id, meta, samples, user, brands)

	f := report.Format(strings.ToLower(*format))
	switch f {
	case report.Markdown, report.HTML, report.JSON, report.CSV:
	default:
		return fmt.Errorf("unknown format %q (use markdown, html, json, or csv)", *format)
	}

	if *outPath == "" {
		return report.Render(os.Stdout, data, f)
	}
	fh, err := os.Create(*outPath)
	if err != nil {
		return err
	}
	defer fh.Close()
	return report.Render(fh, data, f)
}

func writeOutput(path, s string) error {
	if path == "" {
		_, err := fmt.Fprint(os.Stdout, s)
		return err
	}
	return os.WriteFile(path, []byte(s), 0o644)
}

// buildProviders converts each configured provider entry into a live provider
// client, reading API keys from the environment. OPENAI_BASE_URL and
// ANTHROPIC_BASE_URL are honored as endpoint overrides — useful for tests
// or for proxying via a local LLM gateway. A missing API key for a
// configured provider is a fatal error before any network call.
func buildProviders(cfg *config.Config) ([]provider.Provider, error) {
	var out []provider.Provider
	for _, p := range cfg.Providers {
		switch p.Kind {
		case "openai":
			key := os.Getenv("OPENAI_API_KEY")
			if key == "" {
				return nil, fmt.Errorf("provider %q needs OPENAI_API_KEY", p.Name)
			}
			c := provider.NewOpenAI(p.Name, p.Model, key)
			if url := os.Getenv("OPENAI_BASE_URL"); url != "" {
				c.Endpoint = url
			}
			out = append(out, c)
		case "anthropic":
			key := os.Getenv("ANTHROPIC_API_KEY")
			if key == "" {
				return nil, fmt.Errorf("provider %q needs ANTHROPIC_API_KEY", p.Name)
			}
			c := provider.NewAnthropic(p.Name, p.Model, key)
			if url := os.Getenv("ANTHROPIC_BASE_URL"); url != "" {
				c.Endpoint = url
			}
			out = append(out, c)
		case "perplexity":
			key := os.Getenv("PERPLEXITY_API_KEY")
			if key == "" {
				return nil, fmt.Errorf("provider %q needs PERPLEXITY_API_KEY", p.Name)
			}
			c := provider.NewPerplexity(p.Name, p.Model, key)
			if url := os.Getenv("PERPLEXITY_BASE_URL"); url != "" {
				c.Endpoint = url
			}
			out = append(out, c)
		default:
			return nil, fmt.Errorf("unsupported provider kind %q", p.Kind)
		}
	}
	return out, nil
}
