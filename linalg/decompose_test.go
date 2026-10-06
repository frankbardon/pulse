package linalg_test

import (
	"math"
	"math/rand"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"gonum.org/v1/gonum/mat"
)

// Gonum-backed routines carry no bit contract (asm kernels and FMA
// choices differ between arm64 and amd64), so every numeric assertion
// here is tolerance-based. The documented reconstruction tolerance is
//
//	reconTol(p, scale) = 64 · p · Epsilon · max(1, scale)
//
// — a backward-stable decomposition reproduces its input to O(p·ε·‖A‖),
// and the factor 64 absorbs the constant without admitting a real bug
// (which shows up as O(1) error).
func reconTol(p int, scale float64) float64 {
	return 64 * float64(p) * linalg.Epsilon * math.Max(1, scale)
}

func maxAbs(rows [][]float64) float64 {
	m := 0.0
	for _, r := range rows {
		for _, v := range r {
			m = math.Max(m, math.Abs(v))
		}
	}
	return m
}

func mustMatrix(t *testing.T, rows [][]float64) *linalg.Matrix {
	t.Helper()
	m, err := linalg.NewMatrixFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func randRows(rng *rand.Rand, r, c int) [][]float64 {
	out := make([][]float64, r)
	for i := range out {
		out[i] = make([]float64, c)
		for j := range out[i] {
			out[i][j] = rng.NormFloat64()
		}
	}
	return out
}

func randSymRows(rng *rand.Rand, n int) [][]float64 {
	a := randRows(rng, n, n)
	for i := 0; i < n; i++ {
		for j := 0; j < i; j++ {
			a[j][i] = a[i][j]
		}
	}
	return a
}

// column returns column j of m.
func column(m *linalg.Matrix, j int) []float64 {
	out := make([]float64, m.Rows())
	for i := range out {
		out[i] = m.At(i, j)
	}
	return out
}

// dominant mirrors the documented dominance rule independently of the
// package: lowest index within DominanceTolerance of the max magnitude.
func dominant(x []float64) int {
	mx := 0.0
	for _, v := range x {
		mx = math.Max(mx, math.Abs(v))
	}
	for i, v := range x {
		if math.Abs(v) >= mx*(1-linalg.DominanceTolerance) {
			return i
		}
	}
	return 0
}

func assertOrthonormalColumns(t *testing.T, m *linalg.Matrix, tol float64) {
	t.Helper()
	for a := 0; a < m.Cols(); a++ {
		for b := 0; b < m.Cols(); b++ {
			dot := 0.0
			for i := 0; i < m.Rows(); i++ {
				dot += m.At(i, a) * m.At(i, b)
			}
			want := 0.0
			if a == b {
				want = 1
			}
			if math.Abs(dot-want) > tol {
				t.Fatalf("columns %d,%d: dot %v, want %v", a, b, dot, want)
			}
		}
	}
}

func assertSignRule(t *testing.T, name string, m *linalg.Matrix) {
	t.Helper()
	for j := 0; j < m.Cols(); j++ {
		col := column(m, j)
		if d := dominant(col); col[d] <= 0 {
			t.Fatalf("%s column %d: dominant component %d is %v, want > 0", name, j, d, col[d])
		}
	}
}

func assertDescending(t *testing.T, v *linalg.Vec, tol float64) {
	t.Helper()
	for i := 1; i < v.Len(); i++ {
		if v.At(i) > v.At(i-1)+tol {
			t.Fatalf("values not descending at %d: %v", i, v.Slice())
		}
	}
}

// symEigenCases mixes random symmetric matrices with structured ones
// (repeated eigenvalues, rank-deficient, indefinite).
func symEigenCases() map[string][][]float64 {
	rng := rand.New(rand.NewSource(15))
	cases := map[string][][]float64{
		"1x1":            {{-3}},
		"spd3":           spd3,
		"rank deficient": rankDeficient,
		"indefinite":     indefinite,
		"repeated":       {{2, 0, 0, 0}, {0, 5, 0, 0}, {0, 0, 2, 0}, {0, 0, 0, 5}},
		"zero":           {{0, 0}, {0, 0}},
	}
	for _, n := range []int{2, 3, 5, 8, 13, 21} {
		cases["random n"+itoa(n)] = randSymRows(rng, n)
	}
	return cases
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestSymEigenReconstruction(t *testing.T) {
	for name, rows := range symEigenCases() {
		t.Run(name, func(t *testing.T) {
			n := len(rows)
			res, err := linalg.SymEigen(mustSym(t, rows))
			if err != nil {
				t.Fatal(err)
			}
			tol := reconTol(n, maxAbs(rows))
			assertOrthonormalColumns(t, res.Vectors, reconTol(n, 1))
			assertDescending(t, res.Values, tol)
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					got := 0.0
					for k := 0; k < n; k++ {
						got += res.Vectors.At(i, k) * res.Values.At(k) * res.Vectors.At(j, k)
					}
					if math.Abs(got-rows[i][j]) > tol {
						t.Fatalf("(VΛVᵀ)[%d][%d] = %v, want %v (tol %g)", i, j, got, rows[i][j], tol)
					}
				}
			}
			assertSignRule(t, "eigenvector", res.Vectors)
		})
	}
}

// TestSymEigenSignRule pins the flip: across the case set gonum itself
// returns at least one eigenvector with a NEGATIVE dominant component
// (asserted, so the case set cannot go stale), and every eigenvector
// SymEigen returns has a positive one.
func TestSymEigenSignRule(t *testing.T) {
	rawNegative := 0
	for name, rows := range symEigenCases() {
		n := len(rows)
		full := make([]float64, 0, n*n)
		for _, r := range rows {
			full = append(full, r...)
		}
		var eig mat.EigenSym
		if !eig.Factorize(mat.NewSymDense(n, full), true) {
			t.Fatalf("%s: gonum did not converge", name)
		}
		var v mat.Dense
		eig.VectorsTo(&v)
		for j := 0; j < n; j++ {
			col := mat.Col(nil, j, &v)
			if col[dominant(col)] < 0 {
				rawNegative++
			}
		}
		res, err := linalg.SymEigen(mustSym(t, rows))
		if err != nil {
			t.Fatal(err)
		}
		assertSignRule(t, name, res.Vectors)
	}
	if rawNegative == 0 {
		t.Fatal("no case where gonum returns a negative dominant component; the flip is untested")
	}
}

// TestSymEigenMagnitudeTie: the eigenvectors of [[2,1],[1,2]] are
// (1,1)/√2 and (1,-1)/√2 — both components tie on magnitude, so the
// lowest index (0) is dominant and must be positive.
func TestSymEigenMagnitudeTie(t *testing.T) {
	res, err := linalg.SymEigen(mustSym(t, [][]float64{{2, 1}, {1, 2}}))
	if err != nil {
		t.Fatal(err)
	}
	h := 1 / math.Sqrt2
	want := [][]float64{{h, h}, {h, -h}} // rows = components, cols = vectors
	wantValues := []float64{3, 1}
	for k := 0; k < 2; k++ {
		if math.Abs(res.Values.At(k)-wantValues[k]) > 1e-14 {
			t.Fatalf("value %d = %v, want %v", k, res.Values.At(k), wantValues[k])
		}
		for i := 0; i < 2; i++ {
			if math.Abs(res.Vectors.At(i, k)-want[i][k]) > 1e-14 {
				t.Fatalf("V[%d][%d] = %v, want %v", i, k, res.Vectors.At(i, k), want[i][k])
			}
		}
	}
}

// TestSymEigenTieOrder: repeated eigenvalues are ordered by original
// variable order — the dominant-component index of their eigenvector.
func TestSymEigenTieOrder(t *testing.T) {
	cases := []struct {
		name       string
		rows       [][]float64
		wantValues []float64
		wantDom    []int
	}{
		{
			name:       "diagonal pairs",
			rows:       [][]float64{{2, 0, 0, 0, 0}, {0, 5, 0, 0, 0}, {0, 0, 2, 0, 0}, {0, 0, 0, 5, 0}, {0, 0, 0, 0, 1}},
			wantValues: []float64{5, 5, 2, 2, 1},
			wantDom:    []int{1, 3, 0, 2, 4},
		},
		{
			name:       "triple",
			rows:       [][]float64{{7, 0, 0, 0}, {0, 7, 0, 0}, {0, 0, 9, 0}, {0, 0, 0, 7}},
			wantValues: []float64{9, 7, 7, 7},
			wantDom:    []int{2, 0, 1, 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := linalg.SymEigen(mustSym(t, tc.rows))
			if err != nil {
				t.Fatal(err)
			}
			for k, want := range tc.wantValues {
				if math.Abs(res.Values.At(k)-want) > 1e-13 {
					t.Fatalf("values %v, want %v", res.Values.Slice(), tc.wantValues)
				}
				if d := dominant(column(res.Vectors, k)); d != tc.wantDom[k] {
					t.Fatalf("vector %d dominant index %d, want %d", k, d, tc.wantDom[k])
				}
			}
		})
	}
}

func TestSymEigenEdgeCases(t *testing.T) {
	res, err := linalg.SymEigen(mustSym(t, nil))
	if err != nil || res.Values.Len() != 0 || res.Vectors.Rows() != 0 {
		t.Fatalf("empty: %v %v", res, err)
	}
	if _, err := linalg.SymEigen(nil); codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("nil must be a shape mismatch")
	}
	_, err = linalg.SymEigen(mustSym(t, [][]float64{{1, 0}, {math.NaN(), 1}}))
	if codeOf(t, err) != perr.PULSE_MATRIX_SINGULAR {
		t.Fatal("NaN must be PULSE_MATRIX_SINGULAR")
	}
}

func svdCases() map[string][][]float64 {
	rng := rand.New(rand.NewSource(16))
	cases := map[string][][]float64{
		"1x1":       {{-2}},
		"row":       {{3, -4}},
		"column":    {{3}, {-4}},
		"rank 1":    {{1, 2, 3}, {2, 4, 6}, {-1, -2, -3}},
		"zero":      {{0, 0}, {0, 0}, {0, 0}},
		"repeated":  {{0, 3, 0}, {3, 0, 0}, {0, 0, 1}},
		"duplicate": {{1, 1, 2}, {3, 3, 4}, {5, 5, 6}, {7, 7, 9}},
	}
	for _, d := range [][2]int{{2, 2}, {5, 3}, {3, 5}, {10, 4}, {4, 10}, {12, 12}} {
		cases["random "+itoa(d[0])+"x"+itoa(d[1])] = randRows(rng, d[0], d[1])
	}
	return cases
}

func TestSVDReconstruction(t *testing.T) {
	for name, rows := range svdCases() {
		t.Run(name, func(t *testing.T) {
			r, c := len(rows), len(rows[0])
			k := min(r, c)
			res, err := linalg.SVD(mustMatrix(t, rows))
			if err != nil {
				t.Fatal(err)
			}
			if res.U.Rows() != r || res.U.Cols() != k || res.V.Rows() != c || res.V.Cols() != k || res.Values.Len() != k {
				t.Fatalf("thin shapes: U %dx%d, V %dx%d, %d values", res.U.Rows(), res.U.Cols(), res.V.Rows(), res.V.Cols(), res.Values.Len())
			}
			p := max(r, c)
			tol := reconTol(p, maxAbs(rows))
			assertDescending(t, res.Values, tol)
			for i := 0; i < k; i++ {
				if res.Values.At(i) < 0 {
					t.Fatalf("negative singular value %v", res.Values.At(i))
				}
			}
			assertOrthonormalColumns(t, res.V, reconTol(p, 1))
			for i := 0; i < r; i++ {
				for j := 0; j < c; j++ {
					got := 0.0
					for q := 0; q < k; q++ {
						got += res.U.At(i, q) * res.Values.At(q) * res.V.At(j, q)
					}
					if math.Abs(got-rows[i][j]) > tol {
						t.Fatalf("(UΣVᵀ)[%d][%d] = %v, want %v (tol %g)", i, j, got, rows[i][j], tol)
					}
				}
			}
			assertSignRule(t, "V", res.V)
		})
	}
}

// TestSVDSignRule: the V column decides the sign and its U column is
// flipped with it. gonum must hand back at least one negative dominant V
// component across the case set (asserted), and for every pair with a
// non-zero singular value u_k must equal A·v_k / σ_k — which fails if U
// and V were flipped inconsistently.
func TestSVDSignRule(t *testing.T) {
	rawNegative := 0
	for name, rows := range svdCases() {
		r, c := len(rows), len(rows[0])
		full := make([]float64, 0, r*c)
		for _, row := range rows {
			full = append(full, row...)
		}
		var svd mat.SVD
		if !svd.Factorize(mat.NewDense(r, c, full), mat.SVDThin) {
			t.Fatalf("%s: gonum did not converge", name)
		}
		var v mat.Dense
		svd.VTo(&v)
		_, kc := v.Dims()
		for j := 0; j < kc; j++ {
			col := mat.Col(nil, j, &v)
			if col[dominant(col)] < 0 {
				rawNegative++
			}
		}
		res, err := linalg.SVD(mustMatrix(t, rows))
		if err != nil {
			t.Fatal(err)
		}
		assertSignRule(t, name, res.V)
		tol := reconTol(max(r, c), maxAbs(rows))
		for q := 0; q < res.Values.Len(); q++ {
			s := res.Values.At(q)
			if s <= tol {
				continue
			}
			for i := 0; i < r; i++ {
				av := 0.0
				for j := 0; j < c; j++ {
					av += rows[i][j] * res.V.At(j, q)
				}
				if math.Abs(av/s-res.U.At(i, q)) > tol/s+reconTol(max(r, c), 1) {
					t.Fatalf("%s pair %d: u[%d] = %v, A·v/σ = %v", name, q, i, res.U.At(i, q), av/s)
				}
			}
		}
	}
	if rawNegative == 0 {
		t.Fatal("no case where gonum returns a negative dominant V component; the flip is untested")
	}
}

func TestSVDTieOrder(t *testing.T) {
	// Singular values 3, 3, 1, 1 on a permuted diagonal: V columns are
	// unit vectors, ordered within each tie by variable index.
	rows := [][]float64{
		{0, 0, 1, 0},
		{0, 0, 0, 3},
		{1, 0, 0, 0},
		{0, 3, 0, 0},
	}
	res, err := linalg.SVD(mustMatrix(t, rows))
	if err != nil {
		t.Fatal(err)
	}
	wantValues := []float64{3, 3, 1, 1}
	wantDom := []int{1, 3, 0, 2}
	for k := range wantValues {
		if math.Abs(res.Values.At(k)-wantValues[k]) > 1e-13 {
			t.Fatalf("values %v, want %v", res.Values.Slice(), wantValues)
		}
		if d := dominant(column(res.V, k)); d != wantDom[k] {
			t.Fatalf("V column %d dominant index %d, want %d", k, d, wantDom[k])
		}
	}
}

func TestSVDEdgeCases(t *testing.T) {
	empty, _ := linalg.NewMatrix(3, 0, nil)
	res, err := linalg.SVD(empty)
	if err != nil || res.Values.Len() != 0 || res.U.Rows() != 3 || res.U.Cols() != 0 || res.V.Rows() != 0 {
		t.Fatalf("3x0: %+v %v", res, err)
	}
	if _, err := linalg.SVD(nil); codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("nil must be a shape mismatch")
	}
	_, err = linalg.SVD(mustMatrix(t, [][]float64{{1, math.Inf(1)}}))
	if codeOf(t, err) != perr.PULSE_MATRIX_SINGULAR {
		t.Fatal("Inf must be PULSE_MATRIX_SINGULAR")
	}
}

func TestQR(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	cases := map[string][][]float64{
		"1x1 negative": {{-5}},
		"tall":         {{1, 2}, {3, 4}, {5, 6}},
		"rank 1":       {{1, 2}, {2, 4}, {3, 6}},
		"random 6x6":   randRows(rng, 6, 6),
		"random 9x4":   randRows(rng, 9, 4),
		"random 20x7":  randRows(rng, 20, 7),
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			r, c := len(rows), len(rows[0])
			res, err := linalg.QR(mustMatrix(t, rows))
			if err != nil {
				t.Fatal(err)
			}
			if res.Q.Rows() != r || res.Q.Cols() != c || res.R.Rows() != c || res.R.Cols() != c {
				t.Fatalf("thin shapes: Q %dx%d, R %dx%d", res.Q.Rows(), res.Q.Cols(), res.R.Rows(), res.R.Cols())
			}
			tol := reconTol(r, maxAbs(rows))
			assertOrthonormalColumns(t, res.Q, reconTol(r, 1))
			for i := 0; i < c; i++ {
				if res.R.At(i, i) < 0 {
					t.Fatalf("R[%d][%d] = %v, want >= 0", i, i, res.R.At(i, i))
				}
				for j := 0; j < i; j++ {
					if res.R.At(i, j) != 0 {
						t.Fatalf("R[%d][%d] = %v below the diagonal", i, j, res.R.At(i, j))
					}
				}
			}
			for i := 0; i < r; i++ {
				for j := 0; j < c; j++ {
					got := 0.0
					for k := 0; k < c; k++ {
						got += res.Q.At(i, k) * res.R.At(k, j)
					}
					if math.Abs(got-rows[i][j]) > tol {
						t.Fatalf("(QR)[%d][%d] = %v, want %v (tol %g)", i, j, got, rows[i][j], tol)
					}
				}
			}
		})
	}
}

// TestQRSignRule: gonum's Householder QR of a positive first column
// gives R[0][0] < 0; the returned R must have it flipped positive.
func TestQRSignRule(t *testing.T) {
	rows := [][]float64{{3, 1}, {4, 2}}
	var raw mat.QR
	raw.Factorize(mat.NewDense(2, 2, []float64{3, 1, 4, 2}))
	var rr mat.Dense
	raw.RTo(&rr)
	if rr.At(0, 0) >= 0 {
		t.Fatalf("gonum R[0][0] = %v; the case no longer exercises the flip", rr.At(0, 0))
	}
	res, err := linalg.QR(mustMatrix(t, rows))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.R.At(0, 0)-5) > 1e-14 {
		t.Fatalf("R[0][0] = %v, want 5", res.R.At(0, 0))
	}
}

func TestQREdgeCases(t *testing.T) {
	_, err := linalg.QR(mustMatrix(t, [][]float64{{1, 2, 3}}))
	if codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("rows < cols must be a shape mismatch")
	}
	if _, err := linalg.QR(nil); codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("nil must be a shape mismatch")
	}
	_, err = linalg.QR(mustMatrix(t, [][]float64{{math.NaN()}}))
	if codeOf(t, err) != perr.PULSE_MATRIX_SINGULAR {
		t.Fatal("NaN must be PULSE_MATRIX_SINGULAR")
	}
	empty, _ := linalg.NewMatrix(2, 0, nil)
	res, err := linalg.QR(empty)
	if err != nil || res.Q.Rows() != 2 || res.Q.Cols() != 0 || res.R.Rows() != 0 {
		t.Fatalf("2x0: %+v %v", res, err)
	}
}

// nearSingular returns [[1,1],[1,1+δ]]: σ_min ≈ δ/2, κ ≈ 4/δ.
func nearSingular(delta float64) [][]float64 {
	return [][]float64{{1, 1}, {1, 1 + delta}}
}

func TestRank(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	cases := []struct {
		name string
		rows [][]float64
		tol  float64
		want int
	}{
		{name: "identity", rows: [][]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}, want: 3},
		{name: "random full 7x4", rows: randRows(rng, 7, 4), want: 4},
		{name: "random full 4x7", rows: randRows(rng, 4, 7), want: 4},
		{name: "rank 1", rows: [][]float64{{1, 2, 3}, {2, 4, 6}, {-1, -2, -3}}, want: 1},
		{name: "duplicate column", rows: [][]float64{{1, 1, 2}, {3, 3, 4}, {5, 5, 6}, {7, 7, 9}}, want: 2},
		{name: "zero", rows: [][]float64{{0, 0}, {0, 0}}, want: 0},
		// δ = 1e-10 sits far above max(p)·ε·σ_max ≈ 8.9e-16: full rank.
		{name: "near singular above tolerance", rows: nearSingular(1e-10), want: 2},
		// δ = 1e-15 puts σ_min ≈ 5e-16 below the default tolerance.
		{name: "near singular below tolerance", rows: nearSingular(1e-15), want: 1},
		{name: "explicit tolerance", rows: nearSingular(1e-10), tol: 1e-8, want: 1},
		{name: "negative tol means default", rows: nearSingular(1e-10), tol: -1, want: 2},
		{name: "NaN tol means default", rows: nearSingular(1e-15), tol: math.NaN(), want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := linalg.Rank(mustMatrix(t, tc.rows), tc.tol)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("rank %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRankEdgeCases(t *testing.T) {
	empty, _ := linalg.NewMatrix(0, 3, nil)
	if got, err := linalg.Rank(empty, 0); err != nil || got != 0 {
		t.Fatalf("empty: %d %v", got, err)
	}
	if _, err := linalg.Rank(nil, 0); codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("nil must be a shape mismatch")
	}
	_, err := linalg.Rank(mustMatrix(t, [][]float64{{math.NaN()}}), 0)
	if codeOf(t, err) != perr.PULSE_MATRIX_SINGULAR {
		t.Fatal("NaN must be PULSE_MATRIX_SINGULAR")
	}
}

func TestConditionNumber(t *testing.T) {
	cases := []struct {
		name   string
		rows   [][]float64
		want   float64
		relTol float64
	}{
		{name: "identity", rows: [][]float64{{1, 0}, {0, 1}}, want: 1, relTol: 1e-14},
		{name: "diagonal", rows: [][]float64{{4, 0}, {0, -1}}, want: 4, relTol: 1e-14},
		{name: "tall", rows: [][]float64{{2, 0}, {0, 1}, {0, 0}}, want: 2, relTol: 1e-14},
		// κ ≈ 4/δ; σ_min carries relative error ~ε·‖A‖/σ_min ≈ 1e-5.
		{name: "near singular", rows: nearSingular(1e-10), want: 4e10, relTol: 1e-4},
		{name: "rank deficient", rows: [][]float64{{1, 2}, {2, 4}}, want: math.Inf(1)},
		{name: "below tolerance", rows: nearSingular(1e-15), want: math.Inf(1)},
		{name: "zero", rows: [][]float64{{0, 0}, {0, 0}}, want: math.Inf(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := linalg.ConditionNumber(mustMatrix(t, tc.rows))
			if err != nil {
				t.Fatal(err)
			}
			if math.IsInf(tc.want, 1) {
				if !math.IsInf(got, 1) {
					t.Fatalf("κ = %v, want +Inf", got)
				}
				return
			}
			if math.Abs(got-tc.want) > tc.relTol*tc.want {
				t.Fatalf("κ = %v, want %v (rel tol %g)", got, tc.want, tc.relTol)
			}
		})
	}
}

func TestConditionNumberEdgeCases(t *testing.T) {
	if _, err := linalg.ConditionNumber(nil); codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("nil must be a shape mismatch")
	}
	empty, _ := linalg.NewMatrix(0, 0, nil)
	if _, err := linalg.ConditionNumber(empty); codeOf(t, err) != perr.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatal("empty must be a shape mismatch")
	}
	_, err := linalg.ConditionNumber(mustMatrix(t, [][]float64{{math.Inf(-1)}}))
	if codeOf(t, err) != perr.PULSE_MATRIX_SINGULAR {
		t.Fatal("Inf must be PULSE_MATRIX_SINGULAR")
	}
}
