package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// ATTR_MAHALANOBIS — each row's Mahalanobis distance from the fit
// centre across the slot's p members, a fitted vector attribute
// (attribute_fitted.go):
//
//	D² = (x − μ)ᵀ S⁻¹ (x − μ)
//
// μ the (weighted) fit mean and S the ddof = 1 covariance on w*
// (fittedState.cov). params.output "p" returns the χ² upper tail of D²
// on p degrees of freedom instead (chiSquareSurvival) — the outlier
// p-value under joint normality.
//
// The singular DECISION is the one rank rule (rankDeficient: a zero or
// non-finite variance, or a smallest correlation-form eigenvalue within
// linalg.RankTolerance), shared with TEST_HOTELLING_T2 / TEST_MANOVA; a
// singular fit nulls every score with PULSE_MATRIX_SINGULAR, never a
// ridge. The VALUE is FMA-free: S's reference Cholesky factor L (once,
// at fit), then per row z = L⁻¹(x − μ) by forward substitution and
// D² = Σ zᵢ², summed in member order — non-negative by construction.
type mahalanobisModel struct {
	output string

	p    int
	mean []float64
	l    *linalg.Matrix
}

// newMahalanobisAttribute is ATTR_MAHALANOBIS's registry factory.
func newMahalanobisAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ATTR_MAHALANOBIS requires a non-nil spec")
	}
	cfg, cerr := vectors.MahalanobisParams(attributeSlotLabel(attr), attr.Params)
	if cerr != nil {
		return nil, cerr
	}
	return newFittedVectorAttribute(attr, schema, fittedSource{fitOn: cfg.FitOn, fit: cfg.Fit, labels: cfg.Labels}, &mahalanobisModel{output: cfg.Output})
}

// fit centres on the fit mean and factors the covariance.
func (m *mahalanobisModel) fit(st fittedState) *fitRefusal {
	cov := st.cov()
	singular := &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
		msg: "the fit covariance is singular (a member is constant or a linear combination of others); D² is undefined and every score is null"}
	if rankDeficient(cov) {
		return singular
	}
	sym, err := linalg.NewSymFromRows(cov)
	if err != nil {
		return singular
	}
	l, err := linalg.Cholesky(sym)
	if err != nil {
		return singular
	}
	m.p, m.mean, m.l = st.p, st.mean(), l
	return nil
}

// score is D² (or its χ² upper tail) for the complete row x.
func (m *mahalanobisModel) score(x []float64) float64 {
	z := make([]float64, m.p)
	d2 := 0.0
	for i := 0; i < m.p; i++ {
		sum := x[i] - m.mean[i]
		for k := 0; k < i; k++ {
			sum -= float64(m.l.At(i, k) * z[k])
		}
		z[i] = sum / m.l.At(i, i)
		d2 += float64(z[i] * z[i])
	}
	if m.output == vectors.MahalanobisP {
		return chiSquareSurvival(d2, float64(m.p))
	}
	return d2
}
