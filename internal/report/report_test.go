package report

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/salience-cli/salience/internal/store"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRate(t *testing.T) {
	if r := rate(0, 0); r != 0 {
		t.Errorf("rate(0,0) = %v, want 0", r)
	}
	if r := rate(3, 10); !approx(r, 0.3) {
		t.Errorf("rate(3,10) = %v, want 0.3", r)
	}
	if r := rate(10, 10); !approx(r, 1) {
		t.Errorf("rate(10,10) = %v, want 1", r)
	}
}

func TestComputeGap_Losing(t *testing.T) {
	rates := map[string]float64{"You": 0.2, "Comp1": 0.6, "Comp2": 0.4}
	g := computeGap(0.2, []string{"Comp1", "Comp2"}, rates)
	if !approx(g, -0.4) {
		t.Errorf("gap = %v, want -0.4", g)
	}
}

func TestComputeGap_Winning(t *testing.T) {
	rates := map[string]float64{"You": 0.8, "Comp1": 0.3, "Comp2": 0.5}
	g := computeGap(0.8, []string{"Comp1", "Comp2"}, rates)
	if !approx(g, 0.3) {
		t.Errorf("gap = %v, want 0.3", g)
	}
}

func TestComputeGap_NoCompetitors(t *testing.T) {
	rates := map[string]float64{"You": 0.5}
	g := computeGap(0.5, nil, rates)
	if !approx(g, 0.5) {
		t.Errorf("gap = %v, want 0.5", g)
	}
}

func TestBuild_AggregatesAndSortsLosingFirst(t *testing.T) {
	meta := &store.Run{
		ID:        1,
		StartedAt: time.Now(),
		Status:    "completed",
	}
	samples := []store.SampleRow{
		// prompt A / openai: 4 samples; You hit twice, Comp1 hit four times -> Gap = -0.5
		{Prompt: "A", ProviderName: "openai-x", Model: "m1", SampleIdx: 0, BrandsHit: []string{"You", "Comp1"}},
		{Prompt: "A", ProviderName: "openai-x", Model: "m1", SampleIdx: 1, BrandsHit: []string{"Comp1"}},
		{Prompt: "A", ProviderName: "openai-x", Model: "m1", SampleIdx: 2, BrandsHit: []string{"You", "Comp1"}},
		{Prompt: "A", ProviderName: "openai-x", Model: "m1", SampleIdx: 3, BrandsHit: []string{"Comp1"}},

		// prompt B / openai: 2 samples; You hit both, Comp1 hit none -> Gap = +1.0
		{Prompt: "B", ProviderName: "openai-x", Model: "m1", SampleIdx: 0, BrandsHit: []string{"You"}},
		{Prompt: "B", ProviderName: "openai-x", Model: "m1", SampleIdx: 1, BrandsHit: []string{"You"}},

		// one failure should not skew rates
		{Prompt: "A", ProviderName: "openai-x", Model: "m1", SampleIdx: 4, Error: "boom"},
	}

	d := Build(1, meta, samples, "You", []string{"Comp1"})

	if d.TotalSamples != 6 || d.TotalFailures != 1 {
		t.Errorf("totals: got %d/%d, want 6/1", d.TotalSamples, d.TotalFailures)
	}
	if len(d.Cells) != 2 {
		t.Fatalf("expected 2 cells, got %d", len(d.Cells))
	}
	// Worst gap should be first.
	if d.Cells[0].Prompt != "A" {
		t.Errorf("expected losing prompt A first, got %q (gap=%v)", d.Cells[0].Prompt, d.Cells[0].Gap)
	}
	if !approx(d.Cells[0].Gap, -0.5) {
		t.Errorf("prompt A gap = %v, want -0.5", d.Cells[0].Gap)
	}
	if !approx(d.Cells[1].Gap, 1.0) {
		t.Errorf("prompt B gap = %v, want 1.0", d.Cells[1].Gap)
	}
	// Per-brand overall rates: 4 You hits over 6 samples
	if !approx(d.Totals.Rates["You"], 4.0/6.0) {
		t.Errorf("overall You rate = %v, want %v", d.Totals.Rates["You"], 4.0/6.0)
	}
	// 4 Comp1 hits over 6 samples
	if !approx(d.Totals.Rates["Comp1"], 4.0/6.0) {
		t.Errorf("overall Comp1 rate = %v, want %v", d.Totals.Rates["Comp1"], 4.0/6.0)
	}
}

func TestRenderMarkdownMentionsBothSections(t *testing.T) {
	meta := &store.Run{ID: 1, StartedAt: time.Now(), Status: "completed"}
	samples := []store.SampleRow{
		{Prompt: "A", ProviderName: "p", Model: "m", SampleIdx: 0, BrandsHit: []string{"Comp1"}},
		{Prompt: "B", ProviderName: "p", Model: "m", SampleIdx: 0, BrandsHit: []string{"You"}},
	}
	d := Build(1, meta, samples, "You", []string{"Comp1"})
	var buf bytes.Buffer
	if err := Render(&buf, d, Markdown); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "losing") {
		t.Errorf("markdown should contain the losing section header")
	}
	if !strings.Contains(out, "winning") {
		t.Errorf("markdown should contain the winning section header")
	}
}

func TestRenderHTMLEscapes(t *testing.T) {
	meta := &store.Run{ID: 1, StartedAt: time.Now(), Status: "completed"}
	samples := []store.SampleRow{
		{Prompt: "<script>alert(1)</script>", ProviderName: "p", Model: "m", SampleIdx: 0, BrandsHit: []string{"You"}},
	}
	d := Build(1, meta, samples, "You", []string{"Comp1"})
	var buf bytes.Buffer
	if err := Render(&buf, d, HTML); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("HTML output must escape script tags")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("HTML output should contain escaped tag")
	}
}
