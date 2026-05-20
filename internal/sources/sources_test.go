package sources

import (
	"encoding/json"
	"testing"

	"github.com/salience-cli/salience/internal/detect"
)

func TestDomainOf(t *testing.T) {
	cases := map[string]string{
		"https://www.g2.com/contoso/reviews":            "g2.com",
		"http://reddit.com/r/saas":                      "reddit.com",
		"contoso.example/pricing":                       "contoso.example",
		"https://developers.example.com/api/v1":         "developers.example.com",
		"https://news.ycombinator.com/item?id=123":      "news.ycombinator.com",
	}
	for in, want := range cases {
		if got := DomainOf(in); got != want {
			t.Errorf("DomainOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDomainCategory(t *testing.T) {
	vendor := map[string]bool{"northwind.example": true}
	cases := []struct {
		domain string
		want   Category
	}{
		{"g2.com", CatReview},
		{"capterra.com", CatReview},
		{"reddit.com", CatForum},
		{"news.ycombinator.com", CatForum},
		{"en.wikipedia.org", CatWiki},
		{"techcrunch.com", CatNews},
		{"twitter.com", CatSocial},
		{"x.com", CatSocial},
		{"medium.com", CatBlog},
		{"northwind.example", CatVendor},
		{"docs.example.com", CatDocs},
		{"weird-private-blog.io", CatOther},
	}
	for _, c := range cases {
		got := domainCategory(c.domain, vendor)
		if got != c.want {
			t.Errorf("domainCategory(%q) = %q, want %q", c.domain, got, c.want)
		}
	}
}

func TestAnalyzeSamples_PerBrandAndGap(t *testing.T) {
	// Three samples, three brands.
	samples := []SampleWithSources{
		{
			SampleID:  1,
			BrandsHit: []string{"Northwind", "Contoso"},
			Sources: []detect.Source{
				{URL: "https://g2.com/best-crm", Title: "Best CRM"},
				{URL: "https://reddit.com/r/saas/contoso", Title: "Contoso thread"},
			},
		},
		{
			SampleID:  2,
			BrandsHit: []string{"Contoso"},
			Sources: []detect.Source{
				{URL: "https://g2.com/best-crm", Title: "Best CRM"}, // same URL again
				{URL: "https://news.ycombinator.com/item?id=42", Title: "HN"},
			},
		},
		{
			SampleID:  3,
			BrandsHit: []string{"Northwind"},
			Sources: []detect.Source{
				{URL: "https://northwind.example/about", Title: "About"},
			},
		},
	}
	r := AnalyzeSamples(samples, "Northwind",
		[]string{"Contoso", "Fabrikam"},
		map[string][]string{
			"Northwind": {"northwind.example"},
			"Contoso":   {"contoso.example"},
		},
	)

	// g2.com is the top domain (3 cites).
	if len(r.Domains) == 0 || r.Domains[0].Domain != "g2.com" {
		t.Fatalf("expected g2.com top, got %#v", r.Domains)
	}
	// Reddit + HN drive Contoso mentions but never co-occur with Northwind.
	gapDomains := map[string]bool{}
	for _, d := range r.MissingFromBrand {
		gapDomains[d.Domain] = true
	}
	if !gapDomains["news.ycombinator.com"] {
		t.Errorf("HN should be in the source gap; got %#v", r.MissingFromBrand)
	}
	// Northwind's own domain should be classified vendor and appear in
	// PerBrand[Northwind].
	foundVendor := false
	for _, d := range r.PerBrand["Northwind"] {
		if d.Domain == "northwind.example" {
			foundVendor = true
			if d.Category != CatVendor {
				t.Errorf("expected vendor category, got %q", d.Category)
			}
		}
	}
	if !foundVendor {
		t.Errorf("Northwind's own domain should appear under PerBrand[Northwind]")
	}
}

func TestAnalyzeSamples_EmptyInput(t *testing.T) {
	r := AnalyzeSamples(nil, "X", []string{"Y"}, nil)
	if len(r.URLs) != 0 || len(r.Domains) != 0 {
		t.Fatalf("expected empty report, got %#v", r)
	}
}

func TestDecodeSamples_HandlesNullAndMalformed(t *testing.T) {
	rows := []sampleRowStub{
		{ID: 1, BrandsHit: []string{"A"}},
		{ID: 2, BrandsHit: nil},
		{ID: 3, BrandsHit: []string{"B"}},
	}
	srcJSON := map[int64]string{
		1: `[{"URL":"https://x.com"}]`,
		2: `null`,
		3: `oops not json`,
	}
	out, errs := decodeForTest(rows, srcJSON)
	if len(out) != 3 {
		t.Fatalf("expected 3 samples returned, got %d", len(out))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 decode error, got %d", len(errs))
	}
	if len(out[0].Sources) != 1 || out[0].Sources[0].URL != "https://x.com" {
		t.Errorf("first sample's source missing or wrong: %#v", out[0])
	}
}

// --- helpers (kept here so the test doesn't reach into store) ---

type sampleRowStub struct {
	ID        int64
	BrandsHit []string
}

// decodeForTest mirrors DecodeSamples but accepts the stub type so this file
// stays independent of the store package.
func decodeForTest(rows []sampleRowStub, sourcesJSONBySampleID map[int64]string) ([]SampleWithSources, []error) {
	var errs []error
	out := make([]SampleWithSources, 0, len(rows))
	for _, r := range rows {
		s := SampleWithSources{
			SampleID:  r.ID,
			BrandsHit: r.BrandsHit,
		}
		j := sourcesJSONBySampleID[r.ID]
		if j != "" && j != "null" {
			if err := unmarshalSources(j, &s.Sources); err != nil {
				errs = append(errs, err)
			}
		}
		out = append(out, s)
	}
	return out, errs
}

func unmarshalSources(j string, dst *[]detect.Source) error {
	return json.Unmarshal([]byte(j), dst)
}
