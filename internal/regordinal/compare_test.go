package regordinal

import (
	stderrors "errors"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCompareFigures_FromTheLastFit: the figures are the last fit's —
// predictors then thresholds, θ̂, a copy of the covariance, −2ℓ̂ and
// N* − q — and asking before any fit is an invariant violation
// (PROCESSING_INTERNAL), never a zero figure. The values themselves are
// pinned to anova.polr end to end (internal/service
// TestReferenceOracle_NestedOrdinalMatchesR).
func TestCompareFigures_FromTheLastFit(t *testing.T) {
	t.Parallel()

	m := &model{spec: &types.RegressionSpec{Type: Name, Target: "y", Predictors: []string{"x1", "x2"}}}
	_, err := m.CompareFigures()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_INTERNAL {
		t.Fatalf("before a fit: %v", err)
	}
	vcov := [][]float64{{1, 0.1, 0, 0}, {0.1, 2, 0, 0}, {0, 0, 3, 0.2}, {0, 0, 0.2, 4}}
	m.fitted = &fitState{theta: []float64{0.5, -0.4, -1, 1}, vcov: vcov, thresholds: []string{"a|b", "b|c"}, deviance: 123.5, dfResidual: 96}
	out, err := m.CompareFigures()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Terms, []string{"x1", "x2", "a|b", "b|c"}) || !slices.Equal(out.Coefficients, m.fitted.theta) ||
		out.Deviance != 123.5 || out.DFResidual != 96 || len(out.Covariance) != 4 || out.Covariance[1][0] != 0.1 {
		t.Fatalf("figures %+v", out)
	}
	out.Covariance[0][0] = 99
	if vcov[0][0] != 1 {
		t.Fatal("CompareFigures aliases the fit's covariance")
	}
}
