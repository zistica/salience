package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/salience-cli/salience/internal/store"
)

// RunRuns prints a table of recorded runs, newest first.
func RunRuns(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	limit := fs.Int("limit", 20, "max runs to show (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		fmt.Println("no database yet — run `salience bench` first")
		return nil
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	runs, err := st.ListRuns(ctx, *limit)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("no runs persisted")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTARTED\tDURATION\tBRAND\tSTATUS\tOK\tERR")
	for _, r := range runs {
		ok, errored, _ := st.CountSamples(ctx, r.ID)
		dur := "—"
		if r.FinishedAt != nil {
			dur = r.FinishedAt.Sub(r.StartedAt).Round(time.Second).String()
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d\t%d\n",
			r.ID,
			r.StartedAt.Local().Format("2006-01-02 15:04"),
			dur,
			r.BrandName,
			r.Status,
			ok,
			errored,
		)
	}
	return tw.Flush()
}
