package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/envfile"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/store"
)

// RunExpand asks one configured provider to brainstorm realistic prompt
// variations based on the user's existing prompts and brand domain. The
// answers are persisted as PromptSuggestion rows for review.
func RunExpand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("expand", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project name or id (default: latest)")
	count := fs.Int("count", 20, "how many prompt variations to ask for")
	yes := fs.Bool("yes", false, "skip the cost confirmation prompt")
	dryRun := fs.Bool("dry-run", false, "print the plan and exit without calling any provider")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = envfile.Load(".env")

	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no database at %s", *dbPath)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	proj, err := pickProject(ctx, st, *projectKey)
	if err != nil {
		return err
	}
	cfg, err := projectConfig(proj)
	if err != nil {
		return err
	}
	providers, err := buildProviders(cfg)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return fmt.Errorf("project has no providers configured")
	}

	estIn := 200 + 8*len(cfg.Prompts)
	estOut := 50 * *count
	estCost := pricing.Estimate(providers[0].Model(), estIn, estOut)
	fmt.Printf("Expand plan: ask %s for %d prompt variations (est. $%.4f).\n",
		providers[0].Name(), *count, estCost)
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

	prompt := fmt.Sprintf(
		"You are helping a brand named %q (in the same product category implied "+
			"by the following sample customer queries) discover related prompts a "+
			"real customer might type when looking for a recommendation.\n\n"+
			"Sample queries:\n%s\n\n"+
			"Return exactly %d additional realistic customer queries as a JSON "+
			"array of objects with two keys: \"text\" (the query) and \"rationale\" "+
			"(one short sentence on what kind of customer asks this). Output ONLY "+
			"the JSON array — no preamble, no markdown fence.",
		cfg.Brand.Name, bulletList(cfg.Prompts), *count)

	temp := 0.7
	resp, err := providers[0].Call(ctx, prompt, 1500, &temp)
	if err != nil {
		return fmt.Errorf("provider call: %w", err)
	}

	// Robustly find the JSON array — LLMs sometimes still add prose.
	body := extractJSONArray(resp.Text)
	var suggestions []struct {
		Text      string `json:"text"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(body), &suggestions); err != nil {
		return fmt.Errorf("could not parse LLM JSON output: %w\n\n--- raw response ---\n%s", err, resp.Text)
	}

	existing := map[string]bool{}
	for _, p := range cfg.Prompts {
		existing[strings.ToLower(strings.TrimSpace(p))] = true
	}

	saved := 0
	for _, s := range suggestions {
		t := strings.TrimSpace(s.Text)
		if t == "" || existing[strings.ToLower(t)] {
			continue
		}
		if _, err := st.InsertPromptSuggestion(ctx, store.PromptSuggestion{
			ProjectID: proj.ID,
			Text:      t,
			Rationale: strings.TrimSpace(s.Rationale),
		}); err == nil {
			saved++
		}
	}
	fmt.Printf("Persisted %d new suggestions (skipped duplicates). View with `salience expand list -project %s`.\n",
		saved, proj.Slug)
	return nil
}

// RunExpandList prints staged suggestions.
func RunExpandList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("expand-list", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project name or id (default: latest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	proj, err := pickProject(ctx, st, *projectKey)
	if err != nil {
		return err
	}
	sugg, err := st.ListPromptSuggestions(ctx, proj.ID)
	if err != nil {
		return err
	}
	if len(sugg) == 0 {
		fmt.Println("no suggestions yet — run `salience expand` first")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tACCEPTED\tTEXT\tRATIONALE")
	for _, s := range sugg {
		acc := " "
		if s.Accepted {
			acc = "✓"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", s.ID, acc, clip(s.Text, 70), clip(s.Rationale, 50))
	}
	return tw.Flush()
}

// pickProject resolves -project flag or returns the most recent project.
func pickProject(ctx context.Context, st *store.Store, key string) (*store.Project, error) {
	if key != "" {
		return st.GetProjectBySlugOrName(ctx, key)
	}
	id, err := st.LatestProjectID(ctx)
	if err != nil {
		return nil, err
	}
	if id == 0 {
		return nil, fmt.Errorf("no projects exist yet — create one via `salience project new` or the dashboard")
	}
	return st.GetProject(ctx, id)
}

// projectConfig hydrates a store.Project into the runner-facing config.
// Duplicates the server's projectToConfig but kept inline so the CLI
// package doesn't depend on the server package.
func projectConfig(p *store.Project) (*config.Config, error) {
	var brand config.Brand
	var comps []config.Brand
	var prompts []string
	var provs []config.Provider
	if err := json.Unmarshal([]byte(p.BrandJSON), &brand); err != nil {
		return nil, fmt.Errorf("brand: %w", err)
	}
	_ = json.Unmarshal([]byte(p.CompetitorsJSON), &comps)
	_ = json.Unmarshal([]byte(p.PromptsJSON), &prompts)
	_ = json.Unmarshal([]byte(p.ProvidersJSON), &provs)
	cfg := &config.Config{
		Brand:       brand,
		Competitors: comps,
		Prompts:     prompts,
		Providers:   provs,
		SamplesPer:  p.SamplesPerPrompt,
		Concurrency: p.ConcurrencyPerProvider,
		MaxTokens:   p.MaxTokens,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func bulletList(items []string) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString("  - ")
		b.WriteString(it)
		b.WriteString("\n")
	}
	return b.String()
}

// extractJSONArray finds the first balanced `[ ... ]` JSON array in s,
// handling realistic LLM output: code fences (```json ... ```), prose
// preamble before the array, and trailing prose after it. String content
// inside the array — including strings that contain `[` or `]` — is
// skipped so a quote-embedded bracket doesn't confuse the depth counter.
//
// Returns the original input on no-match (callers can choose to surface
// the raw text in the error path).
func extractJSONArray(s string) string {
	// Strip Markdown code fences if present (```json ... ``` or ``` ... ```).
	if idx := strings.Index(s, "```"); idx >= 0 {
		rest := s[idx+3:]
		// Drop any language tag on the same line.
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			s = rest[:end]
		} else {
			s = rest
		}
	}

	start := strings.IndexByte(s, '[')
	if start < 0 {
		return s
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if escape {
			escape = false
			continue
		}
		if inString {
			switch ch {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[start : i+1])
			}
		}
	}
	return strings.TrimSpace(s[start:])
}
