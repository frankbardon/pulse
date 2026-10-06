package linalg_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse/linalg"
)

// refTryCholesky is a verbatim copy of internal/synth/copula.go
// tryCholesky as of the linalg-core effort. It is the bit contract
// linalg.Cholesky must reproduce; do NOT "improve" it.
func refTryCholesky(m [][]float64) ([][]float64, bool) {
	n := len(m)
	L := make([][]float64, n)
	for i := range L {
		L[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := m[i][j]
			for k := 0; k < j; k++ {
				sum -= float64(L[i][k] * L[j][k])
			}
			if i == j {
				if sum <= 0 {
					return nil, false
				}
				L[i][j] = math.Sqrt(sum)
			} else {
				L[i][j] = sum / L[j][j]
			}
		}
	}
	return L, true
}

// refCholesky is a verbatim copy of internal/synth/copula.go cholesky's
// ridge loop (error construction elided): the default RidgeSchedule
// must reproduce its factor AND its accumulated jitter bit for bit.
func refCholesky(m [][]float64) ([][]float64, float64, bool) {
	n := len(m)
	work := make([][]float64, n)
	for i := range work {
		work[i] = make([]float64, n)
		copy(work[i], m[i])
	}
	total := 0.0
	for ridge := 0; ridge < 8; ridge++ {
		L, ok := refTryCholesky(work)
		if ok {
			return L, total, true
		}
		jitter := math.Pow(10, float64(ridge-6))
		for i := 0; i < n; i++ {
			work[i][i] += jitter
		}
		total += jitter
	}
	return nil, total, false
}

// randomCorrelation builds a random correlation-like SPD matrix:
// G·Gᵀ for a random n×(n+extra) G, scaled to a unit diagonal. extra < 0
// yields an exactly rank-deficient (rank n+extra) matrix.
func randomCorrelation(rng *rand.Rand, n, extra int) [][]float64 {
	k := n + extra
	g := make([][]float64, n)
	for i := range g {
		g[i] = make([]float64, k)
		for j := range g[i] {
			g[i][j] = rng.NormFloat64()
		}
	}
	c := make([][]float64, n)
	for i := range c {
		c[i] = make([]float64, n)
		for j := range c[i] {
			s := 0.0
			for t := 0; t < k; t++ {
				s += float64(g[i][t] * g[j][t])
			}
			c[i][j] = s
		}
	}
	d := make([]float64, n)
	for i := range d {
		d[i] = math.Sqrt(c[i][i])
	}
	for i := range c {
		for j := range c[i] {
			c[i][j] = c[i][j] / (d[i] * d[j])
		}
		c[i][i] = 1
	}
	return c
}

func sameBits(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

func assertFactorBits(t *testing.T, label string, want [][]float64, got *linalg.Matrix) {
	t.Helper()
	n := len(want)
	if got.Rows() != n || got.Cols() != n {
		t.Fatalf("%s: factor is %dx%d, want %dx%d", label, got.Rows(), got.Cols(), n, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if !sameBits(want[i][j], got.At(i, j)) {
				t.Fatalf("%s: L[%d][%d] = %x, reference %x", label, i, j,
					math.Float64bits(got.At(i, j)), math.Float64bits(want[i][j]))
			}
		}
	}
}

// TestCholesky_BitIdenticalToSynthReference factors random SPD matrices
// of order 2..40 with linalg.Cholesky and with a verbatim copy of the
// synth copula's tryCholesky and requires every factor element to agree
// bit for bit.
func TestCholesky_BitIdenticalToSynthReference(t *testing.T) {
	rng := rand.New(rand.NewSource(15))
	for n := 2; n <= 40; n++ {
		for rep := 0; rep < 25; rep++ {
			m := randomCorrelation(rng, n, 1+rng.Intn(4))
			want, ok := refTryCholesky(m)
			s, err := linalg.NewSymFromRows(m)
			if err != nil {
				t.Fatal(err)
			}
			got, err := linalg.Cholesky(s)
			if ok != (err == nil) {
				t.Fatalf("n=%d rep=%d: reference ok=%v, linalg err=%v", n, rep, ok, err)
			}
			if ok {
				assertFactorBits(t, "spd", want, got)
			}
		}
	}
}

// TestCholeskyRidge_BitIdenticalToSynthReference drives the default
// ridge schedule over SPD and exactly rank-deficient unit-diagonal
// matrices (the inputs the synth ridge loop exists for) and requires the
// factor, the success decision and the accumulated ridge to match the
// reference loop bit for bit.
func TestCholeskyRidge_BitIdenticalToSynthReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1515))
	ridged := 0
	for n := 2; n <= 40; n++ {
		for rep := 0; rep < 25; rep++ {
			extra := 1
			if rep%2 == 1 {
				extra = -1 - rng.Intn(n-1) // rank-deficient
			}
			m := randomCorrelation(rng, n, extra)
			want, wantRidge, ok := refCholesky(m)
			s, err := linalg.NewSymFromRows(m)
			if err != nil {
				t.Fatal(err)
			}
			got, gotRidge, err := linalg.CholeskyRidge(s, nil)
			if ok != (err == nil) {
				t.Fatalf("n=%d rep=%d: reference ok=%v, linalg err=%v", n, rep, ok, err)
			}
			if !sameBits(wantRidge, gotRidge) {
				t.Fatalf("n=%d rep=%d: ridge %v, reference %v", n, rep, gotRidge, wantRidge)
			}
			if wantRidge > 0 {
				ridged++
			}
			if ok {
				assertFactorBits(t, "ridge", want, got)
			}
		}
	}
	if ridged == 0 {
		t.Fatal("no case exercised the ridge retry; the rank-deficient generator is broken")
	}
}
