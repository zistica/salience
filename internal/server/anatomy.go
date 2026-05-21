package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/provider"
	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/store"
)

// ---- /api/runs/:id/anatomy/:sampleId ----

// SampleAnatomy is what the Anatomy view in the dashboard renders. It's
// a flat record bundling everything the user needs to understand one
// sample's chain: the prompt, the tool calls, the cited sources (with
// the *page-side* analysis we cached), the raw answer, and a list of
// concrete actions.
type SampleAnatomy struct {
	RunID         int64               `json:"run_id"`
	SampleID      int64               `json:"sample_id"`
	Prompt        string              `json:"prompt"`
	PromptSent    string              `json:"prompt_sent"`     // prompt with the region prefix applied
	RegionCode    string              `json:"region"`
	RegionLabel   string              `json:"region_label"`
	RegionPrefix  string              `json:"region_prefix,omitempty"`
	ProviderName  string              `json:"provider_name"`
	ProviderKind  string              `json:"provider_kind"`
	Model         string              `json:"model"`
	UserBrand     string              `json:"user_brand"`
	ResponseText  string              `json:"response_text"`
	ToolCalls     []provider.ToolCall `json:"tool_calls"`
	Sources       []SourceAnatomy     `json:"sources"`
	Diagnoses     []Diagnosis         `json:"diagnoses"`
	CreatedAt     time.Time           `json:"created_at"`
}

// SourceAnatomy is one cited URL hydrated with its cached profile data.
// When the profile is missing (worker hasn't crawled it yet) we still
// return the URL with Pending: true, so the UI can show a placeholder.
type SourceAnatomy struct {
	URL              string      `json:"url"`
	Domain           string      `json:"domain"`
	Title            string      `json:"title,omitempty"`
	Description      string      `json:"description,omitempty"`
	PageKind         string      `json:"page_kind,omitempty"`
	HTMLLang         string      `json:"html_lang,omitempty"`
	WordCount        int         `json:"word_count,omitempty"`
	HasSchemaProduct bool        `json:"has_schema_product,omitempty"`
	HasSchemaReview  bool        `json:"has_schema_review,omitempty"`
	HasSchemaArticle bool        `json:"has_schema_article,omitempty"`
	LastModified     string      `json:"last_modified,omitempty"`
	AuthorityScore   int         `json:"authority_score,omitempty"`
	StatusCode       int         `json:"status_code,omitempty"`
	BrandHits        []BrandHitV `json:"brand_hits,omitempty"`
	UserPresent      bool        `json:"user_present"`     // shortcut: was the user brand mentioned on this page?
	FetchedAt        time.Time   `json:"fetched_at,omitempty"`
	Pending          bool        `json:"pending,omitempty"` // profile not crawled yet
	Err              string      `json:"err,omitempty"`
}

// BrandHitV is the view-side shape for one brand's appearance on a
// cited page. Mirrors profiler.BrandHit but JSON-tagged for the UI.
type BrandHitV struct {
	Brand    string   `json:"brand"`
	Count    int      `json:"count"`
	Positive int      `json:"positive"`
	Neutral  int      `json:"neutral"`
	Negative int      `json:"negative"`
	Snippets []string `json:"snippets,omitempty"`
	IsUser   bool     `json:"is_user,omitempty"`
}

// Diagnosis is one human-readable explanation of why this answer
// landed the way it did, paired with the concrete action to take.
// The rules engine that produces these lives in diagnose.go.
type Diagnosis struct {
	Severity string `json:"severity"` // "info" | "warn" | "critical"
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Action   string `json:"action,omitempty"`
}

func (s *Server) serveSampleAnatomy(w http.ResponseWriter, ctx context.Context, runID, sampleID int64) {
	meta, err := s.st.GetRun(ctx, runID)
	if err != nil {
		httpErr(w, err)
		return
	}
	user, comps, err := report.LoadBrandsFromConfigJSON(ctx, meta.ConfigJSON)
	if err != nil {
		httpErr(w, err)
		return
	}

	// Pull a single sample by id — keep the query inline (one-shot,
	// avoids ballooning the Store API). We also need region info to
	// reconstruct what was actually sent to the LLM.
	row := s.st.DB().QueryRowContext(ctx, `
		SELECT id, run_id, prompt, provider_name, provider_kind, model, region,
		       created_at, response_text, sources_json, tool_calls_json
		FROM samples WHERE id = ? AND run_id = ?`, sampleID, runID)

	var (
		got        SampleAnatomy
		regionCode string
		createdAt  string
		srcJSON    string
		toolJSON   string
	)
	if err := row.Scan(&got.SampleID, &got.RunID, &got.Prompt,
		&got.ProviderName, &got.ProviderKind, &got.Model, &regionCode,
		&createdAt, &got.ResponseText, &srcJSON, &toolJSON); err != nil {
		http.Error(w, "sample not found", 404)
		return
	}
	if t, perr := time.Parse(time.RFC3339Nano, createdAt); perr == nil {
		got.CreatedAt = t
	}
	got.RegionCode = regionCode
	got.UserBrand = user

	// Resolve the region prefix from the project config so we can show
	// the *exact* string we sent to the LLM, not just the original
	// prompt. This makes the "what got sent" line transparent.
	var cfg config.Config
	if err := json.Unmarshal([]byte(meta.ConfigJSON), &cfg); err == nil {
		for _, r := range cfg.Regions {
			if r.Code == regionCode {
				got.RegionLabel = r.Label
				got.RegionPrefix = r.Prefix
				break
			}
		}
	}
	if got.RegionLabel == "" {
		got.RegionLabel = regionCode
	}
	got.PromptSent = got.Prompt
	if got.RegionPrefix != "" {
		got.PromptSent = got.RegionPrefix + " " + got.Prompt
	}

	// Tool calls.
	if toolJSON != "" {
		_ = json.Unmarshal([]byte(toolJSON), &got.ToolCalls)
	}

	// Hydrate sources with cached profile data. Profiles missing from
	// the cache (the worker hasn't reached them yet) come back as
	// Pending placeholders.
	var sources []detect.Source
	if srcJSON != "" {
		_ = json.Unmarshal([]byte(srcJSON), &sources)
	}
	urls := make([]string, 0, len(sources))
	titleByURL := make(map[string]string, len(sources))
	for _, src := range sources {
		if src.URL == "" {
			continue
		}
		urls = append(urls, src.URL)
		if titleByURL[src.URL] == "" {
			titleByURL[src.URL] = src.Title
		}
	}
	profiles, err := s.st.GetSourceProfiles(ctx, urls)
	if err != nil {
		httpErr(w, err)
		return
	}
	seen := make(map[string]bool)
	for _, src := range sources {
		if src.URL == "" || seen[src.URL] {
			continue
		}
		seen[src.URL] = true
		sa := SourceAnatomy{URL: src.URL, Domain: hostOf(src.URL), Title: src.Title}
		if p, ok := profiles[src.URL]; ok {
			sa.Title = firstNonEmpty(p.Title, sa.Title)
			sa.Description = p.Description
			sa.PageKind = p.PageKind
			sa.HTMLLang = p.HTMLLang
			sa.WordCount = p.WordCount
			sa.HasSchemaProduct = p.HasSchemaProduct
			sa.HasSchemaReview = p.HasSchemaReview
			sa.HasSchemaArticle = p.HasSchemaArticle
			sa.LastModified = p.LastModified
			sa.AuthorityScore = p.AuthorityScore
			sa.StatusCode = p.StatusCode
			sa.FetchedAt = p.FetchedAt
			sa.Err = p.Err
			for _, h := range decodeBrandHits(p.BrandHitsJSON) {
				isUser := h.Brand == user
				if isUser {
					sa.UserPresent = true
				}
				sa.BrandHits = append(sa.BrandHits, BrandHitV{
					Brand: h.Brand, Count: h.Count,
					Positive: h.Positive, Neutral: h.Neutral, Negative: h.Negative,
					Snippets: h.Snippets, IsUser: isUser,
				})
			}
			sort.Slice(sa.BrandHits, func(i, j int) bool {
				return sa.BrandHits[i].Count > sa.BrandHits[j].Count
			})
		} else {
			sa.Pending = true
		}
		got.Sources = append(got.Sources, sa)
	}

	// Diagnoses come from the rules engine.
	got.Diagnoses = diagnose(&got, user, comps)
	writeJSON(w, got)
}

// ---- /api/runs/:id/domains ----

// DomainStat is one row in the Influence tab's "Domain Hit List" — for
// the whole run, which domains the LLMs cite most and the user's
// position on each.
type DomainStat struct {
	Domain           string   `json:"domain"`
	Citations        int      `json:"citations"`
	PageKind         string   `json:"page_kind,omitempty"`
	AuthorityScore   int      `json:"authority_score,omitempty"`
	UserPresent      bool     `json:"user_present"`
	CompetitorCount  int      `json:"competitor_count"`
	CompetitorNames  []string `json:"competitor_names,omitempty"`
	SampleURLs       []string `json:"sample_urls,omitempty"`
}

func (s *Server) serveRunDomains(w http.ResponseWriter, ctx context.Context, runID int64) {
	meta, err := s.st.GetRun(ctx, runID)
	if err != nil {
		httpErr(w, err)
		return
	}
	user, _, err := report.LoadBrandsFromConfigJSON(ctx, meta.ConfigJSON)
	if err != nil {
		httpErr(w, err)
		return
	}

	// Pull every cited URL from this run's samples.
	srcByID, err := s.st.SourcesJSONByRun(ctx, runID)
	if err != nil {
		httpErr(w, err)
		return
	}
	// citationCount counts a domain once per sample (not per URL on the
	// domain) — so a sample that cites two pages on cosme.net still
	// counts as one cosme.net citation. This matches what users intuit.
	type bucket struct {
		domain string
		urls   map[string]bool // dedupe URLs we've seen
		count  int
	}
	domainMap := make(map[string]*bucket)
	for _, raw := range srcByID {
		urls := extractSourceURLs(raw)
		seenDomain := make(map[string]bool)
		for _, u := range urls {
			d := hostOf(u)
			if d == "" {
				continue
			}
			b, ok := domainMap[d]
			if !ok {
				b = &bucket{domain: d, urls: make(map[string]bool)}
				domainMap[d] = b
			}
			b.urls[u] = true
			if !seenDomain[d] {
				b.count++
				seenDomain[d] = true
			}
		}
	}
	if len(domainMap) == 0 {
		writeJSON(w, []DomainStat{})
		return
	}

	// Hydrate each domain's stat with its profile data (any of the URLs
	// on that domain is good enough for page_kind + authority signals).
	allURLs := make([]string, 0)
	for _, b := range domainMap {
		for u := range b.urls {
			allURLs = append(allURLs, u)
		}
	}
	profiles, err := s.st.GetSourceProfiles(ctx, allURLs)
	if err != nil {
		httpErr(w, err)
		return
	}

	out := make([]DomainStat, 0, len(domainMap))
	for _, b := range domainMap {
		ds := DomainStat{Domain: b.domain, Citations: b.count}
		// Pick a representative profile — prefer the one with the
		// highest authority score. Aggregate competitor names across
		// all URLs on this domain.
		comps := make(map[string]bool)
		bestScore := -1
		for u := range b.urls {
			if p, ok := profiles[u]; ok {
				if p.AuthorityScore > bestScore {
					bestScore = p.AuthorityScore
					ds.PageKind = p.PageKind
					ds.AuthorityScore = p.AuthorityScore
				}
				for _, h := range decodeBrandHits(p.BrandHitsJSON) {
					if h.Brand == user {
						ds.UserPresent = true
					} else {
						comps[h.Brand] = true
					}
				}
			}
			if len(ds.SampleURLs) < 3 {
				ds.SampleURLs = append(ds.SampleURLs, u)
			}
		}
		for c := range comps {
			ds.CompetitorNames = append(ds.CompetitorNames, c)
		}
		sort.Strings(ds.CompetitorNames)
		ds.CompetitorCount = len(ds.CompetitorNames)
		out = append(out, ds)
	}
	// Most-cited first; ties broken by authority score then alpha.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Citations != out[j].Citations {
			return out[i].Citations > out[j].Citations
		}
		if out[i].AuthorityScore != out[j].AuthorityScore {
			return out[i].AuthorityScore > out[j].AuthorityScore
		}
		return out[i].Domain < out[j].Domain
	})
	writeJSON(w, out)
}

// ---- helpers ----

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	h := strings.ToLower(u.Host)
	return strings.TrimPrefix(h, "www.")
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// decodeBrandHits is a tiny shim so server.go doesn't import the
// profiler package just for one decoder. Mirrors profiler.BrandHit
// fields one-for-one (kept in sync by tests).
func decodeBrandHits(s string) []decodedHit {
	if strings.TrimSpace(s) == "" || s == "{}" {
		return nil
	}
	var m map[string]decodedHit
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	out := make([]decodedHit, 0, len(m))
	for k, h := range m {
		if h.Brand == "" {
			h.Brand = k
		}
		out = append(out, h)
	}
	return out
}

type decodedHit struct {
	Brand    string   `json:"brand"`
	Count    int      `json:"count"`
	Positive int      `json:"positive"`
	Neutral  int      `json:"neutral"`
	Negative int      `json:"negative"`
	Snippets []string `json:"snippets,omitempty"`
}

// diagnose builds the action list from rules. Real engine lives in
// diagnose.go; the wrapper here exists so the Anatomy handler doesn't
// have to know which rules ran.
func diagnose(a *SampleAnatomy, user string, competitors []config.Brand) []Diagnosis {
	return runDiagnosisRules(a, user, competitors)
}

// store accessor — Store doesn't expose its *sql.DB by default, so we
// add a tiny helper. Kept in this file so reviewers see exactly where
// raw SQL is reaching past the Store API.
func init() {
	// no-op; the DB() helper is defined in store/store.go's tail (added
	// alongside this feature).
	_ = store.ProfileRow{}
}
