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
	"github.com/salience-cli/salience/internal/sources"
	"github.com/salience-cli/salience/internal/store"
)

// RunSources prints the URL and domain leaderboard for a run.
func RunSources(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sources", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	runID := fs.Int64("run", 0, "run id (0 = latest)")
	limit := fs.Int("limit", 20, "max rows to show per section (0 = all)")
	gapOnly := fs.Bool("gaps", false, "only print competitor-only domains that don't co-occur with your brand")
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
		return fmt.Errorf("decode embedded config: %w", err)
	}
	samples, err := st.ListSamples(ctx, id)
	if err != nil {
		return err
	}
	srcJSON, err := st.SourcesJSONByRun(ctx, id)
	if err != nil {
		return err
	}
	decoded, _ := sources.DecodeSamples(samples, srcJSON)

	vendorAliases := map[string][]string{cfg.Brand.Name: cfg.Brand.Aliases}
	competitors := make([]string, 0, len(cfg.Competitors))
	for _, c := range cfg.Competitors {
		competitors = append(competitors, c.Name)
		vendorAliases[c.Name] = c.Aliases
	}

	rep := sources.AnalyzeSamples(decoded, cfg.Brand.Name, competitors, vendorAliases)
	rep.RunID = id

	fmt.Printf("Sources for run #%d — brand %q\n\n", id, cfg.Brand.Name)
	if *gapOnly {
		printGap(rep, *limit)
		printListicleGap(ctx, st, rep, cfg.Brand.Name, competitors, *limit)
		return nil
	}
	printDomains(rep, *limit)
	fmt.Println()
	printPerBrand(rep, competitors, *limit)
	fmt.Println()
	printGap(rep, *limit)
	fmt.Println()
	printListicleGap(ctx, st, rep, cfg.Brand.Name, competitors, *limit)
	fmt.Println()
	printTopURLs(rep, *limit)
	return nil
}

// printListicleGap surfaces individual pages (not just domains) that mention
// at least one competitor but never the user's brand — the most actionable
// "ask to be added here" list.
func printListicleGap(ctx context.Context, st *store.Store, rep sources.Report, brand string, competitors []string, limit int) {
	var inputs []sources.ScrapedPageInput
	for _, u := range rep.URLs {
		page, err := st.GetScrapedPage(ctx, u.URL)
		if err != nil || page == nil {
			continue
		}
		inputs = append(inputs, sources.ScrapedPageInput{
			URL: page.URL, Title: page.Title,
			Description: page.Description, Body: page.Body,
		})
	}
	if len(inputs) == 0 {
		fmt.Println("Page-level gap: (no scraped pages — run `salience scrape` first)")
		return
	}
	rows := sources.PageGapAnalysis(inputs, brand, competitors)
	fmt.Printf("Page-level gap — listicles that mention competitors but not %q:\n", brand)
	if len(rows) == 0 {
		fmt.Println("  (none — either you appear on every listicle, or no listicles were cited)")
		return
	}
	for i, r := range rows {
		if limit > 0 && i >= limit {
			break
		}
		fmt.Printf("  · %s\n    %s\n    competitors here: %s\n",
			clip(r.Title, 70), r.URL, strings.Join(r.MentionsCompetitors, ", "))
	}
}

func printDomains(r sources.Report, limit int) {
	fmt.Printf("Top cited domains:\n")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  RANK\tDOMAIN\tCATEGORY\tCITES\tBRANDS THAT CO-OCCURRED")
	for i, d := range capDomains(r.Domains, limit) {
		brands := topBrands(d.Brands, 3)
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%d\t%s\n", i+1, d.Domain, d.Category, d.Count, brands)
	}
	_ = tw.Flush()
}

func printPerBrand(r sources.Report, competitors []string, limit int) {
	fmt.Printf("Per-brand top domains:\n")
	all := append([]string{r.Brand}, competitors...)
	for _, br := range all {
		list := r.PerBrand[br]
		if len(list) == 0 {
			fmt.Printf("  %-20s (none cited)\n", br)
			continue
		}
		var preview []string
		for i, d := range list {
			if limit > 0 && i >= limit {
				break
			}
			preview = append(preview, fmt.Sprintf("%s×%d", d.Domain, d.Count))
		}
		fmt.Printf("  %-20s %s\n", br, joinComma(preview, 6))
	}
}

func printGap(r sources.Report, limit int) {
	fmt.Printf("Domains driving competitor citations but NOT co-occurring with %q:\n", r.Brand)
	if len(r.MissingFromBrand) == 0 {
		fmt.Println("  (none — you co-occur with every cited domain)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  DOMAIN\tCATEGORY\tDRIVES")
	for i, d := range r.MissingFromBrand {
		if limit > 0 && i >= limit {
			break
		}
		driver := topBrands(d.Brands, 3)
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", d.Domain, d.Category, driver)
	}
	_ = tw.Flush()
}

func printTopURLs(r sources.Report, limit int) {
	fmt.Printf("Top cited URLs:\n")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  CITES\tURL\tTITLE")
	for i, u := range r.URLs {
		if limit > 0 && i >= limit {
			break
		}
		fmt.Fprintf(tw, "  %d\t%s\t%s\n", u.Count, clipURL(u.URL, 60), clip(u.Title, 40))
	}
	_ = tw.Flush()
}

func capDomains(ds []sources.DomainRank, n int) []sources.DomainRank {
	if n <= 0 || len(ds) <= n {
		return ds
	}
	return ds[:n]
}

func topBrands(m map[string]int, n int) string {
	type bc struct {
		brand string
		c     int
	}
	xs := make([]bc, 0, len(m))
	for k, v := range m {
		xs = append(xs, bc{k, v})
	}
	// Sort by count desc.
	for i := range xs {
		for j := i + 1; j < len(xs); j++ {
			if xs[j].c > xs[i].c {
				xs[i], xs[j] = xs[j], xs[i]
			}
		}
	}
	var out []string
	for i, x := range xs {
		if i >= n {
			break
		}
		out = append(out, fmt.Sprintf("%s×%d", x.brand, x.c))
	}
	return joinComma(out, n)
}

func joinComma(xs []string, max int) string {
	if len(xs) > max {
		xs = xs[:max]
	}
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

func clipURL(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
