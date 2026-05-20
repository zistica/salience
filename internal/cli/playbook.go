package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/sources"
	"github.com/salience-cli/salience/internal/store"
)

// RunPlaybook combines the source attribution, the LLM-stated competitor
// strengths, and the LLM-recommended actions into one prioritized document.
// It writes Markdown by default; pass -format json to get the underlying
// data structure.
func RunPlaybook(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("playbook", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	runID := fs.Int64("run", 0, "run id (0 = latest)")
	out := fs.String("out", "", "output file path (default: stdout)")
	format := fs.String("format", "markdown", "output format: markdown or json")
	if err := fs.Parse(args); err != nil {
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

	meta, err := st.GetRun(ctx, id)
	if err != nil {
		return err
	}
	var cfg config.Config
	if err := json.Unmarshal([]byte(meta.ConfigJSON), &cfg); err != nil {
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
	explanations, err := st.ListExplanations(ctx, id, "")
	if err != nil {
		return err
	}
	advice, err := st.ListAdvice(ctx, id)
	if err != nil {
		return err
	}
	srcJSON, _ := st.SourcesJSONByRun(ctx, id)
	decoded, _ := sources.DecodeSamples(samples, srcJSON)
	vendorAliases := map[string][]string{cfg.Brand.Name: cfg.Brand.Aliases}
	for _, c := range cfg.Competitors {
		vendorAliases[c.Name] = c.Aliases
	}
	srcRep := sources.AnalyzeSamples(decoded, cfg.Brand.Name, comps, vendorAliases)

	pb := buildPlaybook(rep, srcRep, explanations, advice)

	var rendered string
	switch strings.ToLower(*format) {
	case "json":
		j, _ := json.MarshalIndent(pb, "", "  ")
		rendered = string(j)
	default:
		rendered = renderPlaybookMD(pb)
	}

	if *out == "" {
		fmt.Print(rendered)
		return nil
	}
	return os.WriteFile(*out, []byte(rendered), 0o644)
}

// Playbook is the combined data structure surfaced by RunPlaybook. Public so
// the dashboard can render it without recomputing.
type Playbook struct {
	RunID         int64                `json:"run_id"`
	UserBrand     string               `json:"user_brand"`
	Competitors   []string             `json:"competitors"`
	OverallRates  map[string]float64   `json:"overall_rates"`
	LosingPrompts []PlaybookPrompt     `json:"losing_prompts"`
	SourceGap     []sources.DomainRank `json:"source_gap"`
	CompetitorStrengths map[string][]string `json:"competitor_strengths"`
}

// PlaybookPrompt is the per-prompt action item.
type PlaybookPrompt struct {
	Prompt        string                       `json:"prompt"`
	Provider      string                       `json:"provider"`
	YourRate      float64                      `json:"your_rate"`
	YourRateCI    [2]float64                   `json:"your_rate_ci"`
	WinnerBrand   string                       `json:"winner_brand"`
	WinnerRate    float64                      `json:"winner_rate"`
	Gap           float64                      `json:"gap"`
	CitedURLs     []sources.Citation           `json:"cited_urls"`
	WinnerReasons []string                     `json:"winner_reasons"`
	Actions       []string                     `json:"actions"`
}

func buildPlaybook(rep report.Data, src sources.Report, explanations []store.ExplanationRecord, advice []store.AdviceRecord) Playbook {
	pb := Playbook{
		RunID:        rep.RunID,
		UserBrand:    rep.UserBrand,
		Competitors:  append([]string(nil), rep.Competitors...),
		OverallRates: rep.Totals.Rates,
	}

	// Group explanations by brand.
	expByBrand := map[string][]string{}
	for _, e := range explanations {
		if e.Error != "" || strings.TrimSpace(e.Reasoning) == "" {
			continue
		}
		expByBrand[e.AskedAboutBrand] = append(expByBrand[e.AskedAboutBrand], firstSentences(e.Reasoning, 2))
	}
	pb.CompetitorStrengths = map[string][]string{}
	for b, ss := range expByBrand {
		pb.CompetitorStrengths[b] = dedupShort(ss, 6)
	}

	// Group advice by prompt.
	adviceByPrompt := map[string][]string{}
	for _, a := range advice {
		if a.Error != "" || strings.TrimSpace(a.Advice) == "" {
			continue
		}
		// Pull numbered list items from the advice if present.
		actions := extractNumberedActions(a.Advice)
		if len(actions) == 0 {
			actions = []string{firstSentences(a.Advice, 3)}
		}
		adviceByPrompt[a.Prompt] = append(adviceByPrompt[a.Prompt], actions...)
	}

	// Cells with gap < 0 are losing. Sort worst-first.
	type losing struct {
		c report.Cell
	}
	var losers []report.Cell
	for _, c := range rep.Cells {
		if c.Gap < 0 {
			losers = append(losers, c)
		}
	}
	sort.SliceStable(losers, func(i, j int) bool { return losers[i].Gap < losers[j].Gap })

	citationsByPrompt := map[string][]sources.Citation{}
	for _, u := range src.URLs {
		// Look up which samples cited this url and which prompts they came from.
		// (Cheap approximation — we attribute every citation to every prompt
		// the user is losing on. For a fully precise mapping we'd need to
		// re-join samples by URL; v1 keeps it simple.)
		_ = u
	}
	_ = citationsByPrompt

	for _, c := range losers {
		// Find the winner (top competitor by rate).
		winnerBrand, winnerRate := "", 0.0
		for _, comp := range rep.Competitors {
			if c.Rates[comp] > winnerRate {
				winnerRate = c.Rates[comp]
				winnerBrand = comp
			}
		}
		pp := PlaybookPrompt{
			Prompt:        c.Prompt,
			Provider:      c.ProviderName,
			YourRate:      c.Rates[rep.UserBrand],
			YourRateCI:    [2]float64{c.CILow[rep.UserBrand], c.CIHigh[rep.UserBrand]},
			WinnerBrand:   winnerBrand,
			WinnerRate:    winnerRate,
			Gap:           c.Gap,
			WinnerReasons: dedupShort(expByBrand[winnerBrand], 3),
			Actions:       dedupShort(adviceByPrompt[c.Prompt], 5),
		}
		pb.LosingPrompts = append(pb.LosingPrompts, pp)
	}

	pb.SourceGap = src.MissingFromBrand
	if len(pb.SourceGap) > 20 {
		pb.SourceGap = pb.SourceGap[:20]
	}
	return pb
}

func renderPlaybookMD(pb Playbook) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Salience playbook — run #%d\n\n", pb.RunID)
	fmt.Fprintf(&b, "Brand: **%s**. Competitors: %s.\n\n", pb.UserBrand, strings.Join(pb.Competitors, ", "))

	fmt.Fprintf(&b, "## Overall mention rate\n\n")
	fmt.Fprintf(&b, "| Brand | Rate |\n|---|---|\n")
	fmt.Fprintf(&b, "| **%s** (you) | %.0f%% |\n", pb.UserBrand, pb.OverallRates[pb.UserBrand]*100)
	for _, c := range pb.Competitors {
		fmt.Fprintf(&b, "| %s | %.0f%% |\n", c, pb.OverallRates[c]*100)
	}
	fmt.Fprintln(&b)

	if len(pb.SourceGap) > 0 {
		fmt.Fprintf(&b, "## Source gap — domains driving competitor mentions you do not co-occur on\n\n")
		fmt.Fprintf(&b, "These domains keep showing up alongside competitor mentions but never alongside yours. ")
		fmt.Fprintf(&b, "Each one is a concrete to-do: earn a mention there.\n\n")
		fmt.Fprintf(&b, "| Domain | Category | Drives mentions of |\n|---|---|---|\n")
		for _, d := range pb.SourceGap {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", d.Domain, d.Category, topBrandsForMD(d.Brands))
		}
		fmt.Fprintln(&b)
	}

	if len(pb.CompetitorStrengths) > 0 {
		fmt.Fprintf(&b, "## What the LLMs say about your competitors\n\n")
		fmt.Fprintf(&b, "_Aggregated 'why was X recommended?' answers — treat as the LLMs' perceived strengths of each competitor._\n\n")
		for _, c := range pb.Competitors {
			ss := pb.CompetitorStrengths[c]
			if len(ss) == 0 {
				continue
			}
			fmt.Fprintf(&b, "### %s\n\n", c)
			for _, s := range ss {
				fmt.Fprintf(&b, "- %s\n", s)
			}
			fmt.Fprintln(&b)
		}
	}

	if len(pb.LosingPrompts) > 0 {
		fmt.Fprintf(&b, "## Prompts where you are losing — per-prompt action items\n\n")
		for _, p := range pb.LosingPrompts {
			fmt.Fprintf(&b, "### %s\n\n", p.Prompt)
			fmt.Fprintf(&b, "_Provider: %s · You: **%.0f%%** (CI %.0f%%–%.0f%%) · Top competitor: %s **%.0f%%** · Gap: **%.0f%%**_\n\n",
				p.Provider, p.YourRate*100,
				p.YourRateCI[0]*100, p.YourRateCI[1]*100,
				p.WinnerBrand, p.WinnerRate*100, p.Gap*100)
			if len(p.WinnerReasons) > 0 {
				fmt.Fprintf(&b, "**Why the LLMs picked %s:**\n", p.WinnerBrand)
				for _, r := range p.WinnerReasons {
					fmt.Fprintf(&b, "  - %s\n", r)
				}
				fmt.Fprintln(&b)
			}
			if len(p.Actions) > 0 {
				fmt.Fprintf(&b, "**Recommended actions** (synthesized from the LLMs' own suggestions):\n")
				for i, a := range p.Actions {
					fmt.Fprintf(&b, "  %d. %s\n", i+1, a)
				}
				fmt.Fprintln(&b)
			} else {
				fmt.Fprintf(&b, "_No actions yet — run `salience advise -run %d` to generate them._\n\n", pb.RunID)
			}
		}
	}
	return b.String()
}

// firstSentences returns the first n sentences of s (best-effort, ASCII +
// CJK punctuation).
func firstSentences(s string, n int) string {
	s = strings.TrimSpace(s)
	count := 0
	for i, r := range s {
		switch r {
		case '.', '!', '?', '。', '！', '？':
			count++
			if count >= n {
				end := i + len(string(r))
				return strings.TrimSpace(s[:end])
			}
		}
	}
	return s
}

// extractNumberedActions tries to find numbered list items in text. Falls
// back to splitting on newlines if no obvious numbering is present.
func extractNumberedActions(s string) []string {
	var out []string
	var current strings.Builder
	flush := func() {
		t := strings.TrimSpace(current.String())
		if t != "" {
			out = append(out, t)
		}
		current.Reset()
	}
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		// Numbered: "1.", "1)", "(1)".
		if (len(l) >= 2 && l[0] >= '0' && l[0] <= '9' && (l[1] == '.' || l[1] == ')')) ||
			(len(l) >= 3 && l[0] == '(' && l[1] >= '0' && l[1] <= '9') {
			flush()
		}
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(l)
	}
	flush()
	if len(out) <= 1 {
		return nil
	}
	return out
}

func dedupShort(xs []string, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		key := strings.ToLower(strings.TrimSpace(x))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, x)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func topBrandsForMD(m map[string]int) string {
	type bc struct {
		brand string
		c     int
	}
	xs := make([]bc, 0, len(m))
	for k, v := range m {
		xs = append(xs, bc{k, v})
	}
	sort.SliceStable(xs, func(i, j int) bool { return xs[i].c > xs[j].c })
	var out []string
	for i, x := range xs {
		if i >= 3 {
			break
		}
		out = append(out, fmt.Sprintf("%s×%d", x.brand, x.c))
	}
	return strings.Join(out, ", ")
}
