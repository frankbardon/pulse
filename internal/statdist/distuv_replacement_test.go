package statdist

import (
	"math"
	"testing"

	"gonum.org/v1/gonum/stat/distuv"
)

// Deterministic replacements for the gonum distuv calls SEM makes
// (distuv.ChiSquared.CDF, distuv.UnitNormal.Quantile): ChiSquareCDF /
// ChiSquareSurvival and NormalQuantileDet.
//
// Documented tolerance against gonum, measured across the sweeps below:
//
//   - ChiSquareCDF: relative 1e-11 wherever gonum's CDF is ≥ 1e-300
//     (worst seen 2.6e-12, df = 5000 at CDF ≈ 1e-266, where gonum's
//     Cephes igam prefactor cancels and the saddle-point form here does
//     not; R agrees with this side, reference_oracle_test.go). Within
//     the bulk (CDF ≥ 1e-30, df ≤ 250) relative 1e-12.
//   - ChiSquareSurvival: relative 1e-12 against 1 − gonum CDF wherever
//     that complement is ≥ 1e-3 (below it the complement itself has
//     lost the digits).
//   - NormalQuantileDet: relative 1e-13 with an absolute floor of 1e-16
//     over p ∈ (0, 1) and p down to 1e-300 (worst seen 2.4e-13
//     relative = 6e-17 absolute at p = 0.4999, where the quantile is
//     ~p − ½ and both sides sit within an ulp of it; NormalQuantile's
//     math.* body shows the same gap).

func TestChiSquareCDF_AgreesWithGonum(t *testing.T) {
	t.Parallel()

	r := &failReporter{t: t}
	n := 0
	for _, df := range []float64{0.5, 1, 2, 3, 5, 7.5, 10, 19, 20, 33, 50, 100, 250, 1000, 5000} {
		for i := 1; i <= 200; i++ {
			x := float64(i) / 200 * 4 * (df + float64(4*math.Sqrt(df)))
			want := distuv.ChiSquared{K: df}.CDF(x)
			got := ChiSquareCDF(x, df)
			rel := 1e-11
			if want >= 1e-30 && df <= 250 {
				rel = 1e-12
			}
			if want >= 1e-300 {
				n++
				if !oracleClose(got, want, rel, 0) {
					r.errorf("ChiSquareCDF(%g, %g) = %.17g, gonum %.17g (rel %.3g)", x, df, got, want, math.Abs(got-want)/want)
				}
			}
			if comp := 1 - want; comp >= 1e-3 {
				if s := ChiSquareSurvival(x, df); !oracleClose(s, comp, 1e-12, 0) {
					r.errorf("ChiSquareSurvival(%g, %g) = %.17g, 1 − gonum CDF %.17g", x, df, s, comp)
				}
			}
		}
	}
	r.done()
	if n < 2000 {
		t.Fatalf("sweep compared only %d points", n)
	}
}

func TestChiSquare_SpecialCases(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	cases := []struct {
		x, df    float64
		cdf, sur float64
	}{
		{0, 3, 0, 1},
		{-1, 3, 0, 1},
		{math.Inf(1), 3, 1, 0},
	}
	for _, c := range cases {
		if got := ChiSquareCDF(c.x, c.df); got != c.cdf {
			t.Errorf("ChiSquareCDF(%v, %v) = %v, want %v", c.x, c.df, got, c.cdf)
		}
		if got := ChiSquareSurvival(c.x, c.df); got != c.sur {
			t.Errorf("ChiSquareSurvival(%v, %v) = %v, want %v", c.x, c.df, got, c.sur)
		}
	}
	for _, c := range [][2]float64{{nan, 3}, {1, nan}, {1, 0}, {1, -2}} {
		if got := ChiSquareCDF(c[0], c[1]); !math.IsNaN(got) {
			t.Errorf("ChiSquareCDF(%v, %v) = %v, want NaN", c[0], c[1], got)
		}
		if got := ChiSquareSurvival(c[0], c[1]); !math.IsNaN(got) {
			t.Errorf("ChiSquareSurvival(%v, %v) = %v, want NaN", c[0], c[1], got)
		}
	}
	// P + Q = 1 across both branches of the incomplete gamma.
	for _, c := range [][2]float64{{0.5, 1}, {3, 2}, {40, 30}, {5, 30}, {1100, 1000}} {
		if s := ChiSquareCDF(c[0], c[1]) + ChiSquareSurvival(c[0], c[1]); math.Abs(s-1) > 1e-15 {
			t.Errorf("CDF + survival at (%v, %v) = %.17g, want 1", c[0], c[1], s)
		}
	}
}

func TestNormalQuantileDet_AgreesWithGonum(t *testing.T) {
	t.Parallel()

	r := &failReporter{t: t}
	check := func(p float64) {
		want := distuv.UnitNormal.Quantile(p)
		if got := NormalQuantileDet(p); !oracleClose(got, want, 1e-13, 1e-16) {
			r.errorf("NormalQuantileDet(%g) = %.17g, gonum %.17g", p, got, want)
		}
	}
	for i := 1; i < 10000; i++ {
		check(float64(i) / 10000)
	}
	for e := -300.0; e < -1; e += 0.25 {
		p := math.Pow(10, e)
		check(p)
		if q := 1 - p; q < 1 {
			check(q)
		}
	}
	r.done()
}

func TestNormalQuantileDet_SpecialCases(t *testing.T) {
	t.Parallel()

	if got := NormalQuantileDet(0); !math.IsInf(got, -1) {
		t.Errorf("NormalQuantileDet(0) = %v, want -Inf", got)
	}
	if got := NormalQuantileDet(1); !math.IsInf(got, 1) {
		t.Errorf("NormalQuantileDet(1) = %v, want +Inf", got)
	}
	for _, p := range []float64{math.NaN()} {
		if got := NormalQuantileDet(p); !math.IsNaN(got) {
			t.Errorf("NormalQuantileDet(NaN) = %v, want NaN", got)
		}
	}
	if got := NormalQuantileDet(0.5); got != 0 {
		t.Errorf("NormalQuantileDet(0.5) = %v, want 0", got)
	}
}

// TestStatdist_ChiSquareAndNormalQuantileDetBitsPinned is the
// cross-architecture gate on the distuv replacements: the pinned bits
// must hold exactly on every GOARCH. Pinned on darwin/arm64 and checked
// identical on linux/amd64 (docker); CI re-runs it on linux/amd64.
func TestStatdist_ChiSquareAndNormalQuantileDetBitsPinned(t *testing.T) {
	t.Parallel()

	chi := []struct {
		x, df    float64
		cdf, sur uint64
	}{
		{0.5, 1, 0x3fe0a7ef5c18edd3, 0x3fdeb02147ce245a},
		{3.84, 1, 0x3fee660b211b8630, 0x3fa99f4dee479cfa},
		{11.07, 5, 0x3fee66523a7100b4, 0x3fa99adc58eff4be},
		{2, 3, 0x3fdb5db0451254f1, 0x3fe25127dd76d588},
		{35, 20, 0x3fef5b4e47346631, 0x3f949637197339df},
		{12, 30, 0x3f56f1827ab20de5, 0x3feff4873ec2a6f9},
		{1250, 1000, 0x3fefffffc99db7a1, 0x3e7b31242f4fb5ea},
		{0.01, 7.5, 0x3de36638b6669aa1, 0x3fefffffffec99c7},
		{150, 100, 0x3feff898512e16cc, 0x3f4d9ebb47a4ce3b},
		{60, 9, 0x3fefffffff47bd16, 0x3e17085d4a3000bb},
	}
	for _, c := range chi {
		if got := ChiSquareCDF(c.x, c.df); math.Float64bits(got) != c.cdf {
			t.Errorf("ChiSquareCDF(%v, %v) = %v (%#016x), want %#016x", c.x, c.df, got, math.Float64bits(got), c.cdf)
		}
		if got := ChiSquareSurvival(c.x, c.df); math.Float64bits(got) != c.sur {
			t.Errorf("ChiSquareSurvival(%v, %v) = %v (%#016x), want %#016x", c.x, c.df, got, math.Float64bits(got), c.sur)
		}
	}
	quantiles := []struct {
		p    float64
		want uint64
	}{
		{0.975, 0x3fff5c0331eeff84},
		{0.995, 0x40049b4c64d69161},
		{0.95, 0x3ffa515209676abc},
		{0.9, 0x3ff4813c36e26d33},
		{0.5, 0x0000000000000000},
		{0.6, 0x3fd036d6c4a04b5a},
		{0.025, 0xbfff5c0331eeff86},
		{0.01, 0xc0029c5c4630ff0f},
		{0.3, 0xbfe0c7e39582c5fb},
		{1e-10, 0xc0197203597a2155},
		{1e-300, 0xc04286074064c26e},
		{0.99999, 0x40110f3f8843a851},
	}
	for _, c := range quantiles {
		if got := NormalQuantileDet(c.p); math.Float64bits(got) != c.want {
			t.Errorf("NormalQuantileDet(%v) = %v (%#016x), want %#016x", c.p, got, math.Float64bits(got), c.want)
		}
	}
}
