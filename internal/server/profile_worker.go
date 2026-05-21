package server

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/profiler"
	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/store"
)

// profileTTL is how long a cached source_profiles row is considered
// fresh. URLs older than this get re-profiled the next time anything
// asks about them. 24h is a sensible default: the LLM corpus changes
// daily for trending pages, slower for evergreen ones — once-a-day is
// the worst case we want to lag.
const profileTTL = 24 * time.Hour

// profileBacklog is the per-tick cap on how many URLs we'll profile in
// one ticker pass. Keeps a freshly-started server from hammering 500
// external hosts at once. Backlog drains across subsequent ticks.
const profileBacklog = 12

// runProfileWorker is invoked from the main ticker loop. It walks
// recently-completed runs, collects cited URLs that have no fresh
// profile, and runs profiler.Profile against them in parallel. Failures
// are recorded on the row (status_code=0, err=...) so the dashboard can
// still display the URL with a helpful message.
//
// The worker is intentionally idempotent and resumable: only URLs whose
// cached row is missing or older than profileTTL get re-fetched. Killing
// and restarting the server picks up exactly where it left off.
func (s *Server) runProfileWorker(ctx context.Context) {
	// Look at the last few runs only; older runs are unlikely to need
	// profile data fresh.
	runs, err := s.st.ListRuns(ctx, 5)
	if err != nil {
		log.Printf("profile worker: list runs: %v", err)
		return
	}
	if len(runs) == 0 {
		return
	}

	// Build the candidate URL set across the recent runs, deduped.
	// We also need each run's brand list to feed the detector — brand
	// hits are per-brand, so the profile we cache for a URL is keyed
	// against the brands that were live when we crawled it.
	type job struct {
		url    string
		brands []config.Brand
	}
	seen := make(map[string]bool)
	var queue []job

	for _, run := range runs {
		userName, comps, err := report.LoadBrandsFromConfigJSON(ctx, run.ConfigJSON)
		if err != nil {
			log.Printf("profile worker: run #%d brands: %v", run.ID, err)
			continue
		}
		// Include the user brand in the detector input — we want to know
		// whether each cited page mentions the *user* too, not just the
		// competitors. That's how we tell the user "you aren't on this
		// cited page".
		brands := append([]config.Brand{{Name: userName}}, comps...)
		sources, err := s.st.SourcesJSONByRun(ctx, run.ID)
		if err != nil {
			log.Printf("profile worker: run #%d sources: %v", run.ID, err)
			continue
		}
		for _, raw := range sources {
			urls := extractSourceURLs(raw)
			for _, u := range urls {
				if seen[u] {
					continue
				}
				seen[u] = true
				queue = append(queue, job{url: u, brands: brands})
				if len(queue) >= profileBacklog*4 {
					break // hard upper bound on candidate set per tick
				}
			}
			if len(queue) >= profileBacklog*4 {
				break
			}
		}
		if len(queue) >= profileBacklog*4 {
			break
		}
	}

	// Filter to only those that need refreshing.
	urls := make([]string, 0, len(queue))
	for _, j := range queue {
		urls = append(urls, j.url)
	}
	existing, err := s.st.GetSourceProfiles(ctx, urls)
	if err != nil {
		log.Printf("profile worker: lookup cached: %v", err)
		return
	}
	fresh := time.Now().UTC().Add(-profileTTL)
	var todo []job
	for _, j := range queue {
		if row, ok := existing[j.url]; ok && row.FetchedAt.After(fresh) && row.Err == "" {
			continue
		}
		todo = append(todo, j)
		if len(todo) >= profileBacklog {
			break
		}
	}
	if len(todo) == 0 {
		return
	}

	cl := profiler.New()
	log.Printf("profile worker: %d new URL(s) this tick", len(todo))
	for _, j := range todo {
		p := cl.Profile(ctx, j.url, j.brands)
		bh, _ := p.BrandHitsJSON()
		row := store.ProfileRow{
			URL:              p.URL,
			Domain:           p.Domain,
			FetchedAt:        p.FetchedAt,
			StatusCode:       p.StatusCode,
			HTMLLang:         p.HTMLLang,
			Title:            p.Title,
			Description:      p.Description,
			WordCount:        p.WordCount,
			HasSchemaProduct: p.HasSchemaProduct,
			HasSchemaReview:  p.HasSchemaReview,
			HasSchemaArticle: p.HasSchemaArticle,
			HasSchemaOrg:     p.HasSchemaOrg,
			LastModified:     p.LastModified,
			AuthorityScore:   p.AuthorityScore,
			PageKind:         string(p.PageKind),
			BrandHitsJSON:    bh,
			Err:              p.Err,
		}
		if _, err := s.st.UpsertSourceProfile(ctx, row); err != nil {
			log.Printf("profile worker: upsert %s: %v", p.URL, err)
		}
	}
}

// extractSourceURLs pulls cited URLs out of the sources_json column.
// The column stores the same []detect.Source the providers returned,
// so a plain JSON unmarshal does the job.
func extractSourceURLs(raw string) []string {
	if raw == "" {
		return nil
	}
	var ss []detect.Source
	if err := json.Unmarshal([]byte(raw), &ss); err != nil {
		return nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s.URL != "" {
			out = append(out, s.URL)
		}
	}
	return out
}
