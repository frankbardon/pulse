package mcmc

import (
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// windowEnds drives a varAdaptation through warmup iterations and
// returns the iterations whose draw closed a slow window, plus the
// iterations whose draw entered the variance estimate.
func windowEnds(warmup int) (ends []int, used int) {
	va := newVarAdaptation(warmup, 1)
	m := []float64{1}
	for i := range warmup {
		before := va.n
		upd, _ := va.learn(m, []float64{float64(i)})
		if upd || va.n > before {
			used++
		}
		if upd {
			ends = append(ends, i)
		}
	}
	return ends, used
}

// TestNUTS_WarmupWindows: Stan's windowed schedule. 1000 iterations give
// the 75 / 25·50·100·200·500 / 50 split (the last window stretched to the
// terminal buffer); short warmups use 15% / 75% / 10%; below 20 there is
// no metric window at all.
func TestNUTS_WarmupWindows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		warmup int
		ends   []int
		used   int
	}{
		{1000, []int{99, 149, 249, 449, 949}, 875},
		{500, []int{99, 149, 249, 449}, 375},
		{150, []int{99}, 25},
		{149, []int{134}, 113}, // 22 / 113 / 14: the short split
		{100, []int{89}, 75},
		{20, []int{17}, 15},
		{19, nil, 0},
	}
	for _, tc := range cases {
		ends, used := windowEnds(tc.warmup)
		if !slices.Equal(ends, tc.ends) || used != tc.used {
			t.Errorf("warmup %d: window ends %v using %d draws, want %v using %d", tc.warmup, ends, used, tc.ends, tc.used)
		}
	}
}

// TestNUTS_RegularisedVariance: a closed window's inverse metric is
// Stan's (n/(n+5))·var + 1e-3·5/(n+5) of the window's draws, and the
// next window starts from an empty estimate.
func TestNUTS_RegularisedVariance(t *testing.T) {
	t.Parallel()

	va := newVarAdaptation(20, 2) // one window: iterations 3..17, n = 15
	m := []float64{1, 1}
	var xs, ys []float64
	for i := range 20 {
		x := float64(i * i % 7)
		y := float64(3*i - 10)
		if i >= 3 && i <= 17 {
			xs, ys = append(xs, x), append(ys, y)
		}
		if upd, ok := va.learn(m, []float64{x, y}); upd != (i == 17) || !ok {
			t.Fatalf("iteration %d: updated %v ok %v", i, upd, ok)
		}
	}
	for k, vals := range [][]float64{xs, ys} {
		var s float64
		for _, v := range vals {
			s += v
		}
		mean := s / float64(len(vals))
		var ss float64
		for _, v := range vals {
			ss += float64((v - mean) * (v - mean))
		}
		n := float64(len(vals))
		want := float64((n/(n+5))*(ss/(n-1))) + float64(1e-3*(5/(n+5)))
		if math.Abs(m[k]-want) > 1e-12*want {
			t.Errorf("metric[%d] = %.15g, want %.15g", k, m[k], want)
		}
	}
	if va.n != 0 || va.m[0] != 0 || va.m2[1] != 0 {
		t.Errorf("estimator not reset after the window: n %d m %v m2 %v", va.n, va.m, va.m2)
	}
}

// TestNUTS_DualAveraging: Hoffman and Gelman's recursion with Stan's
// constants, checked against the formulas step by step; restart forgets
// the history and re-centres μ on log(10·ε).
func TestNUTS_DualAveraging(t *testing.T) {
	t.Parallel()

	const delta, eps0 = 0.8, 0.5
	da := newDualAveraging(delta, eps0)
	mu := detmath.Log(10 * eps0)
	var sBar, xBar float64
	for i, a := range []float64{0.3, 1.4, 0.95, 0.0, 0.8, 0.6} {
		k := float64(i + 1)
		eta := 1 / (k + 10)
		stat := math.Min(a, 1)
		sBar = float64((1-eta)*sBar) + float64(eta*(delta-stat))
		x := mu - float64(sBar*math.Sqrt(k))/0.05
		w := detmath.Pow(k, -0.75)
		xBar = float64((1-w)*xBar) + float64(w*x)
		if got, want := da.learn(a), detmath.Exp(x); math.Abs(got-want) > 1e-12*want {
			t.Fatalf("step %d: ε %.15g, want %.15g", i, got, want)
		}
	}
	if got, want := da.final(), detmath.Exp(xBar); math.Abs(got-want) > 1e-12*want {
		t.Errorf("final ε %.15g, want %.15g", got, want)
	}
	da.restart(2)
	if da.counter != 0 || da.sBar != 0 || da.xBar != 0 || da.mu != detmath.Log(20) {
		t.Errorf("restart left %+v", *da)
	}
	// An on-target statistic keeps ε at exp(μ) = 10·ε0.
	if got := da.learn(delta); math.Abs(got-20) > 1e-12 {
		t.Errorf("on-target ε %.15g, want 20", got)
	}
}

// TestNUTS_ChainSeedsDistinct: neighbouring seeds and chains never share
// a PCG seed.
func TestNUTS_ChainSeedsDistinct(t *testing.T) {
	t.Parallel()

	seen := map[uint64]bool{}
	for seed := range uint64(64) {
		for c := range 16 {
			s := chainSeed(seed, c)
			if seen[s] {
				t.Fatalf("chainSeed(%d, %d) repeats %#x", seed, c, s)
			}
			seen[s] = true
		}
	}
}
