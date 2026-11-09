package statdist

import (
	"math"
	"testing"
)

// TestReferenceOracle_BinomialTwoSidedP: BinomialTwoSidedP equals R's
// binom.test(x, n, p)$p.value (binomial.json, scripts/reference/
// gen_symmetry.R) to a relative 1e-10 — the relErr tie cases (equal
// densities across the mean), x on the mean, both tails, the p = 0 / 1
// edges and a large n.
func TestReferenceOracle_BinomialTwoSidedP(t *testing.T) {
	t.Parallel()

	cases := loadReference[struct {
		X      int     `json:"x"`
		N      int     `json:"n"`
		P      float64 `json:"p"`
		PValue float64 `json:"p_value"`
	}](t, "binomial")
	if len(cases) < 20 {
		t.Fatalf("binomial.json has %d cases, want at least 20", len(cases))
	}
	r := &failReporter{t: t}
	for _, c := range cases {
		got := BinomialTwoSidedP(c.X, c.N, c.P)
		if !oracleClose(got, c.PValue, oracleRelTol, oracleProbAbsFloor) {
			r.errorf("BinomialTwoSidedP(%d, %d, %.17g) = %.17g, want %.17g (R)", c.X, c.N, c.P, got, c.PValue)
		}
	}
	r.done()
}

// TestBinomialTwoSidedP_ClosedForms: at p = 0.5 the two-sided p is
// min(1, 2·P(X ≤ min(x, n − x))) — exact dyadic rationals for small n —
// and the tie rule makes the mirror outcome count once, so x and n − x
// give the same p.
func TestBinomialTwoSidedP_ClosedForms(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		x, n int
		want float64
	}{
		{0, 1, 1}, {0, 3, 0.25}, {1, 4, 0.625}, {0, 10, 2.0 / 1024}, {2, 10, 2 * 56.0 / 1024}, {5, 10, 1},
	} {
		for _, x := range []int{c.x, c.n - c.x} {
			if got := BinomialTwoSidedP(x, c.n, 0.5); math.Abs(got-c.want) > 1e-15 {
				t.Errorf("BinomialTwoSidedP(%d, %d, 0.5) = %.17g, want %.17g", x, c.n, got, c.want)
			}
		}
	}
}

// TestBinomialTwoSidedP_Refusals: inputs binom.test refuses are NaN.
func TestBinomialTwoSidedP_Refusals(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		x, n int
		p    float64
	}{
		{0, 0, 0.5}, {-1, 3, 0.5}, {4, 3, 0.5}, {1, 3, -0.1}, {1, 3, 1.1}, {1, 3, math.NaN()},
	} {
		if got := BinomialTwoSidedP(c.x, c.n, c.p); !math.IsNaN(got) {
			t.Errorf("BinomialTwoSidedP(%d, %d, %v) = %v, want NaN", c.x, c.n, c.p, got)
		}
	}
}
