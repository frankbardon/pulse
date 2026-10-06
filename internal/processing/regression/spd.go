package regression

import (
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// Every regression solve and inverse runs on linalg's gonum-backed SPD
// path (linalg.FactorSPD / SPDFactor.Solve / SPDFactor.Inverse /
// linalg.Mul). Those are thin wrappers over the gonum calls this package
// made directly before, on identical operands, so every REG_* output is
// bit-identical to the direct-gonum engine on the same machine. The
// helpers below keep the operands identical: a factor reads the UPPER
// triangle of the row-major Gram exactly as gonum's SymDense did.

// upperSym packs the upper triangle of the n×n row-major data into a
// linalg.Sym — the elements a gonum SymDense over the same slice
// consulted. The lower triangle is ignored.
func upperSym(n int, rowMajor []float64) *linalg.Sym {
	packed := make([]float64, 0, n*(n+1)/2)
	for i := 0; i < n; i++ {
		packed = append(packed, rowMajor[i*n+i:(i+1)*n]...)
	}
	s, err := linalg.NewSym(n, packed)
	if err != nil {
		// Unreachable: packed holds exactly n(n+1)/2 elements.
		panic("regression: upperSym: " + err.Error())
	}
	return s
}

// factorSPD factors the n×n row-major Gram, reporting only whether it is
// positive definite (callers raise their own operator-specific
// PROCESSING_REGRESSION_RANK_DEFICIENT).
func factorSPD(n int, rowMajor []float64) (*linalg.SPDFactor, bool) {
	f, err := linalg.FactorSPD(upperSym(n, rowMajor))
	return f, err == nil
}

// solveSPD solves f·x = b, returning x as a slice.
func solveSPD(f *linalg.SPDFactor, b []float64) ([]float64, error) {
	x, err := f.Solve(linalg.NewVec(b))
	if err != nil {
		return nil, err
	}
	return x.Slice(), nil
}

// backendErrorText renders a failed solve / inverse for the
// "gonum_error" detail regression errors carry. The detail key and its
// text predate the linalg routing and are kept byte-identical: the text
// is gonum's condition report, rebuilt from linalg's
// details.condition_number with gonum's own format.
func backendErrorText(err error) string {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		if c, ok := ce.Details["condition_number"].(float64); ok {
			return fmt.Sprintf("matrix singular or near-singular with condition number %.4e", c)
		}
	}
	return err.Error()
}
