package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/envfile"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/store"
)

// RunBrief generates a content brief for one losing prompt by combining
// (1) the run's report data, (2) scraped competitor citation content,
// (3) stored explain/advice rows, and asking an LLM to produce a
// concrete Markdown brief.
func RunBrief(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("brief", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project name or id (default: latest)")
	runID := fs.Int64("run", 0, "source run id (0 = latest)")
	promptText := fs.String("prompt", "", "exact prompt text to brief on (required)")
	out := fs.String("out", "", "write the brief to this file (default: persist to DB and print)")
	yes := fs.Bool("yes", false, "skip the cost confirmation prompt")
	dryRun := fs.Bool("dry-run", false, "show the plan and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*promptText) == "" {
		return fmt.Errorf("usage: salience brief -prompt \"the losing prompt\" [-project N] [-run M] [-out file.md]")
	}
	_ = envfile.Load(".env")

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

	id := *runID
	if id == 0 {
		if id, err = st.LatestRunID(ctx); err != nil {
			return err
		}
		if id == 0 {
			return fmt.Errorf("no runs persisted")
		}
	}

	// Gather the context we'll feed into the LLM.
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

	var cell *report.Cell
	for i := range rep.Cells {
		if rep.Cells[i].Prompt == *promptText {
			cell = &rep.Cells[i]
			break
		}
	}
	if cell == nil {
		return fmt.Errorf("prompt %q not found in run #%d — run salience runs / show to find it", *promptText, id)
	}

	// Winner (top competitor on this cell).
	winner, winnerRate := "", 0.0
	for _, c := range rep.Competitors {
		if r := cell.Rates[c]; r > winnerRate {
			winnerRate = r
			winner = c
		}
	}

	// Scraped pages cited in this run (we don't filter by cell — citations
	// often overlap across cells).
	scrapedURLs := topScrapedURLs(ctx, st, id, 6)

	// LLM-stated competitor strengths for the winner.
	explainNotes := loadExplain(ctx, st, id, winner, 4)

	// LLM-stated recommended actions for this prompt.
	adviceNotes := loadAdvice(ctx, st, id, *promptText, 4)

	systemPrompt := composeBriefPrompt(cfg.Brand.Name, *promptText, winner, winnerRate,
		cell.Rates[user], scrapedURLs, explainNotes, adviceNotes)

	estCost := pricing.Estimate(providers[0].Model(), len(systemPrompt)/4, 1200)
	fmt.Printf("Brief plan: ask %s, est. $%.4f.\n", providers[0].Name(), estCost)
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

	temp := 0.4
	resp, err := providers[0].Call(ctx, systemPrompt, 1800, &temp)
	if err != nil {
		return fmt.Errorf("provider call: %w", err)
	}
	brief := strings.TrimSpace(resp.Text)

	briefID, err := st.InsertContentBrief(ctx, store.ContentBrief{
		ProjectID:    proj.ID,
		Prompt:       *promptText,
		BodyMarkdown: brief,
		SourceRunID:  id,
	})
	if err != nil {
		return err
	}

	if *out != "" {
		if err := os.WriteFile(*out, []byte(brief+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Printf("brief #%d saved to DB and written to %s\n", briefID, *out)
	} else {
		fmt.Printf("brief #%d saved to DB:\n\n", briefID)
		fmt.Println(brief)
	}
	return nil
}

// composeBriefPrompt builds the full request to send to the LLM.
func composeBriefPrompt(brand, prompt, winner string, winnerRate, brandRate float64,
	scrapedURLs []string, explainNotes, adviceNotes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a senior content strategist helping the brand %q produce a piece "+
		"of content that will increase its visibility in answers from large language models.\n\n", brand)
	fmt.Fprintf(&b, "The customer query we are targeting: %q\n\n", prompt)
	fmt.Fprintf(&b, "Current state:\n"+
		"  - %s mention rate: %.0f%%\n"+
		"  - %s (top competitor) mention rate: %.0f%%\n\n",
		brand, brandRate*100, winner, winnerRate*100)
	if len(scrapedURLs) > 0 {
		fmt.Fprintf(&b, "Pages the LLMs are citing for this category (real, scraped content):\n")
		for _, s := range scrapedURLs {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
		b.WriteString("\n")
	}
	if len(explainNotes) > 0 {
		fmt.Fprintf(&b, "Why the LLMs say %s wins:\n", winner)
		for _, n := range explainNotes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
		b.WriteString("\n")
	}
	if len(adviceNotes) > 0 {
		fmt.Fprintf(&b, "Actions the LLMs themselves recommend for %s:\n", brand)
		for _, n := range adviceNotes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
		b.WriteString("\n")
	}
	b.WriteString("Produce a content brief in Markdown with these exact sections, in this order:\n\n" +
		"1. **Working title** — one line, optimized to be discoverable.\n" +
		"2. **Format** — blog post / comparison page / landing page / Reddit answer / etc.\n" +
		"3. **Target placement** — exact URL or domain category to publish on (e.g. own blog, G2 review listing, Reddit thread).\n" +
		"4. **Audience** — one sentence describing who is asking the query.\n" +
		"5. **Outline** — 5–8 H2 headers with one-line descriptions each.\n" +
		"6. **Key claims to make** — bulleted, specific, ideally with hooks to be cited.\n" +
		"7. **Competitor framing** — how to acknowledge competitors honestly while showcasing the brand's edge.\n" +
		"8. **Discoverability** — concrete steps to get this indexed (cross-posts, SEO meta, structured data).\n\n" +
		"Be specific, not generic. Avoid filler. Skip preamble.\n")
	return b.String()
}

func topScrapedURLs(ctx context.Context, st *store.Store, runID int64, n int) []string {
	srcJSON, _ := st.SourcesJSONByRun(ctx, runID)
	// Pull every URL referenced, then check scraped_pages.
	urls := map[string]int{}
	for _, raw := range srcJSON {
		var sources []detect.Source
		_ = json.Unmarshal([]byte(raw), &sources)
		for _, s := range sources {
			if s.URL == "" {
				continue
			}
			urls[s.URL]++
		}
	}
	type kv struct {
		u string
		c int
	}
	var sorted []kv
	for u, c := range urls {
		sorted = append(sorted, kv{u, c})
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].c > sorted[j].c })

	var out []string
	for _, kv := range sorted {
		if len(out) >= n {
			break
		}
		page, err := st.GetScrapedPage(ctx, kv.u)
		if err != nil || page == nil || page.Title == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s — %s", page.Title, kv.u))
	}
	return out
}

func loadExplain(ctx context.Context, st *store.Store, runID int64, brand string, max int) []string {
	rows, err := st.ListExplanations(ctx, runID, brand)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range rows {
		if r.Error != "" || strings.TrimSpace(r.Reasoning) == "" {
			continue
		}
		out = append(out, firstSentencesShort(r.Reasoning, 2))
		if len(out) >= max {
			break
		}
	}
	return out
}

func loadAdvice(ctx context.Context, st *store.Store, runID int64, prompt string, max int) []string {
	rows, err := st.ListAdvice(ctx, runID)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range rows {
		if r.Prompt != prompt || r.Error != "" || strings.TrimSpace(r.Advice) == "" {
			continue
		}
		out = append(out, firstSentencesShort(r.Advice, 3))
		if len(out) >= max {
			break
		}
	}
	return out
}

func firstSentencesShort(s string, n int) string {
	s = strings.TrimSpace(s)
	count := 0
	for i, r := range s {
		switch r {
		case '.', '!', '?', '。', '！', '？', '\n':
			count++
			if count >= n {
				return strings.TrimSpace(s[:i+1])
			}
		}
	}
	return s
}
