// CLI subcommands for the v0.2 layer: schedule, watch, simulate.
// Cross-model advise enhancement also lives here (it's a tiny tweak).
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/salience-cli/salience/internal/cron"
	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/envfile"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/scraper"
	"github.com/salience-cli/salience/internal/store"
)

// ---------- schedule ----------

// RunSchedule dispatches the salience schedule subcommands.
func RunSchedule(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return scheduleUsage()
	}
	switch args[0] {
	case "add":
		return scheduleAdd(ctx, args[1:])
	case "list", "ls":
		return scheduleList(ctx, args[1:])
	case "rm", "remove", "delete":
		return scheduleRemove(ctx, args[1:])
	case "-h", "--help":
		return scheduleUsage()
	}
	return fmt.Errorf("unknown schedule subcommand %q", args[0])
}

func scheduleUsage() error {
	fmt.Print(`salience schedule — recurring benchmarks (server-side ticker)

Usage:
  salience schedule add  -cron "0 9 * * MON" [-project NAME]
  salience schedule list [-project NAME]
  salience schedule rm   ID

Cron syntax: standard 5-field, or aliases @hourly / @daily / @weekly /
@every <duration>. Schedules only fire while 'salience serve' is running.
`)
	return nil
}

func scheduleAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("schedule add", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project (default: latest)")
	cronExpr := fs.String("cron", "", "cron expression (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*cronExpr) == "" {
		return fmt.Errorf("-cron is required")
	}
	sched, err := cron.Parse(*cronExpr)
	if err != nil {
		return fmt.Errorf("invalid cron: %w", err)
	}
	next := sched.Next(time.Now().UTC())

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	proj, err := pickProject(ctx, st, *projectKey)
	if err != nil {
		return err
	}
	id, err := st.InsertSchedule(ctx, store.Schedule{
		ProjectID: proj.ID, CronExpr: *cronExpr, NextFires: next, Enabled: true,
	})
	if err != nil {
		return err
	}
	fmt.Printf("scheduled #%d for %q — next fires %s\n", id, proj.Name, next.Local().Format(time.RFC3339))
	return nil
}

func scheduleList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("schedule list", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project (empty = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	var projID int64
	if *projectKey != "" {
		p, err := st.GetProjectBySlugOrName(ctx, *projectKey)
		if err != nil {
			return err
		}
		projID = p.ID
	}
	scheds, err := st.ListSchedules(ctx, projID)
	if err != nil {
		return err
	}
	if len(scheds) == 0 {
		fmt.Println("no schedules yet")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROJECT\tCRON\tENABLED\tNEXT FIRES\tLAST FIRED")
	for _, s := range scheds {
		last := "—"
		if s.LastFired != nil {
			last = s.LastFired.Local().Format("2006-01-02 15:04")
		}
		en := "yes"
		if !s.Enabled {
			en = "no"
		}
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\n",
			s.ID, s.ProjectID, s.CronExpr, en,
			s.NextFires.Local().Format("2006-01-02 15:04"), last)
	}
	return tw.Flush()
}

func scheduleRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("schedule rm", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: salience schedule rm ID")
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid id %q", rest[0])
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteSchedule(ctx, id); err != nil {
		return err
	}
	fmt.Printf("deleted schedule #%d\n", id)
	return nil
}

// ---------- watch ----------

// RunWatch dispatches the salience watch subcommands.
func RunWatch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return watchUsage()
	}
	switch args[0] {
	case "add":
		return watchAdd(ctx, args[1:])
	case "list", "ls":
		return watchList(ctx, args[1:])
	case "rm", "remove":
		return watchRemove(ctx, args[1:])
	case "fetch":
		return watchFetch(ctx, args[1:])
	case "-h", "--help":
		return watchUsage()
	}
	return fmt.Errorf("unknown watch subcommand %q", args[0])
}

func watchUsage() error {
	fmt.Print(`salience watch — track external URLs for content changes

Usage:
  salience watch add   -url URL [-label LABEL] [-interval 24h] [-project NAME]
  salience watch list  [-project NAME]
  salience watch rm    ID
  salience watch fetch [ID]   (fetch one or all watchers now, ignoring interval)

Each fetch hashes the page body and records a snapshot. The dashboard
diffs snapshots over time to show when content changes.
`)
	return nil
}

func watchAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch add", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project (default: latest)")
	url := fs.String("url", "", "URL to watch (required)")
	label := fs.String("label", "", "human label")
	interval := fs.Duration("interval", 24*time.Hour, "min duration between fetches")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*url) == "" {
		return fmt.Errorf("-url is required")
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
	id, err := st.InsertWatcher(ctx, store.Watcher{
		ProjectID: proj.ID, URL: *url, Label: *label,
		IntervalSeconds: int(interval.Seconds()), Enabled: true,
	})
	if err != nil {
		return err
	}
	fmt.Printf("added watcher #%d for %s\n", id, *url)
	return nil
}

func watchList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch list", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project (empty = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	var projID int64
	if *projectKey != "" {
		p, err := st.GetProjectBySlugOrName(ctx, *projectKey)
		if err != nil {
			return err
		}
		projID = p.ID
	}
	ws, err := st.ListWatchers(ctx, projID)
	if err != nil {
		return err
	}
	if len(ws) == 0 {
		fmt.Println("no watchers yet")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROJECT\tLABEL\tURL\tINTERVAL\tLAST FETCHED")
	for _, w := range ws {
		last := "—"
		if w.LastFetchedAt != nil {
			last = w.LastFetchedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\n",
			w.ID, w.ProjectID, clip(w.Label, 30), clip(w.URL, 60),
			(time.Duration(w.IntervalSeconds) * time.Second).String(), last)
	}
	return tw.Flush()
}

func watchRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch rm", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: salience watch rm ID")
	}
	id, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteWatcher(ctx, id); err != nil {
		return err
	}
	fmt.Printf("removed watcher #%d\n", id)
	return nil
}

func watchFetch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch fetch", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	rest := fs.Args()
	var ws []store.Watcher
	if len(rest) == 1 {
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			return err
		}
		all, _ := st.ListWatchers(ctx, 0)
		for _, w := range all {
			if w.ID == id {
				ws = append(ws, w)
				break
			}
		}
	} else {
		ws, err = st.ListWatchers(ctx, 0)
		if err != nil {
			return err
		}
	}
	if len(ws) == 0 {
		fmt.Println("no watchers to fetch")
		return nil
	}
	c := scraper.NewClient()
	for _, w := range ws {
		p := c.Fetch(ctx, w.URL)
		hash := sha256hex(p.Title + "\n" + p.Body)

		// Detect brand + competitor presence in the fetched body.
		proj, _ := st.GetProject(ctx, w.ProjectID)
		brandPresent, compCount := false, 0
		if proj != nil {
			brand, comps := loadBrands(proj)
			hay := strings.ToLower(p.Title + " " + p.Body)
			if strings.Contains(hay, strings.ToLower(brand)) {
				brandPresent = true
			}
			for _, c := range comps {
				if strings.Contains(hay, strings.ToLower(c)) {
					compCount++
				}
			}
		}

		_, _ = st.InsertWatcherSnapshot(ctx, store.WatcherSnapshot{
			WatcherID:          w.ID,
			Title:              p.Title,
			Body:               clip(p.Body, 4000),
			ContentHash:        hash,
			BrandPresent:       brandPresent,
			CompetitorsPresent: compCount,
		})
		_ = st.UpdateWatcherFetched(ctx, w.ID, hash)

		changed := ""
		if w.LastHash != "" && w.LastHash != hash {
			changed = "  ← CHANGED"
		}
		fmt.Printf("#%d %s (brand: %v, competitors: %d)%s\n", w.ID, clip(w.URL, 60), brandPresent, compCount, changed)
	}
	return nil
}

// ---------- simulate ----------

// RunSimulate runs a single losing prompt with a candidate piece of content
// prepended as context, and compares the result to the baseline rate.
func RunSimulate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("simulate", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	projectKey := fs.String("project", "", "project name or id")
	promptText := fs.String("prompt", "", "the prompt to simulate (required)")
	contentPath := fs.String("content", "", "Markdown file with the draft (required)")
	samples := fs.Int("samples", 5, "samples per provider per condition")
	yes := fs.Bool("yes", false, "skip cost confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *promptText == "" || *contentPath == "" {
		return fmt.Errorf("usage: salience simulate -prompt \"...\" -content draft.md")
	}
	draftBytes, err := os.ReadFile(*contentPath)
	if err != nil {
		return err
	}
	draft := string(draftBytes)
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

	// Cost estimate.
	tokIn := (len(draft) + len(*promptText)) / 4
	calls := len(providers) * *samples * 2 // baseline + simulated
	estCost := 0.0
	for _, p := range providers {
		estCost += pricing.Estimate(p.Model(), tokIn, 350) * float64(*samples*2)
	}
	fmt.Printf("Simulate plan: %d call(s) total (baseline + with-content × %d providers × %d samples). Est. $%.4f.\n",
		calls, len(providers), *samples, estCost)
	if !*yes {
		fmt.Print("Proceed? [y/N] ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Println("aborted")
			return nil
		}
	}

	brand, _ := loadBrands(proj)
	primedPrompt := fmt.Sprintf(
		"Background context (assume this content has been published and is well-indexed on the open web):\n\n---\n%s\n---\n\nQuestion: %s",
		draft, *promptText)

	baselineHit, primedHit := 0, 0
	for _, p := range providers {
		for i := 0; i < *samples; i++ {
			if base, err := p.Call(ctx, *promptText, cfg.MaxTokens, nil); err == nil {
				if containsBrand(base.Text, brand) {
					baselineHit++
				}
			}
			if alt, err := p.Call(ctx, primedPrompt, cfg.MaxTokens, nil); err == nil {
				if containsBrand(alt.Text, brand) {
					primedHit++
				}
			}
		}
	}
	denom := float64(len(providers) * *samples)
	baseRate := float64(baselineHit) / denom
	primedRate := float64(primedHit) / denom
	delta := primedRate - baseRate
	fmt.Printf("\nBaseline rate:  %.0f%% (%d/%d)\n", baseRate*100, baselineHit, int(denom))
	fmt.Printf("With content:   %.0f%% (%d/%d)\n", primedRate*100, primedHit, int(denom))
	fmt.Printf("Δ:              %+.0f%%\n", delta*100)
	if delta > 0.05 {
		fmt.Println("→ Looks promising. Worth publishing.")
	} else if delta < -0.05 {
		fmt.Println("→ This draft seems counterproductive — revise before publishing.")
	} else {
		fmt.Println("→ No clear movement. Either the draft isn't strong, or the LLM doesn't lean on context for this prompt.")
	}

	_, _ = st.InsertSimulation(ctx, store.Simulation{
		ProjectID: proj.ID, Prompt: *promptText, ContentDraft: draft,
		BaselineRate: baseRate, SimulatedRate: primedRate, Delta: delta,
		NSamples: int(denom),
	})
	return nil
}

func containsBrand(answer, brand string) bool {
	if brand == "" {
		return false
	}
	return strings.Contains(strings.ToLower(answer), strings.ToLower(brand))
}

// loadBrands extracts the brand name + competitor names from a stored project.
func loadBrands(p *store.Project) (string, []string) {
	if p == nil {
		return "", nil
	}
	cfg, err := projectConfig(p)
	if err != nil {
		return "", nil
	}
	out := make([]string, 0, len(cfg.Competitors))
	for _, c := range cfg.Competitors {
		out = append(out, c.Name)
	}
	return cfg.Brand.Name, out
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// dummy use of detect to keep the import alive across refactors
var _ = detect.Source{}
