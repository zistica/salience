// Package sources analyzes the grounded URLs cited by LLM responses. It
// answers two questions: which pages do the models actually look at, and
// which of those pages drive each brand's mention rate?
//
// All the data lives in salience.db already — every sample row stores the
// provider's citations as JSON. This package crunches that into rankings.
package sources

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/store"
)

// Citation is one URL → which brands were mentioned in the sample that cited it.
type Citation struct {
	URL        string
	Domain     string
	Category   Category
	Brands     []string // unique brand names mentioned in any sample that cited this URL
	Count      int      // total number of samples that cited this URL
	Title      string   // best non-empty title we saw
	Snippet    string   // best non-empty snippet
}

// DomainRank summarizes how often a domain is cited and by which brands.
type DomainRank struct {
	Domain   string
	Category Category
	Count    int
	Brands   map[string]int // brand → samples in which this domain was cited alongside a mention of that brand
}

// Category is a coarse bucket for the kind of site a domain represents.
// We don't need to be exhaustive — the buckets that matter are the ones LLMs
// disproportionately ground against.
type Category string

const (
	CatReview  Category = "review"   // G2, Capterra, Software Advice…
	CatForum   Category = "forum"    // Reddit, Hacker News, Stack Overflow…
	CatWiki    Category = "wiki"     // Wikipedia, Britannica
	CatNews    Category = "news"     // TechCrunch, The Verge, Ars Technica…
	CatSocial  Category = "social"   // Twitter/X, LinkedIn, YouTube
	CatDocs    Category = "docs"     // *.com/docs/*, developers.* etc.
	CatBlog    Category = "blog"     // Medium, Substack, dev.to
	CatVendor  Category = "vendor"   // brand's own domain
	CatOther   Category = "other"
)

// domainCategory looks up the coarse category of a domain. Matching is by
// suffix so subdomains (`developers.example.com`) are handled.
func domainCategory(domain string, vendorDomains map[string]bool) Category {
	d := strings.ToLower(domain)
	if vendorDomains[d] {
		return CatVendor
	}
	for suf, cat := range domainBuckets {
		if d == suf || strings.HasSuffix(d, "."+suf) {
			return cat
		}
	}
	// Heuristics for sub-strings.
	switch {
	case strings.Contains(d, "wiki"):
		return CatWiki
	case strings.HasPrefix(d, "docs.") || strings.HasPrefix(d, "developers.") || strings.HasPrefix(d, "developer."):
		return CatDocs
	case strings.HasPrefix(d, "blog."):
		return CatBlog
	}
	return CatOther
}

var domainBuckets = map[string]Category{
	// Review aggregators
	"g2.com":             CatReview,
	"capterra.com":       CatReview,
	"getapp.com":         CatReview,
	"software.com":       CatReview,
	"softwareadvice.com": CatReview,
	"trustradius.com":    CatReview,
	"trustpilot.com":     CatReview,
	"gartner.com":        CatReview,
	"forrester.com":      CatReview,

	// Forums / community
	"reddit.com":          CatForum,
	"news.ycombinator.com": CatForum,
	"stackoverflow.com":   CatForum,
	"quora.com":           CatForum,
	"slashdot.org":        CatForum,
	"lobste.rs":           CatForum,
	"indiehackers.com":    CatForum,

	// Encyclopedias
	"wikipedia.org": CatWiki,
	"britannica.com": CatWiki,

	// Tech press
	"techcrunch.com":      CatNews,
	"theverge.com":        CatNews,
	"arstechnica.com":     CatNews,
	"wired.com":           CatNews,
	"reuters.com":         CatNews,
	"bloomberg.com":       CatNews,
	"forbes.com":          CatNews,
	"venturebeat.com":     CatNews,
	"techradar.com":       CatNews,
	"engadget.com":        CatNews,
	"businessinsider.com": CatNews,
	"protocol.com":        CatNews,

	// Social
	"twitter.com":  CatSocial,
	"x.com":        CatSocial,
	"linkedin.com": CatSocial,
	"youtube.com":  CatSocial,
	"facebook.com": CatSocial,
	"tiktok.com":   CatSocial,

	// Blog platforms
	"medium.com":   CatBlog,
	"substack.com": CatBlog,
	"dev.to":       CatBlog,
	"hashnode.com": CatBlog,
}

// Report is the aggregate per-run output of source attribution.
type Report struct {
	RunID    int64
	Brand    string
	URLs     []Citation
	Domains  []DomainRank
	// PerBrand maps each tracked brand name to the domains that most often
	// appear alongside its mentions.
	PerBrand map[string][]DomainRank
	// MissingFromBrand is the set of domains that drive a competitor's
	// citations but never co-occur with the user's brand.
	MissingFromBrand []DomainRank
}

// normalizeDomain lowercases and strips leading "www.".
func normalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimPrefix(d, "www.")
	if i := strings.IndexByte(d, '/'); i >= 0 {
		d = d[:i]
	}
	if i := strings.IndexByte(d, ':'); i >= 0 {
		d = d[:i]
	}
	return d
}

// DomainOf returns the normalized domain of u, or "" if it can't be parsed.
func DomainOf(u string) string {
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return normalizeDomain(u)
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
}

// SortedCitations returns citations sorted by Count desc, then URL asc.
func SortedCitations(cs []Citation) []Citation {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Count != cs[j].Count {
			return cs[i].Count > cs[j].Count
		}
		return cs[i].URL < cs[j].URL
	})
	return cs
}

// SortedDomains returns ranks sorted by Count desc.
func SortedDomains(ds []DomainRank) []DomainRank {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].Count != ds[j].Count {
			return ds[i].Count > ds[j].Count
		}
		return ds[i].Domain < ds[j].Domain
	})
	return ds
}

// AnalyzeSamples is the concrete entrypoint callers use: it takes the parsed
// per-sample (sources, brands-mentioned) data and produces a Report.
func AnalyzeSamples(samples []SampleWithSources, userBrand string, competitors []string, vendorAliases map[string][]string) Report {
	vendorDomains := map[string]bool{}
	for _, aliases := range vendorAliases {
		for _, a := range aliases {
			d := strings.ToLower(strings.TrimSpace(a))
			if strings.Contains(d, ".") && !strings.Contains(d, " ") {
				vendorDomains[normalizeDomain(d)] = true
			}
		}
	}
	allBrands := append([]string{userBrand}, competitors...)

	cites := map[string]*Citation{}
	doms := map[string]*DomainRank{}
	perBrand := map[string]map[string]*DomainRank{}
	for _, br := range allBrands {
		perBrand[br] = map[string]*DomainRank{}
	}

	for _, s := range samples {
		brandsHere := map[string]bool{}
		for _, b := range s.BrandsHit {
			brandsHere[b] = true
		}
		for _, src := range s.Sources {
			if src.URL == "" {
				continue
			}
			c, ok := cites[src.URL]
			if !ok {
				c = &Citation{
					URL:      src.URL,
					Domain:   DomainOf(src.URL),
					Category: domainCategory(DomainOf(src.URL), vendorDomains),
					Title:    src.Title,
					Snippet:  src.Snippet,
				}
				cites[src.URL] = c
			} else {
				if c.Title == "" {
					c.Title = src.Title
				}
				if c.Snippet == "" {
					c.Snippet = src.Snippet
				}
			}
			c.Count++
			for b := range brandsHere {
				if !containsString(c.Brands, b) {
					c.Brands = append(c.Brands, b)
				}
			}

			d := c.Domain
			dr, ok := doms[d]
			if !ok {
				dr = &DomainRank{
					Domain:   d,
					Category: c.Category,
					Brands:   map[string]int{},
				}
				doms[d] = dr
			}
			dr.Count++
			for b := range brandsHere {
				dr.Brands[b]++
				pb := perBrand[b]
				if pb != nil {
					pbDr, ok := pb[d]
					if !ok {
						pbDr = &DomainRank{Domain: d, Category: dr.Category, Brands: map[string]int{}}
						pb[d] = pbDr
					}
					pbDr.Count++
				}
			}
		}
	}

	urls := make([]Citation, 0, len(cites))
	for _, c := range cites {
		urls = append(urls, *c)
	}
	urls = SortedCitations(urls)

	domList := make([]DomainRank, 0, len(doms))
	for _, d := range doms {
		domList = append(domList, *d)
	}
	domList = SortedDomains(domList)

	pb := map[string][]DomainRank{}
	for br, m := range perBrand {
		list := make([]DomainRank, 0, len(m))
		for _, d := range m {
			list = append(list, *d)
		}
		pb[br] = SortedDomains(list)
	}

	// Gap: domains that drive any competitor mention but never co-occur with
	// the user's brand.
	var missing []DomainRank
	userDomains := map[string]bool{}
	for _, d := range pb[userBrand] {
		userDomains[d.Domain] = true
	}
	for _, d := range domList {
		anyCompetitor := false
		for _, c := range competitors {
			if d.Brands[c] > 0 {
				anyCompetitor = true
				break
			}
		}
		if anyCompetitor && !userDomains[d.Domain] {
			missing = append(missing, d)
		}
	}

	return Report{
		Brand:            userBrand,
		URLs:             urls,
		Domains:          domList,
		PerBrand:         pb,
		MissingFromBrand: missing,
	}
}

// SampleWithSources is the input the analyzer needs: per sample, the list of
// brands detected in it and the list of grounded sources cited.
type SampleWithSources struct {
	SampleID    int64
	BrandsHit   []string
	Sources     []detect.Source
}

// DecodeSamples turns store.SampleRow plus the raw sources_json fetched from
// the same database into the analyzer's expected shape. JSON parse failures
// are logged via the returned errors slice but don't abort the analysis.
func DecodeSamples(rows []store.SampleRow, sourcesJSONBySampleID map[int64]string) ([]SampleWithSources, []error) {
	var errs []error
	out := make([]SampleWithSources, 0, len(rows))
	for _, r := range rows {
		s := SampleWithSources{
			SampleID:  r.ID,
			BrandsHit: r.BrandsHit,
		}
		j := sourcesJSONBySampleID[r.ID]
		if j != "" && j != "null" {
			if err := json.Unmarshal([]byte(j), &s.Sources); err != nil {
				errs = append(errs, err)
			}
		}
		out = append(out, s)
	}
	return out, errs
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
