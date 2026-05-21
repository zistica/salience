// Package report reads a completed (or in-progress) run from the store and
// renders a human-readable summary in Markdown or HTML.
package report

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"sort"
	"strings"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/store"
)

// Format is the output format. Markdown is the default.
type Format string

const (
	Markdown Format = "markdown"
	HTML     Format = "html"
	JSON     Format = "json"
	CSV      Format = "csv"
)

// Data is the in-memory aggregate that both renderers consume.
type Data struct {
	RunID         int64
	Started       string
	Finished      string
	Status        string
	UserBrand     string
	// Competitors is the flat list of every competitor name, kept for
	// backward compatibility with renderers that don't filter by region.
	Competitors []string
	// CompetitorBrands carries the full Brand structs (with Regions). New
	// renderers use this to filter competitor columns per region.
	CompetitorBrands []config.Brand
	Cells            []Cell // one per (prompt, provider, region) combination
	Totals           BrandTotals
	TotalSamples     int
	TotalFailures    int
}

// CompetitorsForRegion returns the names of competitors that apply to the
// given region code. Empty Regions on a Brand means "applies everywhere".
func (d Data) CompetitorsForRegion(code string) []string {
	var out []string
	for _, b := range d.CompetitorBrands {
		if b.AppliesTo(code) {
			out = append(out, b.Name)
		}
	}
	// Fallback: if CompetitorBrands wasn't populated (older callers),
	// return the flat list.
	if len(d.CompetitorBrands) == 0 {
		return d.Competitors
	}
	return out
}

// Cell is the report's atom: one (prompt, provider, model, region)
// combination with computed mention rates for every brand. Region is the
// short code from the project's configured regions list ("global" for
// runs that don't use regions).
type Cell struct {
	Prompt       string
	ProviderName string
	Model        string
	Region       string
	Samples      int
	Failures     int
	// Rates is keyed by canonical brand name. Each value is in [0, 1].
	Rates map[string]float64
	// CILow / CIHigh are the Wilson 95% CI bounds per brand, keyed by name.
	CILow  map[string]float64
	CIHigh map[string]float64
	// SentimentByBrand: positive minus negative mention counts per brand, so a
	// value >0 means net-positive recommendation, <0 means net-negative.
	SentimentByBrand map[string]int
	// Gap is the user brand rate minus the best competitor rate.
	Gap float64
	// SampleIDs is the list of underlying sample DB ids that fed this
	// cell. Used by the dashboard's "Open anatomy" button — clicking a
	// row loads /api/runs/:id/anatomy/:sampleId for the first one.
	SampleIDs []int64
}

// BrandTotals is the overall mention rate per brand across the whole run.
type BrandTotals struct {
	Samples int
	// Rates keyed by canonical brand name.
	Rates map[string]float64
}

// Build crunches the raw samples into the report data structure.
// userBrand is the canonical name of the user's brand; competitors is the
// ordered list of competitor canonical names.
//
// Build also accepts an optional full competitor brand list via
// BuildWithBrands when callers want per-region competitor filtering in
// the report. The flat-name signature here preserves backward compat for
// older callers; it's equivalent to passing brands where every brand has
// Regions == nil ("applies everywhere").
func Build(runID int64, runMeta *store.Run, samples []store.SampleRow, userBrand string, competitors []string) Data {
	brands := make([]config.Brand, 0, len(competitors))
	for _, c := range competitors {
		brands = append(brands, config.Brand{Name: c})
	}
	return BuildWithBrands(runID, runMeta, samples, userBrand, brands)
}

// BuildWithBrands is the region-aware variant. competitorBrands carries
// each competitor's Regions list so the renderer can filter columns per
// region — e.g. Tsubaki only shows in Japan tables, Mamaearth only in
// India tables, Dove (no regions = global) shows everywhere.
func BuildWithBrands(runID int64, runMeta *store.Run, samples []store.SampleRow, userBrand string, competitorBrands []config.Brand) Data {
	competitors := make([]string, 0, len(competitorBrands))
	for _, b := range competitorBrands {
		competitors = append(competitors, b.Name)
	}
	d := Data{
		RunID:            runID,
		UserBrand:        userBrand,
		Competitors:      competitors,
		CompetitorBrands: competitorBrands,
		Status:           runMeta.Status,
		Started:          runMeta.StartedAt.Format("2006-01-02 15:04:05 MST"),
	}
	if runMeta.FinishedAt != nil {
		d.Finished = runMeta.FinishedAt.Format("2006-01-02 15:04:05 MST")
	}

	type key struct {
		prompt   string
		provider string
		model    string
		region   string
	}
	type bucket struct {
		samples  int
		failures int
		hits     map[string]int
		ids      []int64
	}
	buckets := map[key]*bucket{}
	totalHits := map[string]int{}
	totalSamples := 0
	totalFailures := 0

	allBrands := append([]string{userBrand}, competitors...)

	for _, s := range samples {
		region := s.Region
		if region == "" {
			region = "global"
		}
		k := key{s.Prompt, s.ProviderName, s.Model, region}
		b := buckets[k]
		if b == nil {
			b = &bucket{hits: map[string]int{}}
			buckets[k] = b
		}
		if s.Error != "" {
			b.failures++
			totalFailures++
			continue
		}
		b.samples++
		b.ids = append(b.ids, s.ID)
		totalSamples++
		hit := map[string]bool{}
		for _, br := range s.BrandsHit {
			hit[br] = true
		}
		for _, br := range allBrands {
			if hit[br] {
				b.hits[br]++
				totalHits[br]++
			}
		}
	}

	for k, b := range buckets {
		c := Cell{
			Prompt:           k.prompt,
			ProviderName:     k.provider,
			Model:            k.model,
			Region:           k.region,
			Samples:          b.samples,
			Failures:         b.failures,
			Rates:            map[string]float64{},
			CILow:            map[string]float64{},
			CIHigh:           map[string]float64{},
			SentimentByBrand: map[string]int{},
			SampleIDs:        append([]int64(nil), b.ids...),
		}
		for _, br := range allBrands {
			c.Rates[br] = rate(b.hits[br], b.samples)
			lo, hi := WilsonInterval(b.hits[br], b.samples)
			c.CILow[br] = lo
			c.CIHigh[br] = hi
		}
		c.Gap = computeGap(c.Rates[userBrand], competitors, c.Rates)
		d.Cells = append(d.Cells, c)
	}

	sort.SliceStable(d.Cells, func(i, j int) bool {
		if d.Cells[i].Gap != d.Cells[j].Gap {
			return d.Cells[i].Gap < d.Cells[j].Gap // worst gaps first
		}
		if d.Cells[i].Prompt != d.Cells[j].Prompt {
			return d.Cells[i].Prompt < d.Cells[j].Prompt
		}
		return d.Cells[i].ProviderName < d.Cells[j].ProviderName
	})

	d.Totals = BrandTotals{Samples: totalSamples, Rates: map[string]float64{}}
	for _, br := range allBrands {
		d.Totals.Rates[br] = rate(totalHits[br], totalSamples)
	}
	d.TotalSamples = totalSamples
	d.TotalFailures = totalFailures
	return d
}

// rate returns hits/samples in [0,1], or 0 when samples==0.
func rate(hits, samples int) float64 {
	if samples <= 0 {
		return 0
	}
	return float64(hits) / float64(samples)
}

// computeGap returns the user brand rate minus the highest competitor rate.
// A negative value means the user is losing.
func computeGap(userRate float64, competitors []string, rates map[string]float64) float64 {
	best := 0.0
	for _, c := range competitors {
		if r := rates[c]; r > best {
			best = r
		}
	}
	return userRate - best
}

// Render writes the report in the chosen format to w.
func Render(w io.Writer, d Data, format Format) error {
	switch format {
	case HTML:
		return renderHTML(w, d)
	case JSON:
		return renderJSON(w, d)
	case CSV:
		return renderCSV(w, d)
	default:
		return renderMarkdown(w, d)
	}
}

// renderJSON emits the report Data as pretty-printed JSON for machine
// consumption. The exported shape is the public surface of this package, so
// future fields just appear automatically.
func renderJSON(w io.Writer, d Data) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}

// renderCSV emits one row per (prompt, provider) cell with columns:
// prompt, provider, model, samples, failures, your_rate, <competitor_rate>…, gap.
// Suitable for piping into a spreadsheet.
func renderCSV(w io.Writer, d Data) error {
	wr := csv.NewWriter(w)
	header := []string{"prompt", "provider", "model", "samples", "failures", d.UserBrand}
	header = append(header, d.Competitors...)
	header = append(header, "gap")
	if err := wr.Write(header); err != nil {
		return err
	}
	for _, c := range d.Cells {
		row := []string{
			c.Prompt,
			c.ProviderName,
			c.Model,
			fmt.Sprintf("%d", c.Samples),
			fmt.Sprintf("%d", c.Failures),
			fmt.Sprintf("%.4f", c.Rates[d.UserBrand]),
		}
		for _, comp := range d.Competitors {
			row = append(row, fmt.Sprintf("%.4f", c.Rates[comp]))
		}
		row = append(row, fmt.Sprintf("%.4f", c.Gap))
		if err := wr.Write(row); err != nil {
			return err
		}
	}
	wr.Flush()
	return wr.Error()
}

func renderMarkdown(w io.Writer, d Data) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Salience report — run #%d\n\n", d.RunID)
	fmt.Fprintf(&b, "- Brand: **%s**\n", d.UserBrand)
	if len(d.Competitors) > 0 {
		fmt.Fprintf(&b, "- Competitors: %s\n", strings.Join(d.Competitors, ", "))
	}
	fmt.Fprintf(&b, "- Started: %s\n", d.Started)
	if d.Finished != "" {
		fmt.Fprintf(&b, "- Finished: %s\n", d.Finished)
	}
	fmt.Fprintf(&b, "- Status: %s\n", d.Status)
	fmt.Fprintf(&b, "- Samples: %d successful, %d failed\n\n", d.TotalSamples, d.TotalFailures)

	if d.hasLowSampleCells() {
		fmt.Fprintf(&b, "> ⚠ **Statistical warning:** one or more cells have fewer than %d samples. "+
			"Rates from tiny n are noisy — the report shows 95%% confidence intervals to make this visible. "+
			"Bump `samples_per_prompt` to ≥10 for tighter bounds.\n\n", LowSampleThreshold)
	}

	fmt.Fprintf(&b, "## Overall mention rate\n\n")
	fmt.Fprintf(&b, "| Brand | Rate |\n|---|---|\n")
	fmt.Fprintf(&b, "| **%s** (you) | %s |\n", d.UserBrand, pct(d.Totals.Rates[d.UserBrand]))
	for _, c := range d.Competitors {
		fmt.Fprintf(&b, "| %s | %s |\n", c, pct(d.Totals.Rates[c]))
	}
	fmt.Fprintln(&b)

	losing, winning := splitCells(d.Cells)

	// Multi-region runs group cells by region so each section uses only
	// the competitors that actually apply to that region. (Tracking
	// Tsubaki as a "competitor" in India is noise; it isn't sold there.)
	regionCodes := distinctRegions(d.Cells)
	if len(regionCodes) > 1 {
		for _, region := range regionCodes {
			regionLabel := region
			fmt.Fprintf(&b, "## Region: %s\n\n", regionLabel)

			regionComps := d.CompetitorsForRegion(region)
			regionLosing, regionWinning := splitCellsForRegion(losing, winning, region)

			fmt.Fprintf(&b, "### Where you are losing in %s\n\n", regionLabel)
			if len(regionLosing) == 0 {
				fmt.Fprintf(&b, "_None._\n\n")
			} else {
				writeCellTableMD(&b, regionLosing, d.UserBrand, regionComps)
			}

			fmt.Fprintf(&b, "### Where you are winning or tied in %s\n\n", regionLabel)
			if len(regionWinning) == 0 {
				fmt.Fprintf(&b, "_None._\n\n")
			} else {
				writeCellTableMD(&b, regionWinning, d.UserBrand, regionComps)
			}
		}
	} else {
		// Single-region runs: keep the v0.1–v0.3 layout.
		fmt.Fprintf(&b, "## Prompts where you are losing\n\n")
		if len(losing) == 0 {
			fmt.Fprintf(&b, "_None._\n\n")
		} else {
			writeCellTableMD(&b, losing, d.UserBrand, d.Competitors)
		}

		fmt.Fprintf(&b, "## Prompts where you are winning or tied\n\n")
		if len(winning) == 0 {
			fmt.Fprintf(&b, "_None._\n\n")
		} else {
			writeCellTableMD(&b, winning, d.UserBrand, d.Competitors)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// distinctRegions returns the unique region codes seen across cells, in
// stable order ("global" first if present, then alphabetical).
func distinctRegions(cells []Cell) []string {
	seen := map[string]bool{}
	for _, c := range cells {
		r := c.Region
		if r == "" {
			r = "global"
		}
		seen[r] = true
	}
	out := make([]string, 0, len(seen))
	if seen["global"] {
		out = append(out, "global")
	}
	rest := make([]string, 0, len(seen))
	for r := range seen {
		if r != "global" {
			rest = append(rest, r)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// splitCellsForRegion partitions losing/winning cell lists by region.
func splitCellsForRegion(losing, winning []Cell, region string) ([]Cell, []Cell) {
	var l, w []Cell
	for _, c := range losing {
		r := c.Region
		if r == "" {
			r = "global"
		}
		if r == region {
			l = append(l, c)
		}
	}
	for _, c := range winning {
		r := c.Region
		if r == "" {
			r = "global"
		}
		if r == region {
			w = append(w, c)
		}
	}
	return l, w
}

func writeCellTableMD(b *strings.Builder, cells []Cell, user string, comps []string) {
	// Only show the Region column when at least one cell carries a
	// non-global region — otherwise the column is noise.
	multiRegion := false
	for _, c := range cells {
		if c.Region != "" && c.Region != "global" {
			multiRegion = true
			break
		}
	}
	header := []string{"Prompt", "Provider", "Model"}
	if multiRegion {
		header = append(header, "Region")
	}
	header = append(header, "Samples", "You")
	for _, c := range comps {
		header = append(header, c)
	}
	header = append(header, "Gap")
	fmt.Fprintf(b, "| %s |\n", strings.Join(header, " | "))
	sep := make([]string, len(header))
	for i := range sep {
		sep[i] = "---"
	}
	fmt.Fprintf(b, "| %s |\n", strings.Join(sep, " | "))
	for _, c := range cells {
		nLabel := fmt.Sprintf("%d", c.Samples)
		if c.Samples < LowSampleThreshold {
			nLabel = fmt.Sprintf("%d⚠", c.Samples)
		}
		row := []string{truncate(c.Prompt, 80), c.ProviderName, c.Model}
		if multiRegion {
			region := c.Region
			if region == "" {
				region = "global"
			}
			row = append(row, region)
		}
		row = append(row, nLabel, fmt.Sprintf("%s (CI %s–%s)",
			pct(c.Rates[user]), pct(c.CILow[user]), pct(c.CIHigh[user])))
		for _, comp := range comps {
			row = append(row, pct(c.Rates[comp]))
		}
		row = append(row, signedPct(c.Gap))
		fmt.Fprintf(b, "| %s |\n", strings.Join(row, " | "))
	}
	fmt.Fprintln(b)
}

func renderHTML(w io.Writer, d Data) error {
	var b strings.Builder
	fmt.Fprintf(&b, "<!doctype html><html><head><meta charset=\"utf-8\"><title>Salience report — run %d</title>", d.RunID)
	fmt.Fprintf(&b, "<style>body{font-family:system-ui,sans-serif;max-width:1100px;margin:2em auto;padding:0 1em;color:#222}")
	fmt.Fprintf(&b, "table{border-collapse:collapse;width:100%%;margin:1em 0}")
	fmt.Fprintf(&b, "th,td{border:1px solid #ddd;padding:6px 10px;font-size:14px;text-align:left}")
	fmt.Fprintf(&b, "th{background:#f4f4f4}.you{font-weight:bold}.neg{color:#b00020}.pos{color:#1a7f37}")
	fmt.Fprintf(&b, "h2{margin-top:2em;border-bottom:1px solid #eee;padding-bottom:4px}</style></head><body>")

	fmt.Fprintf(&b, "<h1>Salience report — run #%d</h1>", d.RunID)
	fmt.Fprintf(&b, "<ul><li>Brand: <strong>%s</strong></li>", html.EscapeString(d.UserBrand))
	if len(d.Competitors) > 0 {
		esc := make([]string, len(d.Competitors))
		for i, c := range d.Competitors {
			esc[i] = html.EscapeString(c)
		}
		fmt.Fprintf(&b, "<li>Competitors: %s</li>", strings.Join(esc, ", "))
	}
	fmt.Fprintf(&b, "<li>Started: %s</li>", html.EscapeString(d.Started))
	if d.Finished != "" {
		fmt.Fprintf(&b, "<li>Finished: %s</li>", html.EscapeString(d.Finished))
	}
	fmt.Fprintf(&b, "<li>Status: %s</li>", html.EscapeString(d.Status))
	fmt.Fprintf(&b, "<li>Samples: %d successful, %d failed</li></ul>", d.TotalSamples, d.TotalFailures)

	fmt.Fprintf(&b, "<h2>Overall mention rate</h2><table><tr><th>Brand</th><th>Rate</th></tr>")
	fmt.Fprintf(&b, "<tr class=\"you\"><td>%s (you)</td><td>%s</td></tr>", html.EscapeString(d.UserBrand), pct(d.Totals.Rates[d.UserBrand]))
	for _, c := range d.Competitors {
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td></tr>", html.EscapeString(c), pct(d.Totals.Rates[c]))
	}
	fmt.Fprintf(&b, "</table>")

	losing, winning := splitCells(d.Cells)
	fmt.Fprintf(&b, "<h2>Prompts where you are losing</h2>")
	writeCellTableHTML(&b, losing, d.UserBrand, d.Competitors)
	fmt.Fprintf(&b, "<h2>Prompts where you are winning or tied</h2>")
	writeCellTableHTML(&b, winning, d.UserBrand, d.Competitors)
	fmt.Fprintf(&b, "</body></html>")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeCellTableHTML(b *strings.Builder, cells []Cell, user string, comps []string) {
	if len(cells) == 0 {
		fmt.Fprintf(b, "<p><em>None.</em></p>")
		return
	}
	fmt.Fprintf(b, "<table><tr><th>Prompt</th><th>Provider</th><th>Model</th><th>Samples</th><th class=\"you\">%s</th>", html.EscapeString(user))
	for _, c := range comps {
		fmt.Fprintf(b, "<th>%s</th>", html.EscapeString(c))
	}
	fmt.Fprintf(b, "<th>Gap</th></tr>")
	for _, c := range cells {
		gapClass := ""
		if c.Gap < 0 {
			gapClass = "neg"
		} else if c.Gap > 0 {
			gapClass = "pos"
		}
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%d</td><td class=\"you\">%s</td>",
			html.EscapeString(truncate(c.Prompt, 120)), html.EscapeString(c.ProviderName),
			html.EscapeString(c.Model), c.Samples, pct(c.Rates[user]))
		for _, comp := range comps {
			fmt.Fprintf(b, "<td>%s</td>", pct(c.Rates[comp]))
		}
		fmt.Fprintf(b, "<td class=\"%s\">%s</td></tr>", gapClass, signedPct(c.Gap))
	}
	fmt.Fprintf(b, "</table>")
}

func splitCells(in []Cell) (losing, winning []Cell) {
	for _, c := range in {
		if c.Gap < 0 {
			losing = append(losing, c)
		} else {
			winning = append(winning, c)
		}
	}
	return
}

func pct(f float64) string {
	return fmt.Sprintf("%.0f%%", f*100)
}

func signedPct(f float64) string {
	if f > 0 {
		return fmt.Sprintf("+%.0f%%", f*100)
	}
	return fmt.Sprintf("%.0f%%", f*100)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// LoadCompetitorsFromConfigJSON extracts competitor canonical names from the
// stored config_json blob; useful when the config file is gone but the run
// remains in the database. Returns names only — for the region-aware full
// brand structs use LoadBrandsFromConfigJSON.
func LoadCompetitorsFromConfigJSON(ctx context.Context, cfgJSON string) (userBrand string, competitors []string, err error) {
	var c config.Config
	if e := json.Unmarshal([]byte(cfgJSON), &c); e != nil {
		return "", nil, e
	}
	userBrand = c.Brand.Name
	for _, comp := range c.Competitors {
		competitors = append(competitors, comp.Name)
	}
	return userBrand, competitors, nil
}

// LoadBrandsFromConfigJSON is the region-aware variant: returns the full
// competitor Brand structs (with Regions populated) so callers can do
// per-region filtering in the report.
func LoadBrandsFromConfigJSON(ctx context.Context, cfgJSON string) (userBrand string, competitors []config.Brand, err error) {
	var c config.Config
	if e := json.Unmarshal([]byte(cfgJSON), &c); e != nil {
		return "", nil, e
	}
	return c.Brand.Name, c.Competitors, nil
}
