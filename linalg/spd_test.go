package linalg_test

import (
	stderrors "errors"
	"math"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"gonum.org/v1/gonum/mat"
)

// The SPD path (FactorSPD, SPDFactor.Solve / Inverse, Mul) is an
// FMA-free port of gonum's amd64 operation order; its bit contract
// (equal to gonum on amd64, one pinned digest on every architecture) is
// TestSPD_MatchesGonumOrder / TestSPD_BitPinned in
// spd_gonumorder_test.go. The tests here pin its error and shape
// behaviour.

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

// TestFactorSPD_DoesNotAliasInput: the factor owns its storage.
func TestFactorSPD_DoesNotAliasInput(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

// TestSPDFactor_IllConditioned: past ConditionTolerance the factor still
// forms but the solve and the inverse refuse, as gonum's do, with
// PULSE_MATRIX_SINGULAR carrying the factor's own condition estimate in
// details.condition_number.
func TestSPDFactor_IllConditioned(t *testing.T) {
	t.Parallel()

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
	if c, ok := ce.Details["condition_number"].(float64); !ok || !sameBits(c, f.ConditionNumber()) {
		t.Errorf("solve details %v, want condition_number %v", ce.Details, f.ConditionNumber())
	}
	_, err = f.Inverse()
	ce = wantCode(t, err, perr.PULSE_MATRIX_SINGULAR)
	if c, ok := ce.Details["condition_number"].(float64); !ok || !sameBits(c, f.ConditionNumber()) {
		t.Errorf("inverse details %v, want condition_number %v", ce.Details, f.ConditionNumber())
	}
}

func TestSPDFactor_Shapes(t *testing.T) {
	t.Parallel()

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

func TestMul_Shapes(t *testing.T) {
	t.Parallel()

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
