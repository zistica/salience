package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/salience-cli/salience/internal/cron"
	"github.com/salience-cli/salience/internal/scraper"
	"github.com/salience-cli/salience/internal/store"
)

// tickerInterval is how often the background ticker wakes up. Each wake
// scans all schedules and watchers; only those that are due actually fire,
// so we can afford to wake fairly often without burning resources.
const tickerInterval = 30 * time.Second

// StartBackground starts the schedule + watcher tickers. They run for the
// lifetime of ctx and shut down cleanly when it's cancelled. Safe to call
// at most once per Server.
func (s *Server) StartBackground(ctx context.Context) {
	go s.tickerLoop(ctx)
}

func (s *Server) tickerLoop(ctx context.Context) {
	// Fire once on startup so a freshly-due schedule doesn't wait a full
	// tickerInterval to be noticed.
	s.tickOnce(ctx)
	t := time.NewTicker(tickerInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tickOnce(ctx)
		}
	}
}

func (s *Server) tickOnce(ctx context.Context) {
	s.fireDueSchedules(ctx)
	s.fetchDueWatchers(ctx)
}

// ---------- schedules ----------

func (s *Server) fireDueSchedules(ctx context.Context) {
	all, err := s.st.ListSchedules(ctx, 0)
	if err != nil {
		log.Printf("ticker: list schedules: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, sched := range all {
		if !sched.Enabled {
			continue
		}
		if sched.NextFires.After(now) {
			continue
		}
		s.fireSchedule(ctx, sched, now)
	}
}

func (s *Server) fireSchedule(ctx context.Context, sched store.Schedule, now time.Time) {
	proj, err := s.st.GetProject(ctx, sched.ProjectID)
	if err != nil {
		log.Printf("ticker: schedule #%d: project lookup: %v", sched.ID, err)
		return
	}
	cfg, err := projectToConfig(proj)
	if err != nil {
		log.Printf("ticker: schedule #%d: bad project config: %v", sched.ID, err)
		return
	}

	job, jobCtx := s.jm.Start(JobBench, proj.ID)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				s.jm.Finish(job.ID, JobErrored, fmt.Sprintf("scheduled bench panic: %v", rec))
			}
		}()
		// Scheduled benches use no extra cost cap — operator is presumed to
		// have set sensible knobs on the project. They can be aborted via
		// the jobs API if needed.
		runBenchJob(jobCtx, s, job, proj, cfg, 0, false)
	}()

	// Recompute next fire time from the cron expression. If parsing fails,
	// disable the schedule rather than spam.
	parsed, err := cron.Parse(sched.CronExpr)
	var next time.Time
	if err != nil {
		log.Printf("ticker: schedule #%d has invalid cron %q, disabling: %v",
			sched.ID, sched.CronExpr, err)
		next = now.Add(100 * 365 * 24 * time.Hour) // effectively "never"
	} else {
		next = parsed.Next(now)
	}
	if err := s.st.UpdateScheduleFired(ctx, sched.ID, now, next); err != nil {
		log.Printf("ticker: schedule #%d: update fired: %v", sched.ID, err)
	}
}

// ---------- watchers ----------

func (s *Server) fetchDueWatchers(ctx context.Context) {
	all, err := s.st.ListWatchers(ctx, 0)
	if err != nil {
		log.Printf("ticker: list watchers: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, w := range all {
		if !w.Enabled {
			continue
		}
		if w.LastFetchedAt != nil {
			elapsed := now.Sub(*w.LastFetchedAt)
			if elapsed < time.Duration(w.IntervalSeconds)*time.Second {
				continue
			}
		}
		s.fetchWatcher(ctx, w)
	}
}

func (s *Server) fetchWatcher(ctx context.Context, w store.Watcher) {
	c := scraper.NewClient()
	page := c.Fetch(ctx, w.URL)
	hash := contentHash(page.Title, page.Body)

	// Brand / competitor presence is computed against the *project's*
	// brand list so a watcher tied to a CRM project doesn't accidentally
	// flag the wrong names.
	proj, _ := s.st.GetProject(ctx, w.ProjectID)
	brandPresent, compCount := false, 0
	if proj != nil {
		brand, comps := projectBrandNames(proj)
		hay := strings.ToLower(page.Title + " " + page.Body)
		if brand != "" && strings.Contains(hay, strings.ToLower(brand)) {
			brandPresent = true
		}
		for _, c := range comps {
			if strings.Contains(hay, strings.ToLower(c)) {
				compCount++
			}
		}
	}

	body := page.Body
	if len(body) > 4000 {
		body = body[:4000]
	}
	if _, err := s.st.InsertWatcherSnapshot(ctx, store.WatcherSnapshot{
		WatcherID:          w.ID,
		Title:              page.Title,
		Body:               body,
		ContentHash:        hash,
		BrandPresent:       brandPresent,
		CompetitorsPresent: compCount,
	}); err != nil {
		log.Printf("ticker: watcher #%d snapshot: %v", w.ID, err)
		return
	}
	if err := s.st.UpdateWatcherFetched(ctx, w.ID, hash); err != nil {
		log.Printf("ticker: watcher #%d update last_fetched: %v", w.ID, err)
	}
}

// ---------- helpers ----------

func contentHash(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0x1f})
		}
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// projectBrandNames pulls just the canonical brand + competitor names from
// a project's stored JSON. Lighter than projectToConfig since we don't need
// the rest of the config.
func projectBrandNames(p *store.Project) (string, []string) {
	cfg, err := projectToConfig(p)
	if err != nil {
		return "", nil
	}
	out := make([]string, 0, len(cfg.Competitors))
	for _, c := range cfg.Competitors {
		out = append(out, c.Name)
	}
	return cfg.Brand.Name, out
}
