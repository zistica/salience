// Package server is the local dashboard. It serves a single-page UI from
// embedded assets, exposes a small JSON API over the existing salience.db,
// and pushes live updates over Server-Sent Events while a bench is running
// in another terminal.
//
// No external dependencies. No node_modules, no Docker. Everything ships in
// the salience binary.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/report"
	"github.com/salience-cli/salience/internal/sources"
	"github.com/salience-cli/salience/internal/store"
)

//go:embed ui/*
var uiFS embed.FS

// Server holds the HTTP dashboard plus the in-process job manager that
// kicks off bench / explain / advise from the UI.
type Server struct {
	st     *store.Store
	dbPath string
	jm     *JobManager
}

// New constructs a server reading from dbPath.
func New(st *store.Store, dbPath string) *Server {
	srv := &Server{st: st, dbPath: dbPath, jm: NewJobManager()}
	// Best-effort: if there are no projects yet but a salience.json exists,
	// import it as project #1 so the dashboard starts non-empty.
	srv.importLegacyConfig(context.Background())
	return srv
}

// Handler returns the http.Handler that should be served.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// runs (legacy, unscoped)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/runs/", s.handleRunNested)
	mux.HandleFunc("/api/trend", s.handleTrend)
	// SSE: combined feed of progress events from runs *and* jobs.
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/playbook", s.handlePlaybook)
	// projects + job triggers
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProjectByID)
	mux.HandleFunc("/api/jobs", s.handleJobsList)
	mux.HandleFunc("/api/jobs/", s.handleJobsCancel)
	// v0.2 surfaces — read-only listings of derived data.
	mux.HandleFunc("/api/scraped", s.handleListScrapedPages)
	mux.HandleFunc("/api/actions", s.handleListActions)
	mux.HandleFunc("/api/briefs", s.handleListBriefs)
	mux.HandleFunc("/api/suggestions", s.handleListSuggestions)
	mux.HandleFunc("/api/schedules", s.handleListSchedules)
	mux.HandleFunc("/api/watchers", s.handleListWatchers)
	mux.HandleFunc("/api/simulations", s.handleListSimulations)
	mux.Handle("/", s.uiHandler())
	return mux
}

// handleJobsList returns the in-memory list of recent jobs.
func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.jm.List())
}

// handleJobsCancel cancels a job by id.
func (s *Server) handleJobsCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE required", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	if !s.jm.Cancel(id) {
		http.Error(w, "job not found or not running", 404)
		return
	}
	w.WriteHeader(204)
}

// uiHandler serves the embedded SPA. Unknown paths fall back to index.html so
// a future hash-based router doesn't get 404s.
func (s *Server) uiHandler() http.Handler {
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// embed.FS doesn't actually fail here at runtime, but keep the
		// branch so unit tests for new go-embed bugs would surface.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "embedded UI missing: "+err.Error(), 500)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		f, err := sub.Open(path)
		if err != nil {
			// SPA fallback.
			f, err = sub.Open("index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		defer f.Close()
		// Tiny content-type sniff.
		switch {
		case strings.HasSuffix(path, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case strings.HasSuffix(path, ".js"):
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case strings.HasSuffix(path, ".svg"):
			w.Header().Set("Content-Type", "image/svg+xml")
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		_, _ = copyTo(w, f)
	})
}

func copyTo(w http.ResponseWriter, src interface{ Read(p []byte) (int, error) }) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return total, nil
			}
			return total, err
		}
	}
}

// ---- API handlers ----

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runs, err := s.st.ListRuns(ctx, 0)
	if err != nil {
		httpErr(w, err)
		return
	}
	type runSummary struct {
		ID         int64   `json:"id"`
		StartedAt  string  `json:"started_at"`
		FinishedAt string  `json:"finished_at,omitempty"`
		BrandName  string  `json:"brand_name"`
		Status     string  `json:"status"`
		Ok         int     `json:"ok"`
		Errored    int     `json:"errored"`
		Cost       float64 `json:"cost"`
	}
	out := make([]runSummary, 0, len(runs))
	for _, run := range runs {
		ok, errored, _ := s.st.CountSamples(ctx, run.ID)
		// Sum cost across samples.
		samples, _ := s.st.ListSamples(ctx, run.ID)
		cost := 0.0
		for _, sm := range samples {
			cost += sm.CostUSD
		}
		summ := runSummary{
			ID: run.ID, StartedAt: run.StartedAt.Format(time.RFC3339),
			BrandName: run.BrandName, Status: run.Status,
			Ok: ok, Errored: errored, Cost: cost,
		}
		if run.FinishedAt != nil {
			summ.FinishedAt = run.FinishedAt.Format(time.RFC3339)
		}
		out = append(out, summ)
	}
	writeJSON(w, out)
}

// handleRunNested routes /api/runs/{id} and /api/runs/{id}/<sub>.
func (s *Server) handleRunNested(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "bad run id", 400)
		return
	}
	ctx := r.Context()
	if len(parts) == 1 {
		s.serveRunReport(w, ctx, id)
		return
	}
	switch parts[1] {
	case "mentions":
		s.serveRunMentions(w, ctx, id)
	case "sources":
		s.serveRunSources(w, ctx, id)
	case "explanations":
		s.serveRunExplanations(w, ctx, id)
	case "advice":
		s.serveRunAdvice(w, ctx, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveRunReport(w http.ResponseWriter, ctx context.Context, id int64) {
	meta, err := s.st.GetRun(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	user, comps, err := report.LoadCompetitorsFromConfigJSON(ctx, meta.ConfigJSON)
	if err != nil {
		httpErr(w, err)
		return
	}
	samples, err := s.st.ListSamples(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	data := report.Build(id, meta, samples, user, comps)
	writeJSON(w, data)
}

func (s *Server) serveRunMentions(w http.ResponseWriter, ctx context.Context, id int64) {
	ms, err := s.st.ListMentionsForRun(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, ms)
}

func (s *Server) serveRunSources(w http.ResponseWriter, ctx context.Context, id int64) {
	meta, err := s.st.GetRun(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	var cfg config.Config
	if err := json.Unmarshal([]byte(meta.ConfigJSON), &cfg); err != nil {
		httpErr(w, err)
		return
	}
	samples, err := s.st.ListSamples(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	srcJSON, err := s.st.SourcesJSONByRun(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	decoded, _ := sources.DecodeSamples(samples, srcJSON)
	competitors := make([]string, 0, len(cfg.Competitors))
	vendorAliases := map[string][]string{cfg.Brand.Name: cfg.Brand.Aliases}
	for _, c := range cfg.Competitors {
		competitors = append(competitors, c.Name)
		vendorAliases[c.Name] = c.Aliases
	}
	rep := sources.AnalyzeSamples(decoded, cfg.Brand.Name, competitors, vendorAliases)
	rep.RunID = id
	writeJSON(w, rep)
}

func (s *Server) serveRunExplanations(w http.ResponseWriter, ctx context.Context, id int64) {
	es, err := s.st.ListExplanations(ctx, id, "")
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, es)
}

func (s *Server) serveRunAdvice(w http.ResponseWriter, ctx context.Context, id int64) {
	as, err := s.st.ListAdvice(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	writeJSON(w, as)
}

func (s *Server) handleTrend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runs, err := s.st.ListRuns(ctx, 30) // last 30 runs
	if err != nil {
		httpErr(w, err)
		return
	}
	// Reverse to chronological order.
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	type point struct {
		RunID     int64              `json:"run_id"`
		StartedAt string             `json:"started_at"`
		Status    string             `json:"status"`
		Rates     map[string]float64 `json:"rates"`
	}
	var out []point
	for _, run := range runs {
		user, comps, err := report.LoadCompetitorsFromConfigJSON(ctx, run.ConfigJSON)
		if err != nil {
			continue
		}
		samples, err := s.st.ListSamples(ctx, run.ID)
		if err != nil {
			continue
		}
		runMeta := run
		data := report.Build(run.ID, &runMeta, samples, user, comps)
		out = append(out, point{
			RunID:     run.ID,
			StartedAt: run.StartedAt.Format(time.RFC3339),
			Status:    run.Status,
			Rates:     data.Totals.Rates,
		})
	}
	writeJSON(w, out)
}

func (s *Server) handlePlaybook(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("run")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id == 0 {
		http.Error(w, "?run=N required", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// For v1, surface the same Playbook struct as the CLI would build, but
	// the CLI path uses os.Stdout — so we lift the data path. Keep it
	// simple: send 501 here and surface the playbook command instead.
	http.Error(w, "playbook over HTTP not implemented yet — run `salience playbook -run "+strconv.FormatInt(id, 10)+"` and `cat` the file", 501)
}

// ---- SSE ----

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ctx := r.Context()

	// Hook into the job manager too — every UI-launched job will push
	// events down this same stream.
	jobCh, jobUnsub := s.jm.Subscribe()
	defer jobUnsub()
	go func() {
		for ev := range jobCh {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: job\ndata: %s\n\n", b)
			flusher.Flush()
		}
	}()

	// We poll the latest run's sample count every 1s and only push when it
	// changes. Cheap, no extra schema, works alongside `salience bench`.
	var lastRun int64
	var lastSamples int
	send := func(typ string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
		flusher.Flush()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
		}
		latest, err := s.st.LatestRunID(ctx)
		if err != nil || latest == 0 {
			continue
		}
		if latest != lastRun {
			lastRun = latest
			lastSamples = 0
			meta, _ := s.st.GetRun(ctx, latest)
			send("run-start", map[string]any{
				"run_id":     latest,
				"brand":      meta.BrandName,
				"started_at": meta.StartedAt.Format(time.RFC3339),
			})
		}
		ok, errored, _ := s.st.CountSamples(ctx, latest)
		total := ok + errored
		if total != lastSamples {
			lastSamples = total
			send("progress", map[string]any{
				"run_id":  latest,
				"ok":      ok,
				"errored": errored,
			})
		}
		meta, _ := s.st.GetRun(ctx, latest)
		if meta != nil && meta.Status != "running" {
			send("run-end", map[string]any{
				"run_id": latest,
				"status": meta.Status,
			})
		}
	}
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), 500)
}

// Used by callers that want to share the same Server instance from multiple
// goroutines; kept here for future expansion.
var _ sync.Mutex
