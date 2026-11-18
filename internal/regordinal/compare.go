package regordinal

import (
	"slices"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
)

// compare.go is REG_ORDINAL's nested-model comparison figures
// (extend.CompareRegression; U68 E5-S1, #653): what a Compose `compare`
// entry over two ordinal fits reads — the terms (the predictors, then
// the thresholds "<lo>|<hi>"), θ̂ and the covariance the standard
// errors read (classical, or the hc0 / hc1 / cluster sandwich), the
// deviance −2ℓ̂ and the residual df N* − q (polr's df.residual: Σw less
// predictors + thresholds). The engine's likelihood ratio is then
// anova.polr's — the deviance change on χ²(added predictors) — and its
// Wald test reads the full fit's covariance over the tested predictors.
// The two fits' thresholds must carry the same names (the same level
// set), which the engine holds them to.

// CompareFigures reports the last fit's comparison figures.
func (m *model) CompareFigures() (extend.CompareOutput, error) {
	st := m.fitted
	if st == nil {
		return extend.CompareOutput{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_INTERNAL,
			string(Name)+": comparison figures requested before a successful fit", nil)
	}
	cov := make([][]float64, len(st.vcov))
	for i, row := range st.vcov {
		cov[i] = slices.Clone(row)
	}
	return extend.CompareOutput{
		Terms:        append(slices.Clone(m.spec.Predictors), st.thresholds...),
		Coefficients: slices.Clone(st.theta),
		Covariance:   cov,
		Deviance:     st.deviance,
		DFResidual:   st.dfResidual,
	}, nil
}
