package embeddersmoke

import (
	stderrors "errors"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// TestLinalgFlow drives the public linalg package the way an embedder
// would: build a symmetric matrix, factor, solve and invert it, and
// recognise the coded singular error.
func TestLinalgFlow(t *testing.T) {
	s, err := linalg.NewSymFromRows([][]float64{{4, 2}, {2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	L, err := linalg.Cholesky(s)
	if err != nil || L.At(0, 0) != 2 || L.At(1, 0) != 1 {
		t.Fatalf("Cholesky: %v %v", L, err)
	}
	x, err := linalg.SolveSPD(s, linalg.NewVec([]float64{8, 7}))
	if err != nil || x.Len() != 2 {
		t.Fatalf("SolveSPD: %v %v", x, err)
	}
	if _, err := linalg.InverseSPD(s); err != nil {
		t.Fatal(err)
	}

	singular, _ := linalg.NewSymFromRows([][]float64{{1, 1}, {1, 1}})
	_, err = linalg.Cholesky(singular)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_MATRIX_SINGULAR {
		t.Fatalf("want PULSE_MATRIX_SINGULAR, got %v", err)
	}
	if _, ridge, err := linalg.CholeskyRidge(singular, nil); err != nil || ridge <= 0 {
		t.Fatalf("CholeskyRidge: ridge=%v err=%v", ridge, err)
	}
}

// TestLinalgDecompositionFlow drives the gonum-backed decompositions
// through the public spellings only: eigen, SVD, QR, rank and condition
// number, with no gonum type in sight.
func TestLinalgDecompositionFlow(t *testing.T) {
	s, _ := linalg.NewSymFromRows([][]float64{{2, 1}, {1, 2}})
	eig, err := linalg.SymEigen(s)
	if err != nil || eig.Values.Len() != 2 || eig.Values.At(0) < eig.Values.At(1) || eig.Vectors.At(0, 0) <= 0 {
		t.Fatalf("SymEigen: %+v %v", eig, err)
	}
	m, _ := linalg.NewMatrixFromRows([][]float64{{1, 2}, {3, 4}, {5, 6}})
	svd, err := linalg.SVD(m)
	if err != nil || svd.U.Rows() != 3 || svd.V.Rows() != 2 || svd.Values.Len() != 2 {
		t.Fatalf("SVD: %+v %v", svd, err)
	}
	qr, err := linalg.QR(m)
	if err != nil || qr.Q.Cols() != 2 || qr.R.At(0, 0) <= 0 {
		t.Fatalf("QR: %+v %v", qr, err)
	}
	if r, err := linalg.Rank(m, 0); err != nil || r != 2 {
		t.Fatalf("Rank: %d %v", r, err)
	}
	dup, _ := linalg.NewMatrixFromRows([][]float64{{1, 1}, {2, 2}})
	if k, err := linalg.ConditionNumber(dup); err != nil || k <= 1e300 {
		t.Fatalf("ConditionNumber of a rank-deficient matrix: %v %v", k, err)
	}
}

// TestLinalgCoMomentFlow accumulates two partial CoMoments the way a
// worker pool would, merges them, and reads the weighted figures back
// through the public spellings; a mode mismatch is the coded shape error.
func TestLinalgCoMomentFlow(t *testing.T) {
	a, err := linalg.NewCoMoment(2, linalg.Listwise)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := linalg.NewCoMoment(2, linalg.Listwise)
	a.Add([]float64{1, 2}, 1)
	a.Add([]float64{2, 4}, 1)
	b.Add([]float64{3, 6}, 2)
	b.Add([]float64{9, 9}, -1) // invalid weight: skipped and counted
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if a.N() != 3 || a.W() != 4 || a.NWeightInvalid() != 1 || a.Mean().At(0) != 2.25 {
		t.Fatalf("merged: N=%d W=%v invalid=%d mean=%v", a.N(), a.W(), a.NWeightInvalid(), a.Mean().At(0))
	}
	if r := a.Corr().At(0, 1); r < 0.999999 {
		t.Fatalf("corr %v", r)
	}
	if a.Cov(1).N() != 2 || a.NEff() <= 0 || a.PairN(0, 1) != 3 {
		t.Fatal("cov / neff / pair reads")
	}
	p, _ := linalg.NewCoMoment(2, linalg.Pairwise)
	var ce *perrors.CodedError
	if err := a.Merge(p); !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_MATRIX_SHAPE_MISMATCH {
		t.Fatalf("want PULSE_MATRIX_SHAPE_MISMATCH, got %v", err)
	}
}
