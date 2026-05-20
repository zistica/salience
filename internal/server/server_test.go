package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salience-cli/salience/internal/store"
)

// seedStore creates a fresh in-temp-dir DB with one completed run, two
// samples, and one mention so the API handlers have something to return.
func seedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "srv.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	cfgJSON := `{"brand":{"name":"Northwind","aliases":["northwind.example"]},
	  "competitors":[{"name":"Contoso","aliases":["contoso.example"]}],
	  "prompts":["q"],
	  "providers":[{"name":"p","kind":"openai","model":"gpt-4.1-mini"}],
	  "samples_per_prompt":2,"concurrency_per_provider":1}`
	runID, err := st.StartRun(ctx, cfgJSON, "Northwind")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	for i := range 2 {
		_ = st.SaveSample(ctx, store.SampleRecord{
			RunID: runID, Prompt: "q", ProviderName: "p", ProviderKind: "openai",
			Model: "gpt-4.1-mini", SampleIdx: i,
			ResponseText: "Northwind is great.",
			Sources:      []any{},
			InputTokens:  10, OutputTokens: 20, CostUSD: 0.001,
		}, []store.MentionRecord{{
			Brand: "Northwind", Alias: "Northwind", Where: "text",
			Context: "Northwind is great.", Sentiment: "positive",
		}})
	}
	_ = st.FinishRun(ctx, runID, "completed")
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestHandler_ServesUI(t *testing.T) {
	st := seedStore(t)
	srv := New(st, "test.db")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected html content-type, got %q", ct)
	}
}

func TestHandler_RunsAPI(t *testing.T) {
	st := seedStore(t)
	srv := New(st, "test.db")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/runs")
	if err != nil {
		t.Fatalf("get /api/runs: %v", err)
	}
	defer res.Body.Close()
	var runs []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0]["brand_name"] != "Northwind" {
		t.Fatalf("unexpected brand_name: %v", runs[0]["brand_name"])
	}
	if okN, _ := runs[0]["ok"].(float64); okN != 2 {
		t.Fatalf("expected ok=2, got %v", runs[0]["ok"])
	}
}

func TestHandler_RunReport(t *testing.T) {
	st := seedStore(t)
	srv := New(st, "test.db")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/runs/1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var rep map[string]any
	_ = json.NewDecoder(res.Body).Decode(&rep)
	if rep["UserBrand"] != "Northwind" {
		t.Fatalf("UserBrand wrong: %v", rep["UserBrand"])
	}
}

func TestHandler_RunMentions(t *testing.T) {
	st := seedStore(t)
	srv := New(st, "test.db")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/runs/1/mentions")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	var ms []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ms)
	if len(ms) != 2 {
		t.Fatalf("expected 2 mentions, got %d", len(ms))
	}
	if ms[0]["Sentiment"] != "positive" {
		t.Fatalf("expected positive sentiment, got %v", ms[0]["Sentiment"])
	}
}

func TestHandler_RunSources(t *testing.T) {
	st := seedStore(t)
	srv := New(st, "test.db")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/runs/1/sources")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var src map[string]any
	_ = json.NewDecoder(res.Body).Decode(&src)
	if src["Brand"] != "Northwind" {
		t.Fatalf("Brand wrong: %v", src["Brand"])
	}
}

func TestHandler_BadRunID(t *testing.T) {
	st := seedStore(t)
	srv := New(st, "test.db")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/runs/not-a-number")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("expected 400 for bad id, got %d", res.StatusCode)
	}
}
