package clinical

import "math"

// Test hooks for the growth engine (CP47).
//
// The validation set is the publishers' own printed cut-offs, and reproducing them means
// running the LMS transform *forwards* — value at a z — which nothing in the production API
// needs. Exposing it here rather than exporting it keeps the package's surface honest: this
// file is only compiled into the test binary.

// ValueAtZForTest is the inverse of the z-score: what measurement sits at this z.
func ValueAtZForTest(l, m, s, z float64) float64 {
	if math.Abs(l) < 1e-12 {
		return m * math.Exp(s*z)
	}
	return m * math.Pow(1+l*s*z, 1/l)
}

// ProbitForTest is the inverse normal CDF, so the test can turn CDC's printed percentile
// columns into the z-scores they were produced from.
func ProbitForTest(p float64) float64 { return probit(p) }
