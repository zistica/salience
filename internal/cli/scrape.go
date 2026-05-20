package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/scraper"
	"github.com/salience-cli/salience/internal/store"
)

// RunScrape fetches every URL cited by a run's samples, stores the page
// content (title + description + body text) in scraped_pages so later
// commands can read it without re-fetching.
func RunScrape(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scrape", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	runID := fs.Int64("run", 0, "run id (0 = latest)")
	parallel := fs.Int("parallel", 4, "concurrent fetches")
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

	srcJSON, err := st.SourcesJSONByRun(ctx, id)
	if err != nil {
		return err
	}
	// Collect every unique URL across all samples in the run.
	seen := map[string]bool{}
	var urls []string
	for _, raw := range srcJSON {
		if raw == "" || raw == "null" {
			continue
		}
		var sources []detect.Source
		if err := json.Unmarshal([]byte(raw), &sources); err != nil {
			continue
		}
		for _, s := range sources {
			if s.URL == "" || seen[s.URL] {
				continue
			}
			seen[s.URL] = true
			urls = append(urls, s.URL)
		}
	}
	if len(urls) == 0 {
		fmt.Println("no URLs cited in this run — nothing to scrape.")
		return nil
	}
	fmt.Printf("Fetching %d unique URL(s) with %d parallel workers…\n", len(urls), *parallel)

	c := scraper.NewClient()
	pages := c.FetchAll(ctx, urls, *parallel)
	for _, p := range pages {
		if p == nil {
			continue
		}
		_, _ = st.UpsertScrapedPage(ctx, store.ScrapedPage{
			URL:         p.URL,
			Title:       p.Title,
			Description: p.Description,
			Body:        p.Body,
			StatusCode:  p.StatusCode,
			Err:         p.Err,
		})
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tURL\tTITLE")
	for _, p := range pages {
		if p == nil {
			continue
		}
		st := fmt.Sprintf("%d", p.StatusCode)
		if p.Err != "" {
			st = "ERR"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", st, clip(p.URL, 60), clip(p.Title, 60))
	}
	_ = tw.Flush()
	return nil
}
