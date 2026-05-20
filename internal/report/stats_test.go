package report

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestWilsonInterval_ExtremeCases(t *testing.T) {
	if lo, hi := WilsonInterval(0, 0); lo != 0 || hi != 0 {
		t.Fatalf("n=0 should be (0,0), got (%v,%v)", lo, hi)
	}
	// 0/10 — lower bound must be 0 exactly (Wilson clamps).
	if lo, hi := WilsonInterval(0, 10); lo != 0 || hi <= 0 {
		t.Fatalf("0/10 expected (0, >0), got (%v,%v)", lo, hi)
	}
	// 10/10 — upper bound must be 1 exactly.
	if lo, hi := WilsonInterval(10, 10); hi != 1 || lo >= 1 {
		t.Fatalf("10/10 expected (<1, 1), got (%v,%v)", lo, hi)
	}
}

func TestWilsonInterval_TinyN(t *testing.T) {
	// 1/3 (33%): Wilson CI is approximately [6%, 79%] — very wide, which is
	// exactly the point of showing CIs at small n.
	lo, hi := WilsonInterval(1, 3)
	if !almostEqual(lo, 0.06) || !almostEqual(hi, 0.79) {
		t.Fatalf("Wilson 1/3 expected ~[0.06, 0.79], got [%.2f, %.2f]", lo, hi)
	}
}

func TestWilsonInterval_LargeN(t *testing.T) {
	// 50/100 (50%): Wilson CI is approximately [40%, 60%].
	lo, hi := WilsonInterval(50, 100)
	if !almostEqual(lo, 0.40) || !almostEqual(hi, 0.60) {
		t.Fatalf("Wilson 50/100 expected ~[0.40, 0.60], got [%.2f, %.2f]", lo, hi)
	}
}

func TestIntervalsOverlap(t *testing.T) {
	if !IntervalsOverlap(0.1, 0.4, 0.3, 0.5) {
		t.Fatal("overlapping intervals should report overlap")
	}
	if IntervalsOverlap(0.1, 0.2, 0.3, 0.4) {
		t.Fatal("disjoint intervals should not report overlap")
	}
	if !IntervalsOverlap(0.1, 0.4, 0.4, 0.5) {
		t.Fatal("intervals touching at boundary should overlap")
	}
}
