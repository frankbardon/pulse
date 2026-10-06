package linalg_test

import (
	stderrors "errors"
	"math"
	"math/rand"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"gonum.org/v1/gonum/mat"
)

// The gonum-backed SPD path (FactorSPD, SPDFactor.Solve / Inverse, Mul)
// carries no CROSS-ARCHITECTURE bit contract, but it does carry a
// same-process one: it is a thin wrapper over gonum, so on any one
// machine its output must equal the raw gonum calls bit for bit. That is
// what lets the regression engine route through it without moving a
// single output bit. These tests compare against gonum in-process, so
// they hold on arm64 and amd64 alike without recording any bits.

// spdRows returns a random n×n symmetric positive-definite matrix
// (AᵀA + n·I) as full rows.
func spdRows(rng *rand.Rand, n int, ridge float64) [][]float64 {
	a := randRows(rng, n+3, n)
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, n)
		for j := range out[i] {
			s := 0.0
			for k := range a {
				s += a[k][i] * a[k][j]
			}
			out[i][j] = s
		}
		out[i][i] += ridge
	}
	return out
}

func flatten(rows [][]float64) []float64 {
	var out []float64
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

func wantCode(t *testing.T, err error, code perr.Code) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("error %v, want %s", err, code)
	}
	return ce
}

func TestFactorSPD_MatchesGonumBitwise(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	for _, n := range []int{1, 2, 3, 5, 8, 13} {
		for trial := 0; trial < 6; trial++ {
			rows := spdRows(rng, n, float64(trial)*0.01)
			b := randRows(rng, 1, n)[0]

			var ref mat.Cholesky
			if !ref.Factorize(mat.NewSymDense(n, flatten(rows))) {
				t.Fatalf("n=%d: fixture not SPD", n)
			}
			var refX mat.VecDense
			if err := ref.SolveVecTo(&refX, mat.NewVecDense(n, append([]float64(nil), b...))); err != nil {
				t.Fatal(err)
			}
			var refInv mat.SymDense
			if err := ref.InverseTo(&refInv); err != nil {
				t.Fatal(err)
			}

			f, err := linalg.FactorSPD(mustSym(t, rows))
			if err != nil {
				t.Fatalf("n=%d: FactorSPD: %v", n, err)
			}
			if f.N() != n {
				t.Fatalf("N() = %d, want %d", f.N(), n)
			}
			if !sameBits(f.ConditionNumber(), ref.Cond()) {
				t.Errorf("n=%d: condition %v, gonum %v", n, f.ConditionNumber(), ref.Cond())
			}
			x, err := f.Solve(linalg.NewVec(b))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if !sameBits(x.At(i), refX.AtVec(i)) {
					t.Fatalf("n=%d: x[%d] = %x, gonum %x", n, i, math.Float64bits(x.At(i)), math.Float64bits(refX.AtVec(i)))
				}
			}
			inv, err := f.Inverse()
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					if !sameBits(inv.At(i, j), refInv.At(i, j)) {
						t.Fatalf("n=%d: inv[%d][%d] = %x, gonum %x", n, i, j,
							math.Float64bits(inv.At(i, j)), math.Float64bits(refInv.At(i, j)))
					}
				}
			}
		}
	}
}

// TestFactorSPD_DoesNotAliasInput: the factor owns its storage.
func TestFactorSPD_DoesNotAliasInput(t *testing.T) {
	rows := [][]float64{{4, 2}, {2, 3}}
	s := mustSym(t, rows)
	f, err := linalg.FactorSPD(s)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := f.Solve(linalg.NewVec([]float64{1, 1}))
	s.Set(0, 0, 100)
	after, _ := f.Solve(linalg.NewVec([]float64{1, 1}))
	if !sameBits(before.At(0), after.At(0)) || !sameBits(before.At(1), after.At(1)) {
		t.Fatal("mutating the input Sym changed the factor")
	}
}

func TestFactorSPD_NotPositiveDefinite(t *testing.T) {
	for name, rows := range map[string][][]float64{
		"rank_deficient": {{1, 2}, {2, 4}},
		"indefinite":     {{1, 3}, {3, 1}},
		"zero":           {{0}},
		"nan":            {{1, math.NaN()}, {math.NaN(), 1}},
	} {
		_, err := linalg.FactorSPD(mustSym(t, rows))
		ce := wantCode(t, err, perr.PULSE_MATRIX_SINGULAR)
		if ce.Details["reason"] != "not_positive_definite" {
			t.Errorf("%s: details %v", name, ce.Details)
		}
	}
	_, err := linalg.FactorSPD(nil)
	wantCode(t, err, perr.PULSE_MATRIX_SHAPE_MISMATCH)
}

// TestSPDFactor_IllConditioned: past ConditionTolerance gonum still
// factors but flags the solve and the inverse; the wrapper reports
// PULSE_MATRIX_SINGULAR with gonum's condition estimate in
// details.condition_number, bit-equal to gonum's own Condition error.
func TestSPDFactor_IllConditioned(t *testing.T) {
	// The smallest representable bump over 1 keeps the matrix PD with a
	// condition number near 4/Epsilon ≈ 1.8e16.
	rows := [][]float64{{1, 1}, {1, 1 + linalg.Epsilon}}
	var ref mat.Cholesky
	if !ref.Factorize(mat.NewSymDense(2, flatten(rows))) {
		t.Fatal("fixture should factor")
	}
	var refX mat.VecDense
	refErr := ref.SolveVecTo(&refX, mat.NewVecDense(2, []float64{1, 2}))
	var cond mat.Condition
	if !stderrors.As(refErr, &cond) {
		t.Fatalf("fixture not ill-conditioned for gonum: %v (cond %v)", refErr, ref.Cond())
	}

	f, err := linalg.FactorSPD(mustSym(t, rows))
	if err != nil {
		t.Fatal(err)
	}
	if f.ConditionNumber() <= linalg.ConditionTolerance {
		t.Fatalf("condition %v not above tolerance", f.ConditionNumber())
	}
	_, err = f.Solve(linalg.NewVec([]float64{1, 2}))
	ce := wantCode(t, err, perr.PULSE_MATRIX_SINGULAR)
	if c, ok := ce.Details["condition_number"].(float64); !ok || !sameBits(c, float64(cond)) {
		t.Errorf("solve details %v, want condition_number %v", ce.Details, float64(cond))
	}
	_, err = f.Inverse()
	ce = wantCode(t, err, perr.PULSE_MATRIX_SINGULAR)
	if c, ok := ce.Details["condition_number"].(float64); !ok || !sameBits(c, float64(cond)) {
		t.Errorf("inverse details %v, want condition_number %v", ce.Details, float64(cond))
	}
}

func TestSPDFactor_Shapes(t *testing.T) {
	f, err := linalg.FactorSPD(mustSym(t, [][]float64{{2, 0}, {0, 2}}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Solve(linalg.NewVec([]float64{1}))
	wantCode(t, err, perr.PULSE_MATRIX_SHAPE_MISMATCH)
	_, err = f.Solve(nil)
	wantCode(t, err, perr.PULSE_MATRIX_SHAPE_MISMATCH)

	empty, err := linalg.FactorSPD(mustSym(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if x, err := empty.Solve(linalg.NewVec(nil)); err != nil || x.Len() != 0 {
		t.Fatalf("empty solve = %v, %v", x, err)
	}
	if inv, err := empty.Inverse(); err != nil || inv.N() != 0 {
		t.Fatalf("empty inverse = %v, %v", inv, err)
	}
}

func TestMul_MatchesGonumBitwise(t *testing.T) {
	rng := rand.New(rand.NewSource(43))
	for _, d := range [][3]int{{1, 1, 1}, {2, 3, 4}, {5, 5, 5}, {7, 3, 9}, {13, 13, 13}} {
		a := randRows(rng, d[0], d[1])
		b := randRows(rng, d[1], d[2])
		var ref mat.Dense
		ref.Mul(mat.NewDense(d[0], d[1], flatten(a)), mat.NewDense(d[1], d[2], flatten(b)))
		got, err := linalg.Mul(mustMatrix(t, a), mustMatrix(t, b))
		if err != nil {
			t.Fatal(err)
		}
		if got.Rows() != d[0] || got.Cols() != d[2] {
			t.Fatalf("%v: shape %dx%d", d, got.Rows(), got.Cols())
		}
		for i := 0; i < d[0]; i++ {
			for j := 0; j < d[2]; j++ {
				if !sameBits(got.At(i, j), ref.At(i, j)) {
					t.Fatalf("%v: [%d][%d] = %x, gonum %x", d, i, j,
						math.Float64bits(got.At(i, j)), math.Float64bits(ref.At(i, j)))
				}
			}
		}
	}
}

func TestMul_Shapes(t *testing.T) {
	a := mustMatrix(t, [][]float64{{1, 2}})
	_, err := linalg.Mul(a, a)
	wantCode(t, err, perr.PULSE_MATRIX_SHAPE_MISMATCH)
	_, err = linalg.Mul(nil, a)
	wantCode(t, err, perr.PULSE_MATRIX_SHAPE_MISMATCH)
	_, err = linalg.Mul(a, nil)
	wantCode(t, err, perr.PULSE_MATRIX_SHAPE_MISMATCH)

	// A zero inner dimension is the zero matrix of the outer shape.
	l, _ := linalg.NewMatrix(2, 0, nil)
	r, _ := linalg.NewMatrix(0, 3, nil)
	got, err := linalg.Mul(l, r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows() != 2 || got.Cols() != 3 {
		t.Fatalf("shape %dx%d", got.Rows(), got.Cols())
	}
	for _, v := range got.RowMajor() {
		if v != 0 {
			t.Fatalf("non-zero %v", got.RowMajor())
		}
	}
}
