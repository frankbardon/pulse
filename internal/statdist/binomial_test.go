package statdist

import (
	"encoding/json"
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

// oddsRef is a binomial.json odds bound: null (NaN), the string "Inf"
// (+Inf) or a number.
type oddsRef float64

func (o *oddsRef) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "null":
		*o = oddsRef(math.NaN())
		return nil
	case `"Inf"`:
		*o = oddsRef(math.Inf(1))
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*o = oddsRef(f)
	return nil
}

// TestReferenceOracle_BinomialOddsInterval: BinomialOddsInterval equals
// R's binom.test(x, n, conf.level = level)$conf.int transformed to the
// odds p / (1 - p) (binomial.json, scripts/reference/gen_symmetry.R) to
// a relative 1e-10 — x = 0 (lower 0), x = n (upper +Inf), levels 0.5
// to 0.99, both far tails (x = 1 and x = 999 of 1000) and a large n.
func TestReferenceOracle_BinomialOddsInterval(t *testing.T) {
	t.Parallel()

	cases := loadReference[struct {
		X         int     `json:"x"`
		N         int     `json:"n"`
		Level     float64 `json:"level"`
		OddsLower oddsRef `json:"odds_lower"`
		OddsUpper oddsRef `json:"odds_upper"`
	}](t, "binomial")
	r := &failReporter{t: t}
	levels := map[float64]bool{}
	for _, c := range cases {
		if c.Level == 0 {
			r.errorf("binomial.json case x=%d n=%d has no level", c.X, c.N)
			continue
		}
		levels[c.Level] = true
		lo, hi := BinomialOddsInterval(c.X, c.N, c.Level)
		for _, b := range []struct {
			name      string
			got, want float64
		}{{"lower", lo, float64(c.OddsLower)}, {"upper", hi, float64(c.OddsUpper)}} {
			if math.IsInf(b.want, 1) || b.want == 0 {
				if b.got != b.want {
					r.errorf("BinomialOddsInterval(%d, %d, %g) %s = %.17g, want %v (R)", c.X, c.N, c.Level, b.name, b.got, b.want)
				}
				continue
			}
			if !oracleClose(b.got, b.want, oracleRelTol, 0) {
				r.errorf("BinomialOddsInterval(%d, %d, %g) %s = %.17g, want %.17g (R)", c.X, c.N, c.Level, b.name, b.got, b.want)
			}
		}
	}
	if len(levels) < 4 {
		t.Errorf("binomial.json covers %d confidence levels, want at least 4", len(levels))
	}
	r.done()
}

// TestBinomialOddsInterval_ClosedForms: x = n's lower bound solves
// p^n = alpha / 2, so its odds are (alpha/2)^(1/n) / (1 - (alpha/2)^(1/n));
// x = 0 mirrors it as the upper bound's reciprocal, and swapping x for
// n − x inverts both bounds.
func TestBinomialOddsInterval_ClosedForms(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 2, 5, 40} {
		p := math.Pow(0.025, 1/float64(n))
		want := p / (1 - p)
		lo, hi := BinomialOddsInterval(n, n, 0.95)
		if math.Abs(lo-want) > 1e-12*want || !math.IsInf(hi, 1) {
			t.Errorf("BinomialOddsInterval(%d, %d) = (%v, %v), want (%v, +Inf)", n, n, lo, hi, want)
		}
		lo0, hi0 := BinomialOddsInterval(0, n, 0.95)
		if lo0 != 0 || math.Abs(hi0-1/want) > 1e-12/want {
			t.Errorf("BinomialOddsInterval(0, %d) = (%v, %v), want (0, %v)", n, lo0, hi0, 1/want)
		}
	}
	for _, c := range [][2]int{{3, 10}, {150, 236}, {1, 7}} {
		lo, hi := BinomialOddsInterval(c[0], c[1], 0.9)
		mlo, mhi := BinomialOddsInterval(c[1]-c[0], c[1], 0.9)
		if math.Abs(lo*mhi-1) > 1e-12 || math.Abs(hi*mlo-1) > 1e-12 {
			t.Errorf("BinomialOddsInterval(%d, %d) = (%v, %v) is not the reciprocal of its mirror (%v, %v)", c[0], c[1], lo, hi, mlo, mhi)
		}
	}
}

// TestBinomialOddsInterval_Refusals: inputs binom.test refuses (n < 1,
// x outside [0, n], a level outside (0, 1)) are NaN on both bounds.
func TestBinomialOddsInterval_Refusals(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		x, n  int
		level float64
	}{
		{0, 0, 0.95}, {-1, 3, 0.95}, {4, 3, 0.95}, {1, 3, 0}, {1, 3, 1}, {1, 3, math.NaN()},
	} {
		if lo, hi := BinomialOddsInterval(c.x, c.n, c.level); !math.IsNaN(lo) || !math.IsNaN(hi) {
			t.Errorf("BinomialOddsInterval(%d, %d, %v) = (%v, %v), want NaN", c.x, c.n, c.level, lo, hi)
		}
	}
}
