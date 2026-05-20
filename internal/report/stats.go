package report

import "math"

// LowSampleThreshold is the n below which a rate is flagged as statistically
// unreliable in the report.
const LowSampleThreshold = 10

// WilsonInterval returns the lower and upper bounds of the Wilson score
// confidence interval for a binomial proportion. It is well-behaved at the
// extremes (rate=0% or rate=100%) and at small n, where the normal
// approximation is wrong. z=1.96 → 95% confidence.
func WilsonInterval(successes, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.96
	p := float64(successes) / float64(n)
	nf := float64(n)
	z2 := z * z
	center := (p + z2/(2*nf)) / (1 + z2/nf)
	margin := z * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf)) / (1 + z2/nf)
	lo = center - margin
	if lo < 0 {
		lo = 0
	}
	hi = center + margin
	if hi > 1 {
		hi = 1
	}
	return lo, hi
}

// IntervalsOverlap returns true if [a1,a2] and [b1,b2] share any point.
// Used to decide whether a per-cell rate change between two runs is
// statistically meaningful.
func IntervalsOverlap(a1, a2, b1, b2 float64) bool {
	return a1 <= b2 && b1 <= a2
}

// hasLowSampleCells reports whether any cell in d falls below
// LowSampleThreshold. Used by the markdown / HTML renderers to decide
// whether to print the statistical warning banner.
func (d Data) hasLowSampleCells() bool {
	for _, c := range d.Cells {
		if c.Samples < LowSampleThreshold {
			return true
		}
	}
	return false
}
