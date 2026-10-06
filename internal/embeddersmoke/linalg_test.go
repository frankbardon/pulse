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
