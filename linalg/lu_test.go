package linalg_test

import (
	"math"
	"math/rand"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"gonum.org/v1/gonum/mat"
)

// luResidualOK reports whether x passes the documented round-trip
// tolerance of the reference LU solve:
//
//	‖A·x − b‖∞ ≤ 8 · n · Epsilon · (‖A‖∞ · ‖x‖∞ + ‖b‖∞)
//
// the normwise backward-error bound of partial-pivoting LU with a
// small-growth allowance.
func luResidualOK(a [][]float64, x, b []float64) (float64, float64, bool) {
	n := len(a)
	normA, normX, normB, res := 0.0, 0.0, 0.0, 0.0
	for i := 0; i < n; i++ {
		row, r := 0.0, -b[i]
		for j := 0; j < n; j++ {
			row += math.Abs(a[i][j])
			r += a[i][j] * x[j]
		}
		normA = math.Max(normA, row)
		normX = math.Max(normX, math.Abs(x[i]))
		normB = math.Max(normB, math.Abs(b[i]))
		res = math.Max(res, math.Abs(r))
	}
	bound := 8 * float64(n) * linalg.Epsilon * (normA*normX + normB)
	return res, bound, res <= bound
}

func TestSolve_KnownSolutions(t *testing.T) {
	cases := []struct {
		name string
		a    [][]float64
		b    []float64
		want []float64
	}{
		{"1x1", [][]float64{{4}}, []float64{2}, []float64{0.5}},
		{"diagonal", [][]float64{{2, 0}, {0, 4}}, []float64{2, 8}, []float64{1, 2}},
		// a[0][0] = 0: Doolittle without pivoting divides by zero.
		{"zero leading pivot", [][]float64{{0, 1}, {1, 0}}, []float64{3, 5}, []float64{5, 3}},
		{"non-symmetric 2x2", [][]float64{{1, 2}, {3, 4}}, []float64{5, 11}, []float64{1, 2}},
		// Pivoting must swap rows twice (column 0 picks row 2, column 1 row 2 again).
		{"pivot-requiring 3x3", [][]float64{{1, 2, 3}, {2, 5, 3}, {4, 0, 8}}, []float64{14, 21, 28}, []float64{1, 2, 3}},
		{"indefinite symmetric", [][]float64{{0, 2}, {2, 0}}, []float64{4, 6}, []float64{3, 2}},
		// Markov absorption time: 3 transient states of a fair gambler's
		// ruin, (I − Q)·t = 1, t = (3, 4, 3).
		{"absorption time", [][]float64{{1, -0.5, 0}, {-0.5, 1, -0.5}, {0, -0.5, 1}}, []float64{1, 1, 1}, []float64{3, 4, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x, err := linalg.Solve(mustMatrix(t, tc.a), linalg.NewVec(tc.b))
			if err != nil {
				t.Fatal(err)
			}
			for i, w := range tc.want {
				if !(math.Abs(x.At(i)-w) <= 1e-13*math.Max(1, math.Abs(w))) { // NaN fails too
					t.Fatalf("x = %v, want %v", x.Slice(), tc.want)
				}
			}
		})
	}
}

func TestSolve_RoundTripRandomNonSymmetric(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 3, 5, 8, 13, 21, 40} {
		for trial := 0; trial < 20; trial++ {
			a := randRows(rng, n, n)
			b := make([]float64, n)
			for i := range b {
				b[i] = rng.NormFloat64() * 10
			}
			x, err := linalg.Solve(mustMatrix(t, a), linalg.NewVec(b))
			if err != nil {
				t.Fatalf("n=%d trial=%d: %v", n, trial, err)
			}
			if res, bound, ok := luResidualOK(a, x.Slice(), b); !ok {
				t.Fatalf("n=%d trial=%d: residual %g > bound %g", n, trial, res, bound)
			}
			// Agrees with gonum's LU solve to within the conditioning.
			var g mat.VecDense
			if err := g.SolveVec(mat.NewDense(n, n, flatten(a)), mat.NewVecDense(n, b)); err != nil {
				t.Fatal(err)
			}
			cond, _ := linalg.ConditionNumber(mustMatrix(t, a))
			for i := 0; i < n; i++ {
				if d := math.Abs(x.At(i) - g.AtVec(i)); !(d <= 64*cond*linalg.Epsilon*math.Max(1, math.Abs(g.AtVec(i)))) {
					t.Fatalf("n=%d: x[%d] = %g, gonum %g (cond %g)", n, i, x.At(i), g.AtVec(i), cond)
				}
			}
		}
	}
}

// TestSolve_BitPinned records the exact bits of one non-symmetric,
// pivot-requiring solve. The reference kernel is FMA-free, so these bits
// must hold on arm64 AND amd64 (CI); a drift here means a product lost
// its float64(...) guard or the operation order changed.
func TestSolve_BitPinned(t *testing.T) {
	a := mustMatrix(t, [][]float64{
		{0.3, -1.7, 2.9, 0.11},
		{4.1, 0.2, -0.6, 1.3},
		{-2.2, 3.3, 0.7, -0.9},
		{1.9, -0.4, 5.1, 2.6},
	})
	x, err := linalg.Solve(a, linalg.NewVec([]float64{1.1, -2.3, 0.7, 3.9}))
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{0xbfef34f4bee9daa0, 0xbfbbb610179c9ca9, 0x3fd70e46c270768c, 0x3ff7d471a62e0579}
	for i, w := range want {
		if got := math.Float64bits(x.At(i)); got != w {
			t.Errorf("x[%d] bits %#016x (%v), want %#016x", i, got, x.At(i), w)
		}
	}
}

func TestFactorLU_SingularIsTypedError(t *testing.T) {
	cases := []struct {
		name     string
		a        [][]float64
		reason   string
		wantRank int // -1: no rank key
	}{
		{"zero matrix", [][]float64{{0, 0}, {0, 0}}, "singular", 0},
		{"dependent rows", [][]float64{{1, 2, 3}, {2, 4, 6}, {1, 0, 1}}, "singular", 2},
		{"dependent columns non-symmetric", [][]float64{{1, 1, 2}, {3, -1, 2}, {0, 5, 5}}, "singular", 2},
		{"NaN element", [][]float64{{1, math.NaN()}, {0, 1}}, "non_finite", -1},
		{"Inf element", [][]float64{{1, 0}, {math.Inf(1), 1}}, "non_finite", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := mustMatrix(t, tc.a)
			_, err := linalg.FactorLU(a)
			ce := wantCode(t, err, perr.PULSE_MATRIX_SINGULAR)
			if ce.Details["reason"] != tc.reason {
				t.Fatalf("reason %v, want %s", ce.Details["reason"], tc.reason)
			}
			if tc.wantRank >= 0 {
				if ce.Details["rank"] != tc.wantRank {
					t.Fatalf("rank %v, want %d", ce.Details["rank"], tc.wantRank)
				}
				if c, _ := ce.Details["condition_number"].(float64); !math.IsInf(c, 1) {
					t.Fatalf("condition_number %v, want +Inf", ce.Details["condition_number"])
				}
			}
			// Solve refuses the same way — never a NaN result.
			x, err := linalg.Solve(a, linalg.NewVec(make([]float64, len(tc.a))))
			if x != nil {
				t.Fatalf("Solve returned %v for a singular matrix", x.Slice())
			}
			wantCode(t, err, perr.PULSE_MATRIX_SINGULAR)
		})
	}
}

// TestFactorLU_NearSingularFollowsRankTolerance pins the conditioning
// policy on [[1, 1], [1, 1+δ]]: the LU refuses exactly where the ONE
// rank tolerance calls the matrix rank-deficient (ConditionNumber = +Inf),
// and solves — within the round-trip tolerance — where it does not.
func TestFactorLU_NearSingularFollowsRankTolerance(t *testing.T) {
	for _, delta := range []float64{0, linalg.Epsilon, 2 * linalg.Epsilon, 3 * linalg.Epsilon, 4 * linalg.Epsilon, 5 * linalg.Epsilon, 6 * linalg.Epsilon,
		8 * linalg.Epsilon, 16 * linalg.Epsilon, 1e-14, 1e-12, 1e-8} {
		rows := [][]float64{{1, 1}, {1, 1 + delta}}
		a := mustMatrix(t, rows)
		cond, err := linalg.ConditionNumber(a)
		if err != nil {
			t.Fatal(err)
		}
		b := []float64{2, 2 + delta}
		x, err := linalg.Solve(a, linalg.NewVec(b))
		if math.IsInf(cond, 1) {
			if err == nil {
				t.Fatalf("δ=%g: rank-deficient (cond +Inf) but Solve returned %v", delta, x.Slice())
			}
			wantCode(t, err, perr.PULSE_MATRIX_SINGULAR)
			continue
		}
		if err != nil {
			t.Fatalf("δ=%g: cond %g is finite but Solve refused: %v", delta, cond, err)
		}
		if res, bound, ok := luResidualOK(rows, x.Slice(), b); !ok {
			t.Fatalf("δ=%g: residual %g > bound %g", delta, res, bound)
		}
	}
}

// An ill-conditioned but full-rank matrix (Hilbert 10, κ ≈ 1.6e13)
// solves: the policy refuses singularity, not poor conditioning, and the
// backward error still meets the round-trip tolerance.
func TestSolve_IllConditionedFullRankSolves(t *testing.T) {
	const n = 10
	rows := make([][]float64, n)
	b := make([]float64, n)
	for i := range rows {
		rows[i] = make([]float64, n)
		for j := range rows[i] {
			rows[i][j] = 1 / float64(i+j+1)
			b[i] += rows[i][j]
		}
	}
	a := mustMatrix(t, rows)
	cond, err := linalg.ConditionNumber(a)
	if err != nil || math.IsInf(cond, 1) || cond < 1e12 {
		t.Fatalf("Hilbert condition %g (%v), want large and finite", cond, err)
	}
	x, err := linalg.Solve(a, linalg.NewVec(b))
	if err != nil {
		t.Fatal(err)
	}
	if res, bound, ok := luResidualOK(rows, x.Slice(), b); !ok {
		t.Fatalf("residual %g > bound %g", res, bound)
	}
}

func TestLUFactor_ShapeErrors(t *testing.T) {
	sq := mustMatrix(t, [][]float64{{1, 2}, {3, 4}})
	rect := mustMatrix(t, [][]float64{{1, 2, 3}, {4, 5, 6}})
	tall := mustMatrix(t, [][]float64{{1}, {2}, {3}})
	f, err := linalg.FactorLU(sq)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"FactorLU nil", func() error { _, err := linalg.FactorLU(nil); return err }},
		{"FactorLU non-square", func() error { _, err := linalg.FactorLU(rect); return err }},
		{"Solve nil matrix", func() error { _, err := linalg.Solve(nil, linalg.NewVec([]float64{1})); return err }},
		{"Solve nil vector", func() error { _, err := linalg.Solve(sq, nil); return err }},
		{"Solve non-square", func() error { _, err := linalg.Solve(rect, linalg.NewVec([]float64{1, 2})); return err }},
		{"Solve length", func() error { _, err := linalg.Solve(sq, linalg.NewVec([]float64{1, 2, 3})); return err }},
		{"factor Solve nil", func() error { _, err := f.Solve(nil); return err }},
		{"factor Solve length", func() error { _, err := f.Solve(linalg.NewVec([]float64{1})); return err }},
		{"SolveMatrix nil", func() error { _, err := f.SolveMatrix(nil); return err }},
		{"SolveMatrix rows", func() error { _, err := f.SolveMatrix(tall); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantCode(t, tc.run(), perr.PULSE_MATRIX_SHAPE_MISMATCH)
		})
	}
}

func TestLUFactor_MultipleRHSAndInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const n = 6
	rows := randRows(rng, n, n)
	a := mustMatrix(t, rows)
	f, err := linalg.FactorLU(a)
	if err != nil {
		t.Fatal(err)
	}
	if f.N() != n {
		t.Fatalf("N() = %d", f.N())
	}
	if c, _ := linalg.ConditionNumber(a); f.ConditionNumber() != c {
		t.Fatalf("factor ConditionNumber %g, ConditionNumber %g", f.ConditionNumber(), c)
	}
	B := mustMatrix(t, randRows(rng, n, 3))
	X, err := f.SolveMatrix(B)
	if err != nil {
		t.Fatal(err)
	}
	if X.Rows() != n || X.Cols() != 3 {
		t.Fatalf("X is %dx%d", X.Rows(), X.Cols())
	}
	for j := 0; j < 3; j++ {
		col := make([]float64, n)
		for i := range col {
			col[i] = B.At(i, j)
		}
		x, err := f.Solve(linalg.NewVec(col))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if math.Float64bits(x.At(i)) != math.Float64bits(X.At(i, j)) {
				t.Fatalf("column %d row %d: SolveMatrix %v != Solve %v", j, i, X.At(i, j), x.At(i))
			}
		}
	}
	// One-shot Solve equals factor-then-solve bit for bit.
	col := make([]float64, n)
	for i := range col {
		col[i] = B.At(i, 0)
	}
	one, err := linalg.Solve(a, linalg.NewVec(col))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if math.Float64bits(one.At(i)) != math.Float64bits(X.At(i, 0)) {
			t.Fatalf("Solve vs factor row %d", i)
		}
	}
	inv, err := f.Inverse()
	if err != nil {
		t.Fatal(err)
	}
	prod, err := linalg.Mul(a, inv)
	if err != nil {
		t.Fatal(err)
	}
	cond, _ := linalg.ConditionNumber(a)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			want := 0.0
			if i == j {
				want = 1
			}
			if !(math.Abs(prod.At(i, j)-want) <= 64*cond*linalg.Epsilon) {
				t.Fatalf("A·A⁻¹[%d][%d] = %g", i, j, prod.At(i, j))
			}
		}
	}
	// The factor owns its storage.
	a.Set(0, 0, 1e9)
	again, err := f.Solve(linalg.NewVec(col))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if math.Float64bits(again.At(i)) != math.Float64bits(one.At(i)) {
			t.Fatal("mutating the source matrix changed the factor")
		}
	}
}

func TestLUFactor_Empty(t *testing.T) {
	empty := mustMatrix(t, nil)
	x, err := linalg.Solve(empty, linalg.NewVec(nil))
	if err != nil || x.Len() != 0 {
		t.Fatalf("empty solve = %v, %v", x, err)
	}
	f, err := linalg.FactorLU(empty)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := f.Inverse()
	if err != nil || inv.Rows() != 0 || inv.Cols() != 0 {
		t.Fatalf("empty inverse = %v, %v", inv, err)
	}
}
