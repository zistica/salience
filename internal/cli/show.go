package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/salience-cli/salience/internal/store"
)

// RunShow prints a detailed inspection of a single run: the samples
// table and the full list of detected mentions.
func RunShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	runID := fs.Int64("run", 0, "run id to show (0 = latest)")
	mentionsOnly := fs.Bool("mentions", false, "only print the mentions list, not the samples summary")
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
		id, err = st.LatestRunID(ctx)
		if err != nil {
			return err
		}
		if id == 0 {
			return fmt.Errorf("no runs persisted")
		}
	}

	meta, err := st.GetRun(ctx, id)
	if err != nil {
		return fmt.Errorf("load run #%d: %w", id, err)
	}

	fmt.Printf("Run #%d — %s — status %s\n", meta.ID, meta.BrandName, meta.Status)
	fmt.Printf("Started:  %s\n", meta.StartedAt.Local().Format("2006-01-02 15:04:05"))
	if meta.FinishedAt != nil {
		fmt.Printf("Finished: %s (duration %s)\n",
			meta.FinishedAt.Local().Format("2006-01-02 15:04:05"),
			meta.FinishedAt.Sub(meta.StartedAt).Round(1).String())
	}
	fmt.Println()

	if !*mentionsOnly {
		samples, err := st.ListSamples(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("Samples (%d total):\n", len(samples))
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  PROVIDER\tMODEL\t#\tCOST\tBRANDS HIT\tPROMPT")
		for _, s := range samples {
			brands := "—"
			if len(s.BrandsHit) > 0 {
				sort.Strings(s.BrandsHit)
				brands = strings.Join(s.BrandsHit, ", ")
			}
			if s.Error != "" {
				brands = "error: " + s.Error
			}
			fmt.Fprintf(tw, "  %s\t%s\t%d\t$%.4f\t%s\t%s\n",
				s.ProviderName, s.Model, s.SampleIdx, s.CostUSD, brands, clip(s.Prompt, 50))
		}
		_ = tw.Flush()
		fmt.Println()
	}

	mentions, err := st.ListMentionsForRun(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("Mentions (%d total):\n", len(mentions))
	if len(mentions) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  BRAND\tWHERE\tSENT.\tPROVIDER\t#\tCONTEXT")
	for _, m := range mentions {
		ctx := clip(m.Context, 60)
		if ctx == "" {
			ctx = "—"
		}
		sent := m.Sentiment
		switch sent {
		case "positive":
			sent = "+ pos"
		case "negative":
			sent = "− neg"
		default:
			sent = "  neu"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%d\t%s\n",
			m.Brand, m.Where, sent, m.ProviderName, m.SampleIdx, ctx)
	}
	return tw.Flush()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
