package cli

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/store"
)

// RunDiff compares two runs by computing their reports and showing the cells
// whose user-brand rate or competitive gap shifted the most. Cells whose
// 95% CIs do not overlap are flagged as statistically significant.
func RunDiff(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	from := fs.Int64("from", 0, "baseline run id")
	to := fs.Int64("to", 0, "comparison run id (default: latest)")
	limit := fs.Int("limit", 15, "show at most N rows of biggest movers (0 = all)")
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

	if *from == 0 {
		return fmt.Errorf("-from is required")
	}
	if *to == 0 {
		latest, err := st.LatestRunID(ctx)
		if err != nil {
			return err
		}
		if latest == 0 || latest == *from {
			return fmt.Errorf("nothing newer than -from to compare against")
		}
		*to = latest
	}

	dataA, err := loadReport(ctx, st, *from)
	if err != nil {
		return fmt.Errorf("load run #%d: %w", *from, err)
	}
	dataB, err := loadReport(ctx, st, *to)
	if err != nil {
		return fmt.Errorf("load run #%d: %w", *to, err)
	}

	if dataA.UserBrand != dataB.UserBrand {
		fmt.Fprintf(os.Stderr,
			"! brand differs between runs (%q vs %q); diff will be limited to overlapping prompts.\n",
			dataA.UserBrand, dataB.UserBrand)
	}

	rows := buildDiffRows(dataA, dataB)
	if len(rows) == 0 {
		fmt.Println("no overlapping (prompt, provider) cells between the two runs")
		return nil
	}
	// Sort by |delta| descending so the biggest movers come first.
	sort.SliceStable(rows, func(i, j int) bool {
		return math.Abs(rows[i].UserDelta) > math.Abs(rows[j].UserDelta)
	})
	if *limit > 0 && len(rows) > *limit {
		rows = rows[:*limit]
	}

	fmt.Printf("Diff: run #%d (%s) → run #%d (%s)\n",
		dataA.RunID, dataA.UserBrand, dataB.RunID, dataB.UserBrand)
	fmt.Printf("Brand: %s | %d common cells, showing %d biggest movers\n\n",
		dataB.UserBrand, countCommon(dataA, dataB), len(rows))

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tPROMPT\tOLD\tNEW\tΔ\tSIG?\tGAP Δ")
	for _, r := range rows {
		sig := " "
		if r.Significant {
			sig = "✓"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ProviderName,
			clip(r.Prompt, 50),
			pct(r.UserOld), pct(r.UserNew),
			signedPct(r.UserDelta),
			sig,
			signedPct(r.GapDelta),
		)
	}
	_ = tw.Flush()
	fmt.Println()
	fmt.Println("Δ      = NEW − OLD for the user's mention rate.")
	fmt.Println("SIG?   = ✓ when the 95% CIs of the two rates do not overlap.")
	fmt.Println("GAP Δ  = change in (you − best competitor); positive = closing the gap.")

	// Overlay any actions logged between the two runs — correlational only.
	if err := overlayActions(ctx, st, dataA, dataB); err != nil {
		fmt.Fprintf(os.Stderr, "(could not load actions: %v)\n", err)
	}
	return nil
}

// overlayActions prints actions taken between the two runs' wall-clock times,
// so the user can see what their team did in the window.
func overlayActions(ctx context.Context, st *store.Store, a, b report.Data) error {
	// Resolve project_id by looking at the runs table directly.
	runA, err := st.GetRun(ctx, a.RunID)
	if err != nil || runA.FinishedAt == nil {
		return nil
	}
	runB, err := st.GetRun(ctx, b.RunID)
	if err != nil || runB.FinishedAt == nil {
		return nil
	}
	// Both runs are tied to a project (via project_id on the runs table).
	// We don't have a getter for that, so the actions list lookup uses
	// project = latest. This is correct in the single-project case and a
	// reasonable approximation in multi-project setups where you'd usually
	// diff inside the same project anyway.
	projID, _ := st.LatestProjectID(ctx)
	if projID == 0 {
		return nil
	}
	start := runA.StartedAt.UTC().Format(time.RFC3339Nano)
	end := runB.StartedAt.UTC().Format(time.RFC3339Nano)
	actions, err := st.ActionsBetween(ctx, projID, start, end)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		fmt.Println("\nActions between these runs: none logged.")
		return nil
	}
	fmt.Printf("\nActions logged between run #%d and run #%d (%d):\n", a.RunID, b.RunID, len(actions))
	for _, act := range actions {
		prompts := ""
		if len(act.AppliesToPrompts) > 0 {
			prompts = " — prompts: " + strings.Join(act.AppliesToPrompts, ", ")
		}
		fmt.Printf("  · %s  %s%s\n", act.TakenAt.Format("2006-01-02"), act.Description, prompts)
	}
	return nil
}

type diffRow struct {
	ProviderName        string
	Prompt              string
	UserOld, UserNew    float64
	UserDelta           float64
	GapDelta            float64
	Significant         bool
}

func buildDiffRows(a, b report.Data) []diffRow {
	idx := map[string]report.Cell{}
	for _, c := range a.Cells {
		idx[c.ProviderName+"|"+c.Prompt] = c
	}
	var out []diffRow
	for _, cb := range b.Cells {
		ca, ok := idx[cb.ProviderName+"|"+cb.Prompt]
		if !ok {
			continue
		}
		userA := ca.Rates[a.UserBrand]
		userB := cb.Rates[b.UserBrand]
		sig := !report.IntervalsOverlap(
			ca.CILow[a.UserBrand], ca.CIHigh[a.UserBrand],
			cb.CILow[b.UserBrand], cb.CIHigh[b.UserBrand],
		)
		out = append(out, diffRow{
			ProviderName: cb.ProviderName,
			Prompt:       cb.Prompt,
			UserOld:      userA,
			UserNew:      userB,
			UserDelta:    userB - userA,
			GapDelta:     ca.Gap - cb.Gap, // closing the gap shows as positive
			Significant:  sig,
		})
	}
	return out
}

func countCommon(a, b report.Data) int {
	idx := map[string]bool{}
	for _, c := range a.Cells {
		idx[c.ProviderName+"|"+c.Prompt] = true
	}
	n := 0
	for _, c := range b.Cells {
		if idx[c.ProviderName+"|"+c.Prompt] {
			n++
		}
	}
	return n
}

func loadReport(ctx context.Context, st *store.Store, runID int64) (report.Data, error) {
	meta, err := st.GetRun(ctx, runID)
	if err != nil {
		return report.Data{}, err
	}
	user, comps, err := report.LoadCompetitorsFromConfigJSON(ctx, meta.ConfigJSON)
	if err != nil {
		return report.Data{}, err
	}
	samples, err := st.ListSamples(ctx, runID)
	if err != nil {
		return report.Data{}, err
	}
	return report.Build(runID, meta, samples, user, comps), nil
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }
func signedPct(f float64) string {
	switch {
	case f > 0:
		return "+" + pct(f)
	case f < 0:
		return "−" + pct(-f)
	}
	return "0%"
}
