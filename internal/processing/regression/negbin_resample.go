package regression

import (
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/types"
)

// negbin_resample.go: a negative binomial under resample (U69 E3-S2,
// #638). Every replicate refit re-estimates θ on its own rows
// (glmRefit → fitNegBin, the full fit's alternation), so θ̂ is resampled
// like a coefficient: its replicate SE — the jackknife's
// √((g − 1)/g · Σ(θ̂₍ₖ₎ − θ̄)²) or the bootstrap's sample SD — replaces
// the base fit's 1/√I(θ̂) as theta_std_err, beside the coefficients'
// replicate SEs. θ itself stays the full-data θ̂ (as β does). A fixed θ
// is no estimate: no column, no theta_std_err.

// resampleTheta reports whether spec resamples an estimated θ.
func resampleTheta(spec *types.RegressionSpec) bool {
	return spec.Type == types.REG_GLM && spec.Family == reglink.NegativeBinomial && spec.Theta == 0
}

// withThetaDerive appends θ̂ as the LAST derived column of every
// replicate when spec resamples an estimated θ (after any margins
// columns, which resampleMargins reads by index from the front);
// otherwise derive unchanged.
func withThetaDerive(spec *types.RegressionSpec, derive func(*refitResult) []float64) func(*refitResult) []float64 {
	if !resampleTheta(spec) {
		return derive
	}
	return func(r *refitResult) []float64 {
		var out []float64
		if derive != nil {
			out = derive(r)
		}
		return append(out, r.Theta)
	}
}
