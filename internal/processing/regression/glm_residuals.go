package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/reglink"
)

// glm_residuals.go is ATTR_REG_RESIDUAL params.kind (U65 E1-S1) over a
// bound fit: the residual of one row on the fit's response scale, read
// through the fit's error family and link exactly as R's residuals.glm
// (and statsmodels' GLMResults.resid_*) computes it:
//
//	response  y − μ
//	pearson   (y − μ) / √V(μ)
//	deviance  sign(y − μ) · √d(y, μ),  d the unit deviance
//	working   (y − μ) · g′(μ) = (y − μ) / (∂μ/∂η)
//
// The slot carries no prior weight (a bound fit scores rows, it does not
// refit them), so the Pearson and deviance forms are the unweighted
// ones, R's residuals on a fit without `weights`. An OLS or
// Bayesian-linear fit is Gaussian with the identity link: every kind is
// y − μ.

// ResidualFamily is the error family and link a residual kind reads,
// resolved once per slot. The zero value is Gaussian / identity.
type ResidualFamily struct {
	fam      glmFamily
	gaussian bool
}

// NewResidualFamily resolves family / link (a GLM fit's echo; empty for
// OLS and Bayesian-linear fits, which are Gaussian / identity) at the
// fit's θ (RegressionResult.Theta: the negative binomial's variance and
// deviance read it; ignored by every other family). An unknown pair is
// the engine's own refusal; a negative binomial without a positive θ is
// PROCESSING_INTERNAL (every such fit, and every model file of one,
// carries it).
func NewResidualFamily(family, link string, theta float64) (ResidualFamily, error) {
	if family == "" {
		return ResidualFamily{gaussian: true}, nil
	}
	fam, err := familyAtTheta(family, link, theta)
	if err != nil {
		return ResidualFamily{}, err
	}
	return ResidualFamily{fam: fam}, nil
}

// familyAtTheta is selectFamilyTheta for a bound fit's echo, refusing a
// negative binomial whose θ is not positive.
func familyAtTheta(family, link string, theta float64) (glmFamily, error) {
	if family == reglink.NegativeBinomial && !(theta > 0) {
		return glmFamily{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"a negative_binomial fit carries no theta; its residuals and influence cannot be read",
			map[string]any{"family": family, "link": link})
	}
	return selectFamilyTheta(family, link, theta)
}

// Residual returns y's residual of kind ("response" / "" , "pearson",
// "deviance", "working") at the fitted mean mu; NaN when the residual is
// undefined there (a zero variance or link derivative).
func (r ResidualFamily) Residual(kind string, y, mu float64) float64 {
	e := y - mu
	if r.gaussian || kind == "" || kind == "response" {
		return e
	}
	switch kind {
	case "pearson":
		v := r.fam.Variance(mu)
		if !(v > 0) {
			return math.NaN()
		}
		return e / math.Sqrt(v)
	case "deviance":
		d := 2 * r.fam.Deviance(y, mu)
		if d < 0 {
			d = 0 // rounding residue at y = μ
		}
		return math.Copysign(math.Sqrt(d), e)
	case "working":
		g := r.fam.DmuDeta(r.fam.LinkFunc(mu))
		if g == 0 {
			return math.NaN()
		}
		return e / g
	}
	return math.NaN()
}
