package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openTempStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for range 3 {
		st, err := Open(filepath.Join(dir, "a.db"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_ = st.Close()
	}
}

func TestStartAndFinishRun(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	id, err := st.StartRun(ctx, `{"brand":"A"}`, "A")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected non-zero id")
	}

	r, err := st.GetRun(ctx, id)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if r.Status != "running" || r.FinishedAt != nil {
		t.Fatalf("expected fresh run to be running with no finished_at, got %+v", r)
	}

	if err := st.FinishRun(ctx, id, "completed"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	r, _ = st.GetRun(ctx, id)
	if r.Status != "completed" || r.FinishedAt == nil {
		t.Fatalf("expected status=completed and finished_at set, got %+v", r)
	}
}

func TestSaveSampleAndDuplicateDetection(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	runID, _ := st.StartRun(ctx, "{}", "Brand")

	sr := SampleRecord{
		RunID: runID, Prompt: "q1", ProviderName: "p", ProviderKind: "openai",
		Model: "gpt-4.1-mini", SampleIdx: 0,
		ResponseText: "text mentioning Brand", Sources: []string{},
		InputTokens: 10, OutputTokens: 20, CostUSD: 0.001,
	}
	mentions := []MentionRecord{{Brand: "Brand", Alias: "Brand", Where: "body"}}

	if err := st.SaveSample(ctx, sr, mentions); err != nil {
		t.Fatalf("first SaveSample: %v", err)
	}
	if err := st.SaveSample(ctx, sr, mentions); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists on second SaveSample with same key, got %v", err)
	}
}

func TestListAndCount(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	runID, _ := st.StartRun(ctx, "{}", "Brand")

	for i := range 3 {
		sr := SampleRecord{
			RunID: runID, Prompt: "q", ProviderName: "p", ProviderKind: "openai",
			Model: "gpt-4.1-mini", SampleIdx: i,
			ResponseText: "x", Sources: []string{},
		}
		if err := st.SaveSample(ctx, sr, []MentionRecord{{Brand: "Brand", Alias: "Brand", Where: "body"}}); err != nil {
			t.Fatalf("SaveSample[%d]: %v", i, err)
		}
	}
	// One erroring sample.
	errSR := SampleRecord{
		RunID: runID, Prompt: "q", ProviderName: "p", ProviderKind: "openai",
		Model: "gpt-4.1-mini", SampleIdx: 99,
		ResponseText: "", Sources: []string{}, Error: "boom",
	}
	if err := st.SaveSample(ctx, errSR, nil); err != nil {
		t.Fatalf("save error sample: %v", err)
	}

	samples, err := st.ListSamples(ctx, runID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(samples))
	}

	ok, errored, err := st.CountSamples(ctx, runID)
	if err != nil {
		t.Fatalf("CountSamples: %v", err)
	}
	if ok != 3 || errored != 1 {
		t.Fatalf("expected ok=3 errored=1, got ok=%d errored=%d", ok, errored)
	}

	completed, err := st.CompletedKeys(ctx, runID)
	if err != nil {
		t.Fatalf("CompletedKeys: %v", err)
	}
	if len(completed) != 3 {
		t.Fatalf("expected 3 completed keys (errored excluded), got %d", len(completed))
	}

	mentions, err := st.ListMentionsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListMentionsForRun: %v", err)
	}
	if len(mentions) != 3 {
		t.Fatalf("expected 3 mentions, got %d", len(mentions))
	}
}

func TestListRunsOrdersNewestFirst(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	var ids []int64
	for range 3 {
		id, _ := st.StartRun(ctx, "{}", "B")
		ids = append(ids, id)
	}
	runs, err := st.ListRuns(ctx, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(runs))
	}
	if runs[0].ID != ids[2] || runs[2].ID != ids[0] {
		t.Fatalf("ListRuns should be newest-first; got %v", []int64{runs[0].ID, runs[1].ID, runs[2].ID})
	}
}

func TestLatestUnfinishedRunSkipsCompleted(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	id1, _ := st.StartRun(ctx, "{}", "B")
	_ = st.FinishRun(ctx, id1, "completed")
	id2, _ := st.StartRun(ctx, "{}", "B")

	got, err := st.LatestUnfinishedRunID(ctx)
	if err != nil {
		t.Fatalf("LatestUnfinishedRunID: %v", err)
	}
	if got != id2 {
		t.Fatalf("expected %d, got %d", id2, got)
	}
}
