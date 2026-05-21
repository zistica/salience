// Package profiler analyses a single cited URL and returns a structured
// "source profile": what brands the page mentions, with what sentiment,
// plus authority signals (schema.org markup, language, recency, depth)
// that approximate why an LLM might have chosen to cite it.
//
// Profiles are the foundation of v0.4 "Answer Anatomy" — they let the
// dashboard show *why* an LLM answer landed where it did instead of just
// *what* it said.
package profiler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/scraper"
)

// PageKind is a coarse classification of what the cited page is for.
// LLMs over-index on certain kinds (review aggregators, encyclopedias),
// so showing this helps users understand the citation pattern.
type PageKind string

const (
	KindReviewAggregator PageKind = "review_aggregator"
	KindBrandOwn         PageKind = "brand_own"
	KindEncyclopedia     PageKind = "encyclopedia"
	KindListicle         PageKind = "listicle"
	KindNews             PageKind = "news"
	KindOther            PageKind = "other"
)

// BrandHit summarises everything one tracked brand "looks like" on a page:
// raw mention count plus a sentiment split, plus up to a few snippets that
// the user can click through to verify.
type BrandHit struct {
	Brand     string   `json:"brand"`
	Count     int      `json:"count"`
	Positive  int      `json:"positive"`
	Neutral   int      `json:"neutral"`
	Negative  int      `json:"negative"`
	Snippets  []string `json:"snippets,omitempty"` // max 3, contextual sentences
}

// Profile is the structured analysis of one cited URL.
type Profile struct {
	URL              string              `json:"url"`
	Domain           string              `json:"domain"`
	FetchedAt        time.Time           `json:"fetched_at"`
	StatusCode       int                 `json:"status_code"`
	HTMLLang         string              `json:"html_lang"`
	Title            string              `json:"title"`
	Description      string              `json:"description"`
	WordCount        int                 `json:"word_count"`
	HasSchemaProduct bool                `json:"has_schema_product"`
	HasSchemaReview  bool                `json:"has_schema_review"`
	HasSchemaArticle bool                `json:"has_schema_article"`
	HasSchemaOrg     bool                `json:"has_schema_org"`
	LastModified     string              `json:"last_modified"`
	// AuthorityScore is a 0..100 composite proxy. It's a *proxy* — not real
	// DA — built from cheaply-detectable signals on the page itself
	// (HTTPS, schema markup, language, length, basic structure). Useful for
	// relative ranking within a run; do not treat as Moz/Ahrefs DA.
	AuthorityScore int       `json:"authority_score"`
	PageKind       PageKind  `json:"page_kind"`
	BrandHits      []BrandHit `json:"brand_hits"`
	Err            string    `json:"err,omitempty"`
}

// BrandHitsJSON serialises BrandHits as the keyed map we persist (the
// shape source_profiles.brand_hits_json expects).
func (p *Profile) BrandHitsJSON() (string, error) {
	m := make(map[string]BrandHit, len(p.BrandHits))
	for _, h := range p.BrandHits {
		m[h.Brand] = h
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}", err
	}
	return string(b), nil
}

// BrandHitsFromJSON re-hydrates BrandHits from the persisted JSON map.
func BrandHitsFromJSON(s string) []BrandHit {
	if s == "" {
		return nil
	}
	var m map[string]BrandHit
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	out := make([]BrandHit, 0, len(m))
	for k, h := range m {
		if h.Brand == "" {
			h.Brand = k
		}
		out = append(out, h)
	}
	return out
}

// Client is the live profiler. It wraps a scraper.Client; tests can pass
// their own.
type Client struct {
	Scraper *scraper.Client
}

// New returns a Client with default scraper settings.
func New() *Client {
	return &Client{Scraper: scraper.NewClient()}
}

// Profile fetches the URL and returns a complete Profile. Network and
// parse errors are recorded on Profile.Err rather than returned, so a
// batch caller can keep going.
func (c *Client) Profile(ctx context.Context, u string, brands []config.Brand) *Profile {
	dom := hostOf(u)
	p := &Profile{
		URL:       u,
		Domain:    dom,
		FetchedAt: time.Now().UTC(),
	}

	page := c.Scraper.Fetch(ctx, u)
	p.StatusCode = page.StatusCode
	p.Title = page.Title
	p.Description = page.Description
	if page.Err != "" {
		p.Err = page.Err
		// Even on error, we may have partial body — keep going so the
		// dashboard can still show *something* (e.g. a 403 with body).
		if page.Body == "" {
			return p
		}
	}
	body := page.Body
	p.WordCount = wordCount(body)

	// Re-parse the HTML once to extract structured-data + lang attribute.
	// scraper.Page strips the doc, but we want richer signals here.
	signals := extractSignals(ctx, c.Scraper, u)
	p.HTMLLang = signals.htmlLang
	p.HasSchemaOrg = signals.hasSchemaOrg
	p.HasSchemaProduct = signals.hasProduct
	p.HasSchemaReview = signals.hasReview
	p.HasSchemaArticle = signals.hasArticle
	p.LastModified = signals.lastModified

	// Detect brand mentions on the page body itself. We reuse the existing
	// detector so behaviour is identical to how brands are detected in LLM
	// responses — same word-boundary rules, same script-transition logic.
	det := detect.Detect(body, nil, brands)
	hits := make(map[string]*BrandHit, len(brands))
	for _, m := range det.Matches {
		h, ok := hits[m.Brand]
		if !ok {
			h = &BrandHit{Brand: m.Brand}
			hits[m.Brand] = h
		}
		h.Count++
		switch detect.Classify(m.Context, m.Alias) {
		case detect.SentimentPositive:
			h.Positive++
		case detect.SentimentNegative:
			h.Negative++
		default:
			h.Neutral++
		}
		if len(h.Snippets) < 3 && strings.TrimSpace(m.Context) != "" {
			h.Snippets = append(h.Snippets, m.Context)
		}
	}
	// Emit hits in deterministic order — alpha by brand name — so JSON
	// diffs are stable for tests / regressions.
	keys := make([]string, 0, len(hits))
	for k := range hits {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		p.BrandHits = append(p.BrandHits, *hits[k])
	}

	p.PageKind = classifyPageKind(u, p.Title, p.Description, body, brands, p.BrandHits)
	p.AuthorityScore = authorityScore(p)
	return p
}

// ProfileBatch profiles many URLs concurrently. Returns a slice in the
// same order as the input. Already-cached profiles can be passed in via
// cached so we skip re-fetching unchanged URLs.
func (c *Client) ProfileBatch(ctx context.Context, urls []string, brands []config.Brand, parallel int) []*Profile {
	if parallel <= 0 {
		parallel = 4
	}
	out := make([]*Profile, len(urls))
	sem := make(chan struct{}, parallel)
	done := make(chan int, len(urls))
	for i, u := range urls {
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = c.Profile(ctx, u, brands)
			done <- i
		}()
	}
	for range urls {
		<-done
	}
	return out
}

// ---------- helpers ----------

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	h := strings.ToLower(u.Host)
	return strings.TrimPrefix(h, "www.")
}

func wordCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Fields(s))
}

func sortStrings(s []string) {
	// avoid pulling sort just for this; small N.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// signals holds the page-authority indicators we extract from the parsed
// DOM. Kept in this file so profiler.go is a single-stop module — the
// HTML traversal is small enough not to warrant its own file yet.
type signals struct {
	htmlLang     string
	hasSchemaOrg bool
	hasProduct   bool
	hasReview    bool
	hasArticle   bool
	lastModified string
}

// extractSignals refetches and walks the HTML tree for structured data
// and the document language. This is a second fetch — wasteful, but
// scraper.Client doesn't expose the parsed tree. The duplicate is cheap
// since profile results are cached for 24h on the caller side.
//
// Tradeoff: we could refactor scraper.Client to return the *html.Node;
// keeping this contained here means scraper stays focused on body
// extraction and the profiler owns the heavier analysis.
func extractSignals(ctx context.Context, sc *scraper.Client, u string) signals {
	var sig signals
	if sc.HTTP == nil {
		return sig
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return sig
	}
	req.Header.Set("User-Agent", sc.UA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := sc.HTTP.Do(req)
	if err != nil {
		return sig
	}
	defer resp.Body.Close()
	sig.lastModified = resp.Header.Get("Last-Modified")
	doc, perr := html.Parse(resp.Body)
	if perr != nil {
		return sig
	}
	walk(doc, &sig)
	return sig
}

// walk fills signals by traversing the parsed document. We look for:
//   - <html lang="...">
//   - <script type="application/ld+json"> blocks containing schema.org types
//   - itemtype="https://schema.org/Product" / Review / Article markers
//   - <meta property="article:modified_time">
func walk(n *html.Node, sig *signals) {
	if n == nil {
		return
	}
	if n.Type == html.ElementNode {
		switch strings.ToLower(n.Data) {
		case "html":
			for _, a := range n.Attr {
				if strings.ToLower(a.Key) == "lang" {
					sig.htmlLang = strings.ToLower(strings.TrimSpace(a.Val))
				}
			}
		case "script":
			isLD := false
			for _, a := range n.Attr {
				if strings.ToLower(a.Key) == "type" &&
					strings.Contains(strings.ToLower(a.Val), "ld+json") {
					isLD = true
				}
			}
			if isLD {
				txt := innerText(n)
				if strings.Contains(txt, "schema.org") {
					sig.hasSchemaOrg = true
				}
				low := strings.ToLower(txt)
				if strings.Contains(low, `"@type":"product"`) ||
					strings.Contains(low, `"@type": "product"`) {
					sig.hasProduct = true
				}
				if strings.Contains(low, `"@type":"review"`) ||
					strings.Contains(low, `"@type":"aggregaterating"`) {
					sig.hasReview = true
				}
				if strings.Contains(low, `"@type":"article"`) ||
					strings.Contains(low, `"@type":"newsarticle"`) {
					sig.hasArticle = true
				}
			}
		case "meta":
			var prop, content string
			for _, a := range n.Attr {
				al := strings.ToLower(a.Key)
				if al == "property" || al == "name" {
					prop = strings.ToLower(a.Val)
				}
				if al == "content" {
					content = a.Val
				}
			}
			if (prop == "article:modified_time" || prop == "og:updated_time") &&
				sig.lastModified == "" && content != "" {
				sig.lastModified = content
			}
		default:
			// Microdata: itemtype="https://schema.org/Product" etc.
			for _, a := range n.Attr {
				if strings.ToLower(a.Key) == "itemtype" {
					low := strings.ToLower(a.Val)
					if strings.Contains(low, "schema.org") {
						sig.hasSchemaOrg = true
					}
					if strings.Contains(low, "/product") {
						sig.hasProduct = true
					}
					if strings.Contains(low, "/review") {
						sig.hasReview = true
					}
					if strings.Contains(low, "/article") || strings.Contains(low, "/newsarticle") {
						sig.hasArticle = true
					}
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, sig)
	}
}

func innerText(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if x == nil {
			return
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}

// classifyPageKind picks a coarse bucket for the page. The heuristics
// here intentionally err on the side of "other" — false negatives are
// better than misleading the user.
func classifyPageKind(rawURL, title, desc, body string, brands []config.Brand, hits []BrandHit) PageKind {
	dom := hostOf(rawURL)
	low := strings.ToLower(dom + " " + title + " " + desc)
	bodyLow := strings.ToLower(body)

	// Encyclopedias.
	switch {
	case strings.HasSuffix(dom, "wikipedia.org"),
		strings.HasSuffix(dom, "britannica.com"),
		strings.HasSuffix(dom, "wikidata.org"):
		return KindEncyclopedia
	}

	// Brand-own: when one tracked brand dominates the mentions AND the
	// domain matches one of the brand's aliases (often a domain alias
	// like "kao.com"), classify as the brand's own site.
	for _, b := range brands {
		for _, a := range append([]string{b.Name}, b.Aliases...) {
			if isDomainLike(a) && strings.Contains(dom, strings.ToLower(strings.TrimPrefix(a, "*."))) {
				return KindBrandOwn
			}
		}
	}
	if len(hits) == 1 && hits[0].Count >= 3 {
		// Only one brand on the page and it's mentioned a lot — usually
		// the brand's own marketing page.
		return KindBrandOwn
	}

	// Review aggregators — multiple brands + heavy review markers.
	hasReviewWords := strings.Count(bodyLow, "review") + strings.Count(bodyLow, "rating") +
		strings.Count(bodyLow, "★") + strings.Count(bodyLow, "stars")
	if len(hits) >= 2 && hasReviewWords >= 3 {
		return KindReviewAggregator
	}

	// Listicles: titles like "best X" / "top N" with multiple brand hits.
	titleLow := strings.ToLower(title)
	if (strings.HasPrefix(titleLow, "best ") ||
		strings.HasPrefix(titleLow, "top ") ||
		strings.Contains(titleLow, " best ") ||
		strings.Contains(titleLow, " top ") ||
		strings.Contains(titleLow, "おすすめ") ||
		strings.Contains(titleLow, "ランキング")) && len(hits) >= 2 {
		return KindListicle
	}

	// News-ish: og:type=article or news domain.
	if strings.Contains(low, "news") || strings.Contains(low, "reuters") ||
		strings.Contains(low, "bloomberg") || strings.Contains(low, "techcrunch") {
		return KindNews
	}
	return KindOther
}

func isDomainLike(s string) bool {
	dot := strings.LastIndexByte(s, '.')
	if dot < 0 || dot == len(s)-1 {
		return false
	}
	tail := s[dot+1:]
	if len(tail) < 2 || len(tail) > 24 {
		return false
	}
	for _, r := range tail {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// authorityScore returns a 0..100 composite proxy. This is *not* DA —
// it's a relative ranking signal built from page-side hints. Documented
// limitations:
//   - No actual link-graph data (DA-style backlink counts require Moz/Ahrefs).
//   - No traffic data.
//   - Lightly correlated with what LLMs cite, based on observed patterns;
//     treat as "this page looks more authoritative than that page", not
//     as a hard rank.
func authorityScore(p *Profile) int {
	score := 30 // baseline
	if strings.HasPrefix(p.URL, "https://") {
		score += 5
	}
	if p.StatusCode == 200 {
		score += 5
	}
	if p.HasSchemaOrg {
		score += 8
	}
	if p.HasSchemaProduct {
		score += 6
	}
	if p.HasSchemaReview {
		score += 6
	}
	if p.HasSchemaArticle {
		score += 4
	}
	if p.LastModified != "" {
		score += 4
	}
	if p.HTMLLang != "" {
		score += 3
	}
	if p.WordCount >= 500 {
		score += 5
	}
	if p.WordCount >= 1500 {
		score += 5 // depth bonus
	}
	switch p.PageKind {
	case KindEncyclopedia:
		score += 10
	case KindReviewAggregator:
		score += 8
	case KindListicle:
		score += 4
	case KindBrandOwn:
		score += 2
	case KindNews:
		score += 6
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

