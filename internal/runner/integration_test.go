package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/salience-cli/salience/internal/config"
	"github.com/salience-cli/salience/internal/provider"
	"github.com/salience-cli/salience/internal/store"
)

// mockOpenAI returns an httptest.Server that responds to chat/completions
// with the given text and one annotated url_citation.
func mockOpenAI(t *testing.T, text, citationURL string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-openai-key" {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&map[string]any{}) // discard
		body := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": text,
						"annotations": []map[string]any{
							{
								"type": "url_citation",
								"url_citation": map[string]any{
									"url":   citationURL,
									"title": "cited page",
								},
							},
						},
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     42,
				"completion_tokens": 137,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// mockAnthropic returns an httptest.Server that responds to /messages with
// the given text and one web_search_tool_result containing one source.
func mockAnthropic(t *testing.T, text, sourceURL string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-anthropic-key" {
			http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		toolItems, _ := json.Marshal([]map[string]any{
			{"type": "web_search_result", "url": sourceURL, "title": "cited blog"},
		})
		body := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": text},
				{"type": "web_search_tool_result", "content": json.RawMessage(toolItems)},
			},
			"usage": map[string]any{
				"input_tokens":  77,
				"output_tokens": 154,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestEndToEndBenchAgainstMocks(t *testing.T) {
	// Mock responses are designed so that detection should find each
	// expected brand at least once.
	oa := mockOpenAI(t, "I'd pick Northwind for that — and Contoso also has a free tier.",
		"https://northwind.example/pricing")
	defer oa.Close()
	an := mockAnthropic(t, "Northwind is well-regarded; Fabrikam less so.",
		"https://fabrikam.example/blog")
	defer an.Close()

	cfg := &config.Config{
		Brand: config.Brand{Name: "Northwind", Aliases: []string{"northwind.example"}},
		Competitors: []config.Brand{
			{Name: "Contoso", Aliases: []string{"contoso.example"}},
			{Name: "Fabrikam", Aliases: []string{"fabrikam.example"}},
		},
		Prompts: []string{"which CRM is best for a small SaaS team"},
		Providers: []config.Provider{
			{Name: "test-openai", Kind: "openai", Model: "gpt-4.1-mini"},
			{Name: "test-anthropic", Kind: "anthropic", Model: "claude-haiku-4-5"},
		},
		SamplesPer:  2,
		Concurrency: 2,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "salience.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	oaClient := provider.NewOpenAI("test-openai", "gpt-4.1-mini", "test-openai-key")
	oaClient.Endpoint = oa.URL
	anClient := provider.NewAnthropic("test-anthropic", "claude-haiku-4-5", "test-anthropic-key")
	anClient.Endpoint = an.URL

	var out bytes.Buffer
	r := &Runner{
		Cfg:       cfg,
		Providers: []provider.Provider{oaClient, anClient},
		Store:     st,
		Out:       &out,
		Opts:      Options{MaxAttempts: 1, BaseDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runID, err := r.Run(ctx, 0)
	if err != nil {
		t.Fatalf("Run returned error: %v\nrunner output:\n%s", err, out.String())
	}
	if runID == 0 {
		t.Fatalf("expected non-zero run id")
	}

	// 1 prompt × 2 providers × 2 samples = 4 samples expected.
	samples, err := st.ListSamples(ctx, runID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(samples))
	}
	for _, s := range samples {
		if s.Error != "" {
			t.Errorf("sample %d had unexpected error: %s", s.ID, s.Error)
		}
		if s.CostUSD <= 0 {
			t.Errorf("sample %d has zero cost; expected pricing to compute > 0", s.ID)
		}
	}

	// Every sample's text contains "Northwind", so every sample should mention
	// the user brand at least once.
	ok, errored, err := st.CountSamples(ctx, runID)
	if err != nil {
		t.Fatalf("CountSamples: %v", err)
	}
	if ok != 4 || errored != 0 {
		t.Fatalf("expected ok=4 errored=0, got ok=%d errored=%d", ok, errored)
	}

	mentions, err := st.ListMentionsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListMentionsForRun: %v", err)
	}
	brandHits := map[string]int{}
	for _, m := range mentions {
		brandHits[m.Brand]++
	}
	if brandHits["Northwind"] == 0 {
		t.Errorf("expected at least one Northwind mention; got %v", brandHits)
	}
	if brandHits["Contoso"] == 0 {
		t.Errorf("expected at least one Contoso mention (OpenAI response); got %v", brandHits)
	}
	if brandHits["Fabrikam"] == 0 {
		t.Errorf("expected at least one Fabrikam mention (Anthropic response); got %v", brandHits)
	}

	// Re-running with -resume against the same run should be a no-op.
	r2 := &Runner{
		Cfg:       cfg,
		Providers: []provider.Provider{oaClient, anClient},
		Store:     st,
		Out:       &out,
		Opts:      Options{MaxAttempts: 1, BaseDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond},
	}
	_, err = r2.Run(ctx, runID)
	if err != nil {
		t.Fatalf("resume Run errored: %v", err)
	}
	samples2, _ := st.ListSamples(ctx, runID)
	if len(samples2) != 4 {
		t.Fatalf("resume should not add samples; got %d (started with 4)", len(samples2))
	}
}

func TestBenchAbortsOnAuthFailure(t *testing.T) {
	// Server always 401s, simulating a bad key. Runner must abort
	// without retrying forever.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer bad.Close()

	cfg := &config.Config{
		Brand:       config.Brand{Name: "Acme"},
		Prompts:     []string{"q"},
		Providers:   []config.Provider{{Name: "oa", Kind: "openai", Model: "gpt-4.1-mini"}},
		SamplesPer:  1,
		Concurrency: 1,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "auth.db")
	st, _ := store.Open(dbPath)
	defer st.Close()

	c := provider.NewOpenAI("oa", "gpt-4.1-mini", "whatever")
	c.Endpoint = bad.URL

	var out bytes.Buffer
	r := &Runner{
		Cfg: cfg, Providers: []provider.Provider{c}, Store: st, Out: &out,
		Opts: Options{MaxAttempts: 1, BaseDelay: 10 * time.Millisecond, MaxDelay: 30 * time.Millisecond},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.Run(ctx, 0)
	if err == nil {
		t.Fatalf("expected auth error, got nil")
	}
	if !strings.Contains(err.Error(), "auth") && !strings.Contains(err.Error(), "401") {
		t.Errorf("expected auth-style error, got %v", err)
	}
}
