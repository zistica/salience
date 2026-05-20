package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/provider"
	"github.com/salience-cli/salience/internal/runner"
	"github.com/salience-cli/salience/internal/store"
)

// ---------- request/response shapes ----------

// ProjectIn is the JSON body for POST/PUT /api/projects.
type ProjectIn struct {
	Name        string             `json:"name"`
	Brand       config.Brand       `json:"brand"`
	Competitors []config.Brand     `json:"competitors"`
	Prompts     []string           `json:"prompts"`
	Providers   []config.Provider  `json:"providers"`
	SamplesPer  int                `json:"samples_per_prompt"`
	Concurrency int                `json:"concurrency_per_provider"`
	MaxTokens   int                `json:"max_tokens"`
	Notes       string             `json:"notes"`
}

// ProjectOut is the JSON body returned for GET /api/projects.
type ProjectOut struct {
	ID                     int64             `json:"id"`
	Name                   string            `json:"name"`
	Slug                   string            `json:"slug"`
	Brand                  config.Brand      `json:"brand"`
	Competitors            []config.Brand    `json:"competitors"`
	Prompts                []string          `json:"prompts"`
	Providers              []config.Provider `json:"providers"`
	SamplesPerPrompt       int               `json:"samples_per_prompt"`
	ConcurrencyPerProvider int               `json:"concurrency_per_provider"`
	MaxTokens              int               `json:"max_tokens"`
	Notes                  string            `json:"notes"`
	CreatedAt              string            `json:"created_at"`
	UpdatedAt              string            `json:"updated_at"`
}

func projectToOut(p *store.Project) ProjectOut {
	out := ProjectOut{
		ID: p.ID, Name: p.Name, Slug: p.Slug,
		SamplesPerPrompt: p.SamplesPerPrompt,
		ConcurrencyPerProvider: p.ConcurrencyPerProvider,
		MaxTokens: p.MaxTokens, Notes: p.Notes,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
	_ = json.Unmarshal([]byte(p.BrandJSON), &out.Brand)
	_ = json.Unmarshal([]byte(p.CompetitorsJSON), &out.Competitors)
	_ = json.Unmarshal([]byte(p.PromptsJSON), &out.Prompts)
	_ = json.Unmarshal([]byte(p.ProvidersJSON), &out.Providers)
	return out
}

func inToProject(in ProjectIn) (store.Project, error) {
	if strings.TrimSpace(in.Name) == "" {
		return store.Project{}, fmt.Errorf("name is required")
	}
	brand, _ := json.Marshal(in.Brand)
	comps, _ := json.Marshal(in.Competitors)
	prompts, _ := json.Marshal(in.Prompts)
	provs, _ := json.Marshal(in.Providers)
	return store.Project{
		Name:                   in.Name,
		BrandJSON:              string(brand),
		CompetitorsJSON:        string(comps),
		PromptsJSON:            string(prompts),
		ProvidersJSON:          string(provs),
		SamplesPerPrompt:       in.SamplesPer,
		ConcurrencyPerProvider: in.Concurrency,
		MaxTokens:              in.MaxTokens,
		Notes:                  in.Notes,
	}, nil
}

// ---------- handlers ----------

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		projs, err := s.st.ListProjects(ctx)
		if err != nil {
			httpErr(w, err)
			return
		}
		out := make([]ProjectOut, 0, len(projs))
		for i := range projs {
			out = append(out, projectToOut(&projs[i]))
		}
		writeJSON(w, out)
	case http.MethodPost:
		var in ProjectIn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad JSON: "+err.Error(), 400)
			return
		}
		pr, err := inToProject(in)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		id, err := s.st.CreateProject(ctx, pr)
		if err != nil {
			httpErr(w, err)
			return
		}
		got, err := s.st.GetProject(ctx, id)
		if err != nil {
			httpErr(w, err)
			return
		}
		writeJSON(w, projectToOut(got))
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleProjectByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, "bad project id", 400)
		return
	}
	ctx := r.Context()
	if len(parts) == 2 {
		switch parts[1] {
		case "estimate":
			s.handleProjectEstimate(w, ctx, id)
		case "bench":
			s.handleProjectRun(w, r, ctx, id, JobBench)
		case "explain":
			s.handleProjectRun(w, r, ctx, id, JobExplain)
		case "advise":
			s.handleProjectRun(w, r, ctx, id, JobAdvise)
		case "runs":
			s.handleProjectRuns(w, ctx, id)
		default:
			http.NotFound(w, r)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := s.st.GetProject(ctx, id)
		if err != nil {
			httpErr(w, err)
			return
		}
		writeJSON(w, projectToOut(p))
	case http.MethodPut:
		var in ProjectIn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad JSON: "+err.Error(), 400)
			return
		}
		pr, err := inToProject(in)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		pr.ID = id
		if err := s.st.UpdateProject(ctx, pr); err != nil {
			httpErr(w, err)
			return
		}
		got, _ := s.st.GetProject(ctx, id)
		writeJSON(w, projectToOut(got))
	case http.MethodDelete:
		if err := s.st.DeleteProject(ctx, id); err != nil {
			httpErr(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleProjectRuns(w http.ResponseWriter, ctx context.Context, projectID int64) {
	runs, err := s.st.ListRunsForProject(ctx, projectID, 0)
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

// handleProjectEstimate returns a quick cost preview for the project.
func (s *Server) handleProjectEstimate(w http.ResponseWriter, ctx context.Context, id int64) {
	p, err := s.st.GetProject(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	cfg, err := projectToConfig(p)
	if err != nil {
		httpErr(w, err)
		return
	}
	type providerEst struct {
		Name   string  `json:"name"`
		Model  string  `json:"model"`
		Calls  int     `json:"calls"`
		Cost   float64 `json:"cost"`
	}
	out := struct {
		Total     float64       `json:"total"`
		Providers []providerEst `json:"providers"`
	}{}
	promptTokens := 0
	for _, p := range cfg.Prompts {
		promptTokens += len(p)/4 + 1
	}
	for _, pc := range cfg.Providers {
		calls := len(cfg.Prompts) * cfg.SamplesPer
		in := promptTokens * cfg.SamplesPer
		out2 := calls * 350
		cost := pricing.Estimate(pc.Model, in, out2)
		out.Providers = append(out.Providers, providerEst{Name: pc.Name, Model: pc.Model, Calls: calls, Cost: cost})
		out.Total += cost
	}
	writeJSON(w, out)
}

// handleProjectRun is the common entry point for bench/explain/advise. It
// returns immediately with a job id; SSE clients track progress.
func (s *Server) handleProjectRun(w http.ResponseWriter, r *http.Request, ctx context.Context, id int64, kind JobKind) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	p, err := s.st.GetProject(ctx, id)
	if err != nil {
		httpErr(w, err)
		return
	}
	cfg, err := projectToConfig(p)
	if err != nil {
		httpErr(w, err)
		return
	}

	// Optional knobs from query string.
	maxCost, _ := strconv.ParseFloat(r.URL.Query().Get("max_cost"), 64)
	dryRun := r.URL.Query().Get("dry_run") == "1"

	job, jobCtx := s.jm.Start(kind, id)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				s.jm.Finish(job.ID, JobErrored, fmt.Sprintf("panic: %v", rec))
			}
		}()
		switch kind {
		case JobBench:
			runBenchJob(jobCtx, s, job, p, cfg, maxCost, dryRun)
		case JobExplain:
			runExplainJob(jobCtx, s, job, p, cfg)
		case JobAdvise:
			runAdviseJob(jobCtx, s, job, p, cfg)
		}
	}()

	writeJSON(w, map[string]any{"job_id": job.ID, "kind": kind})
}

// ---------- workers (these are the in-process equivalents of the CLI commands) ----------

func runBenchJob(ctx context.Context, s *Server, job *Job, p *store.Project, cfg *config.Config, maxCost float64, dryRun bool) {
	providers, err := buildProvidersFromEnv(cfg)
	if err != nil {
		s.jm.Finish(job.ID, JobErrored, err.Error())
		return
	}
	total := len(cfg.Prompts) * len(providers) * cfg.SamplesPer
	s.jm.Update(job.ID, 0, total, "starting")

	if dryRun {
		s.jm.Finish(job.ID, JobDone, "dry-run: no calls made")
		return
	}

	// Cost-cap check up front using the same estimate formula as /estimate.
	if maxCost > 0 {
		est := 0.0
		promptTokens := 0
		for _, p := range cfg.Prompts {
			promptTokens += len(p)/4 + 1
		}
		for _, pc := range cfg.Providers {
			est += pricing.Estimate(pc.Model, promptTokens*cfg.SamplesPer, len(cfg.Prompts)*cfg.SamplesPer*350)
		}
		if est > maxCost {
			s.jm.Finish(job.ID, JobErrored, fmt.Sprintf("estimated cost $%.4f exceeds cap $%.4f", est, maxCost))
			return
		}
	}

	// Build a runner and tie it to the project. Persist the run, attach
	// project_id, then call the inner loop ourselves so we can stream
	// per-sample progress into the job.
	cfgJSON, _ := json.Marshal(cfg)
	runID, err := s.st.StartRun(ctx, string(cfgJSON), cfg.Brand.Name)
	if err != nil {
		s.jm.Finish(job.ID, JobErrored, err.Error())
		return
	}
	_ = s.st.AttachRunToProject(ctx, runID, p.ID)
	s.jm.SetRunID(job.ID, runID)

	// Use the existing runner.Runner — capture progress via a sink.
	progBuf := &progressBuf{job: job, jm: s.jm, total: total}
	r := &runner.Runner{
		Cfg: cfg, Providers: providers, Store: s.st, Out: progBuf,
	}
	if _, err := r.Run(ctx, runID); err != nil {
		s.jm.Finish(job.ID, JobErrored, err.Error())
		return
	}
	s.jm.Finish(job.ID, JobDone, fmt.Sprintf("run #%d complete", runID))
}

func runExplainJob(ctx context.Context, s *Server, job *Job, p *store.Project, cfg *config.Config) {
	providers, err := buildProvidersFromEnv(cfg)
	if err != nil {
		s.jm.Finish(job.ID, JobErrored, err.Error())
		return
	}
	providerByName := map[string]provider.Provider{}
	for _, pr := range providers {
		providerByName[pr.Name()] = pr
	}

	// Probe the latest run by default.
	runID, _ := s.st.LatestRunID(ctx)
	if runID == 0 {
		s.jm.Finish(job.ID, JobErrored, "no run to explain — start one first")
		return
	}
	s.jm.SetRunID(job.ID, runID)

	samples, err := s.st.ListSamples(ctx, runID)
	if err != nil {
		s.jm.Finish(job.ID, JobErrored, err.Error())
		return
	}

	// Probe at most 3 mentions per competitor brand for speed.
	type probe struct {
		sampleID  int64
		brand     string
		prompt    string
		provider  string
		model     string
	}
	competitorNames := map[string]bool{}
	for _, c := range cfg.Competitors {
		competitorNames[c.Name] = true
	}
	perBrand := map[string]int{}
	var probes []probe
	for _, sm := range samples {
		if sm.Error != "" {
			continue
		}
		for _, b := range sm.BrandsHit {
			if !competitorNames[b] {
				continue
			}
			if perBrand[b] >= 3 {
				continue
			}
			perBrand[b]++
			probes = append(probes, probe{sm.ID, b, sm.Prompt, sm.ProviderName, sm.Model})
		}
	}

	if len(probes) == 0 {
		s.jm.Finish(job.ID, JobDone, "no competitor mentions to explain")
		return
	}
	s.jm.Update(job.ID, 0, len(probes), "probing")

	for i, pr := range probes {
		if ctx.Err() != nil {
			s.jm.Finish(job.ID, JobCanceled, "canceled")
			return
		}
		cl, ok := providerByName[pr.provider]
		if !ok {
			continue
		}
		text := fmt.Sprintf("Earlier, in response to %q, you mentioned %q. Briefly (≤100 words), why? What facts, features, or sources informed that mention?", pr.prompt, pr.brand)
		temp := 0.4
		resp, callErr := cl.Call(ctx, text, 256, &temp)
		rec := store.ExplanationRecord{
			RunID: runID, SourceSampleID: pr.sampleID,
			ProviderName: pr.provider, Model: pr.model,
			AskedAboutBrand: pr.brand, Prompt: pr.prompt,
		}
		if callErr != nil {
			rec.Error = callErr.Error()
		} else {
			rec.Reasoning = resp.Text
			rec.InputTokens = resp.InputTokens
			rec.OutputTokens = resp.OutputTokens
			rec.CostUSD = pricing.Estimate(pr.model, resp.InputTokens, resp.OutputTokens)
		}
		_ = s.st.InsertExplanation(context.Background(), rec)
		s.jm.Update(job.ID, i+1, len(probes), fmt.Sprintf("explained %s", pr.brand))
	}
	s.jm.Finish(job.ID, JobDone, "explain complete")
}

func runAdviseJob(ctx context.Context, s *Server, job *Job, p *store.Project, cfg *config.Config) {
	providers, err := buildProvidersFromEnv(cfg)
	if err != nil {
		s.jm.Finish(job.ID, JobErrored, err.Error())
		return
	}
	runID, _ := s.st.LatestRunID(ctx)
	if runID == 0 {
		s.jm.Finish(job.ID, JobErrored, "no run to advise on")
		return
	}
	s.jm.SetRunID(job.ID, runID)

	// Cheap losing-prompt discovery via the existing rate math.
	// We rebuild via samples.BrandsHit aggregation — same logic as report.
	samples, _ := s.st.ListSamples(ctx, runID)
	type key struct{ prompt, provider string }
	totals := map[key]int{}
	hits := map[key]map[string]int{}
	for _, sm := range samples {
		if sm.Error != "" {
			continue
		}
		k := key{sm.Prompt, sm.ProviderName}
		totals[k]++
		if hits[k] == nil {
			hits[k] = map[string]int{}
		}
		for _, b := range sm.BrandsHit {
			hits[k][b]++
		}
	}
	type losing struct {
		prompt string
		winner string
	}
	var losers []losing
	user := cfg.Brand.Name
	for k, n := range totals {
		userR := float64(hits[k][user]) / float64(n)
		best := 0.0
		winner := ""
		for _, c := range cfg.Competitors {
			r := float64(hits[k][c.Name]) / float64(n)
			if r > best {
				best = r
				winner = c.Name
			}
		}
		if best-userR >= 0.10 {
			losers = append(losers, losing{k.prompt, winner})
		}
	}

	// Dedup by prompt.
	seen := map[string]bool{}
	uniq := losers[:0]
	for _, l := range losers {
		if seen[l.prompt] {
			continue
		}
		seen[l.prompt] = true
		uniq = append(uniq, l)
	}
	losers = uniq

	if len(losers) == 0 {
		s.jm.Finish(job.ID, JobDone, "no losing prompts — nothing to advise on")
		return
	}

	total := len(losers) * len(providers)
	s.jm.Update(job.ID, 0, total, "asking")
	i := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)
	if cfg.Concurrency < 1 {
		sem = make(chan struct{}, 2)
	}

	for _, l := range losers {
		for _, pr := range providers {
			pr := pr
			l := l
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				text := fmt.Sprintf(
					"I represent %q, a competitor in this market. For the question %q, "+
						"you currently appear to recommend %q. Be candid and specific: what verifiable "+
						"things would %q need to demonstrate publicly to earn this recommendation? "+
						"Give a numbered list of concrete, ranked actions.",
					user, l.prompt, l.winner, user)
				temp := 0.5
				resp, err := pr.Call(ctx, text, 800, &temp)
				rec := store.AdviceRecord{
					RunID: runID, ProviderName: pr.Name(), Model: pr.Model(),
					AskerBrand: user, WinnerBrand: l.winner, Prompt: l.prompt,
				}
				if err != nil {
					rec.Error = err.Error()
				} else {
					rec.Advice = resp.Text
					rec.InputTokens = resp.InputTokens
					rec.OutputTokens = resp.OutputTokens
					rec.CostUSD = pricing.Estimate(pr.Model(), resp.InputTokens, resp.OutputTokens)
				}
				_ = s.st.InsertAdvice(context.Background(), rec)
				mu.Lock()
				i++
				s.jm.Update(job.ID, i, total, fmt.Sprintf("advised on %s", clipShort(l.prompt, 40)))
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	s.jm.Finish(job.ID, JobDone, "advise complete")
}

// ---------- helpers ----------

// projectToConfig hydrates the JSON blobs into a config.Config the runner
// understands.
func projectToConfig(p *store.Project) (*config.Config, error) {
	var brand config.Brand
	var comps []config.Brand
	var prompts []string
	var provs []config.Provider
	if err := json.Unmarshal([]byte(p.BrandJSON), &brand); err != nil {
		return nil, fmt.Errorf("brand JSON: %w", err)
	}
	_ = json.Unmarshal([]byte(p.CompetitorsJSON), &comps)
	_ = json.Unmarshal([]byte(p.PromptsJSON), &prompts)
	_ = json.Unmarshal([]byte(p.ProvidersJSON), &provs)
	cfg := &config.Config{
		Brand:       brand,
		Competitors: comps,
		Prompts:     prompts,
		Providers:   provs,
		SamplesPer:  p.SamplesPerPrompt,
		Concurrency: p.ConcurrencyPerProvider,
		MaxTokens:   p.MaxTokens,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// buildProvidersFromEnv mirrors cli.buildProviders but lives in the server
// package so we don't create an import cycle with cli.
func buildProvidersFromEnv(cfg *config.Config) ([]provider.Provider, error) {
	var out []provider.Provider
	for _, p := range cfg.Providers {
		switch p.Kind {
		case "openai":
			key := os.Getenv("OPENAI_API_KEY")
			if key == "" {
				return nil, fmt.Errorf("provider %q needs OPENAI_API_KEY", p.Name)
			}
			c := provider.NewOpenAI(p.Name, p.Model, key)
			if u := os.Getenv("OPENAI_BASE_URL"); u != "" {
				c.Endpoint = u
			}
			out = append(out, c)
		case "anthropic":
			key := os.Getenv("ANTHROPIC_API_KEY")
			if key == "" {
				return nil, fmt.Errorf("provider %q needs ANTHROPIC_API_KEY", p.Name)
			}
			c := provider.NewAnthropic(p.Name, p.Model, key)
			if u := os.Getenv("ANTHROPIC_BASE_URL"); u != "" {
				c.Endpoint = u
			}
			out = append(out, c)
		case "perplexity":
			key := os.Getenv("PERPLEXITY_API_KEY")
			if key == "" {
				return nil, fmt.Errorf("provider %q needs PERPLEXITY_API_KEY", p.Name)
			}
			c := provider.NewPerplexity(p.Name, p.Model, key)
			if u := os.Getenv("PERPLEXITY_BASE_URL"); u != "" {
				c.Endpoint = u
			}
			out = append(out, c)
		default:
			return nil, fmt.Errorf("unsupported provider kind %q", p.Kind)
		}
	}
	return out, nil
}

func clipShort(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// progressBuf turns runner.Out (an io.Writer the runner uses to print
// per-sample progress) into JobManager.Update calls.
type progressBuf struct {
	job   *Job
	jm    *JobManager
	total int
	done  int
	mu    sync.Mutex
}

func (p *progressBuf) Write(b []byte) (int, error) {
	// Runner writes one line per sample. Detect "[N/M]" markers and the
	// final "Done." line.
	s := string(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(s, "Done.") || strings.Contains(s, "completed") {
		// keep the message visible
		p.jm.Update(p.job.ID, p.done, p.total, strings.TrimSpace(s))
		return len(b), nil
	}
	// Light parser: look for "[N/M]" pattern in the runner's progress line.
	if open := strings.Index(s, "["); open >= 0 {
		slash := strings.Index(s[open:], "/")
		end := strings.Index(s[open:], "]")
		if slash > 0 && end > slash {
			n, errN := strconv.Atoi(strings.TrimSpace(s[open+1 : open+slash]))
			m, errM := strconv.Atoi(strings.TrimSpace(s[open+slash+1 : open+end]))
			if errN == nil && errM == nil {
				p.done = n
				p.total = m
				p.jm.Update(p.job.ID, n, m, "")
			}
		}
	}
	return len(b), nil
}

// importLegacyConfig is called once at startup. If the DB has zero projects
// but a salience.json sits next to the DB, we promote it to project #1 so
// existing users see their workspace immediately in the dashboard.
func (s *Server) importLegacyConfig(ctx context.Context) {
	count, err := s.st.LatestProjectID(ctx)
	if err != nil || count > 0 {
		return
	}
	candidates := []string{"salience.json"}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg config.Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		brand, _ := json.Marshal(cfg.Brand)
		comps, _ := json.Marshal(cfg.Competitors)
		prompts, _ := json.Marshal(cfg.Prompts)
		provs, _ := json.Marshal(cfg.Providers)
		_, _ = s.st.CreateProject(ctx, store.Project{
			Name:                   cfg.Brand.Name,
			BrandJSON:              string(brand),
			CompetitorsJSON:        string(comps),
			PromptsJSON:            string(prompts),
			ProvidersJSON:          string(provs),
			SamplesPerPrompt:       cfg.SamplesPer,
			ConcurrencyPerProvider: cfg.Concurrency,
			MaxTokens:              cfg.MaxTokens,
			Notes:                  fmt.Sprintf("Imported from %s on first dashboard launch.", path),
		})
		return
	}
}
