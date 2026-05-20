package pricing

import (
	"math"
	"testing"
)

func TestEstimate_KnownModel(t *testing.T) {
	// 1M input + 1M output at gpt-4o = 2.50 + 10.00 = 12.50
	got := Estimate("gpt-4o", 1_000_000, 1_000_000)
	if math.Abs(got-12.50) > 1e-9 {
		t.Errorf("got %v want 12.50", got)
	}
}

func TestEstimate_PrefixMatch(t *testing.T) {
	// dated snapshot should fall back via prefix match
	got := Estimate("gpt-4o-2024-08-06", 1000, 1000)
	if got <= 0 {
		t.Errorf("expected non-zero cost for snapshot, got %v", got)
	}
}

func TestEstimate_UnknownModelReturnsZero(t *testing.T) {
	if got := Estimate("totally-made-up-model", 100, 100); got != 0 {
		t.Errorf("expected 0 for unknown model, got %v", got)
	}
}
