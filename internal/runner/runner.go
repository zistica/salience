// Package runner orchestrates the actual benchmark: fan-out across providers,
// retries with backoff, resume support, and persistence to the store.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/detect"
	"github.com/salience-cli/salience/internal/pricing"
	"github.com/salience-cli/salience/internal/provider"
	"github.com/salience-cli/salience/internal/store"
)

// Options configures one Run call.
type Options struct {
	MaxAttempts int           // total attempts per sample including the first; default 4
	BaseDelay   time.Duration // backoff base; default 750ms
	MaxDelay    time.Duration // backoff cap; default 20s
}

// Defaults fills in zero values.
func (o *Options) Defaults() {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 4
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = 750 * time.Millisecond
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = 20 * time.Second
	}
}

// Runner ties together config, providers, and the store for one execution.
type Runner struct {
	Cfg       *config.Config
	Providers []provider.Provider
	Store     *store.Store
	Out       io.Writer
	Opts      Options
}

// Run performs the benchmark. If resumeRunID is non-zero, completed samples
// from that run are skipped and new samples are appended to the same run.
// Otherwise a new run is started.
func (r *Runner) Run(ctx context.Context, resumeRunID int64) (int64, error) {
	r.Opts.Defaults()
	cfgJSON, _ := json.Marshal(r.Cfg)
	var runID int64
	var err error
	if resumeRunID != 0 {
		runID = resumeRunID
		fmt.Fprintf(r.Out, "resuming run #%d\n", runID)
	} else {
		runID, err = r.Store.StartRun(ctx, string(cfgJSON), r.Cfg.Brand.Name)
		if err != nil {
			return 0, fmt.Errorf("start run: %w", err)
		}
		fmt.Fprintf(r.Out, "started run #%d\n", runID)
	}

	completed, err := r.Store.CompletedKeys(ctx, runID)
	if err != nil {
		return runID, fmt.Errorf("load completed keys: %w", err)
	}

	brands := r.Cfg.AllBrands()
	totalPlanned := len(r.Cfg.Prompts) * len(r.Providers) * r.Cfg.SamplesPer

	// Set up a cancellable context that also flips on a fatal auth error so the
	// other workers stop quickly.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		fatalErr error
		done     int64
		skipped  int64
		failed   int64
	)

	setFatal := func(e error) {
		mu.Lock()
		if fatalErr == nil {
			fatalErr = e
			cancel()
		}
		mu.Unlock()
	}

	// One worker pool per provider (so each provider's concurrency knob is
	// honored independently).
	var wg sync.WaitGroup
	for _, p := range r.Providers {
		p := p
		jobs := make(chan job, 64)
		// Producer for this provider's jobs.
		go func() {
			for _, prompt := range r.Cfg.Prompts {
				for i := 0; i < r.Cfg.SamplesPer; i++ {
					key := store.EncodeKey(prompt, p.Name(), i)
					if _, ok := completed[key]; ok {
						atomic.AddInt64(&skipped, 1)
						continue
					}
					select {
					case <-runCtx.Done():
						close(jobs)
						return
					case jobs <- job{prompt: prompt, sampleIdx: i}:
					}
				}
			}
			close(jobs)
		}()

		// Workers for this provider.
		for w := 0; w < r.Cfg.Concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					if runCtx.Err() != nil {
						return
					}
					resp, err := r.callWithRetry(runCtx, p, j.prompt)
					if errors.Is(err, provider.ErrAuth) {
						setFatal(err)
						return
					}
					rec := store.SampleRecord{
						RunID:        runID,
						Prompt:       j.prompt,
						ProviderName: p.Name(),
						ProviderKind: p.Kind(),
						Model:        p.Model(),
						SampleIdx:    j.sampleIdx,
					}
					var mentions []store.MentionRecord
					if err != nil {
						rec.Error = err.Error()
						rec.Sources = []detect.Source{}
						atomic.AddInt64(&failed, 1)
					} else {
						rec.ResponseText = resp.Text
						rec.Sources = resp.Sources
						rec.InputTokens = resp.InputTokens
						rec.OutputTokens = resp.OutputTokens
						rec.CostUSD = pricing.Estimate(p.Model(), resp.InputTokens, resp.OutputTokens)
						det := detect.Detect(resp.Text, resp.Sources, brands)
						for _, m := range det.Matches {
							mentions = append(mentions, store.MentionRecord{
								Brand: m.Brand, Alias: m.Alias, Where: m.Where, IsDomain: m.IsDomain,
								Context:   m.Context,
								Sentiment: string(detect.Classify(m.Context, m.Alias)),
							})
						}
					}
					if err := r.Store.SaveSample(context.Background(), rec, mentions); err != nil && !errors.Is(err, store.ErrAlreadyExists) {
						fmt.Fprintf(r.Out, "  save error: %v\n", err)
					}
					n := atomic.AddInt64(&done, 1)
					if rec.Error == "" {
						fmt.Fprintf(r.Out, "  [%d/%d] %s :: %s :: sample %d -> %d brand mention(s)\n",
							n, int64(totalPlanned)-atomic.LoadInt64(&skipped), p.Name(), shortPrompt(j.prompt), j.sampleIdx, countUnique(mentions))
					} else {
						fmt.Fprintf(r.Out, "  [%d/%d] %s :: %s :: sample %d ERR: %s\n",
							n, int64(totalPlanned)-atomic.LoadInt64(&skipped), p.Name(), shortPrompt(j.prompt), j.sampleIdx, rec.Error)
					}
				}
			}()
		}
	}

	wg.Wait()

	status := "completed"
	if fatalErr != nil {
		status = "aborted"
	} else if ctx.Err() != nil {
		status = "interrupted"
	}
	_ = r.Store.FinishRun(context.Background(), runID, status)

	fmt.Fprintf(r.Out, "run #%d %s: %d done, %d skipped (resumed), %d failed\n",
		runID, status, atomic.LoadInt64(&done), atomic.LoadInt64(&skipped), atomic.LoadInt64(&failed))

	if fatalErr != nil {
		return runID, fatalErr
	}
	if ctx.Err() != nil {
		return runID, ctx.Err()
	}
	return runID, nil
}

type job struct {
	prompt    string
	sampleIdx int
}

// callWithRetry runs p.Call with exponential backoff for transient errors.
// Non-transient errors are returned immediately; auth errors are surfaced as
// provider.ErrAuth so the runner can abort the whole run.
func (r *Runner) callWithRetry(ctx context.Context, p provider.Provider, prompt string) (*provider.Response, error) {
	var last error
	for attempt := 1; attempt <= r.Opts.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		resp, err := p.Call(ctx, prompt, r.Cfg.MaxTokens, r.Cfg.Temperature)
		if err == nil {
			return resp, nil
		}
		last = err
		if errors.Is(err, provider.ErrAuth) {
			return nil, err
		}
		if !provider.IsTransient(err) {
			return nil, err
		}
		if attempt == r.Opts.MaxAttempts {
			break
		}
		delay := backoff(attempt, r.Opts.BaseDelay, r.Opts.MaxDelay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, fmt.Errorf("retries exhausted: %w", last)
}

// backoff returns the delay before retry #attempt: exponential growth with a
// small random jitter, capped at max.
func backoff(attempt int, base, max time.Duration) time.Duration {
	exp := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(base) * exp)
	if d > max {
		d = max
	}
	jitter := time.Duration(rand.Int63n(int64(base / 2)))
	return d + jitter
}

func shortPrompt(p string) string {
	if len(p) > 40 {
		return p[:37] + "..."
	}
	return p
}

func countUnique(ms []store.MentionRecord) int {
	seen := map[string]struct{}{}
	for _, m := range ms {
		seen[m.Brand] = struct{}{}
	}
	return len(seen)
}
