package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/internal/regmargins"
	"github.com/frankbardon/pulse/types"
)

// margins_resample.go: marginal effects under the Resample modifier (U69
// #639). Every replicate refit recomputes the block's effects from its
// own β, fitted design, prior weights and linear predictor — the
// replicate's AME (its rows' weighted mean), its MEM (at its rows'
// means) or its discrete contrast, on the terms the full fit reports as
// contrasts — and the effect's replicate spread replaces the delta-method
// standard error, exactly as the coefficient table's replicate spread
// replaces its analytical one:
//
//	jackknife: SE = √((g − 1)/g · Σ (θ₍ₖ₎ − θ̄)²), p the Wald z
//	bootstrap: SE the B − 1 sample SD,          p the percentile p
//
// the statistic estimate / SE, the interval the Wald normal one at
// conf_level, no DF — the resampled coefficient table's rules, so an
// effect that IS a coefficient (the link scale, an identity link, all of
// REG_OLS) reports the coefficient's SE, p and interval bit for bit. The
// estimate stays the full-data effect (the point estimate, as the
// coefficients stay the full-data β), and the block carries inference
// "resample".

// resampleMargins is mg with its inference replaced by run's replicate
// statistics over the effects (run.derivedSE / derivedP, one per
// regmargins.ResultKeys(mg) key); nil when mg is. The estimates, terms,
// contrasts and At are kept; the maps are fresh, so the base fit's block
// is never mutated.
func resampleMargins(mg *types.RegressionMarginsResult, run *replicateRun, level float64) *types.RegressionMarginsResult {
	if mg == nil {
		return nil
	}
	out := *mg
	keys := regmargins.ResultKeys(mg)
	n := len(keys)
	out.Estimates = make(map[string]float64, n)
	out.StdErrors = make(map[string]float64, n)
	out.Statistics = make(map[string]float64, n)
	out.PValues = make(map[string]float64, n)
	out.CILower = make(map[string]float64, n)
	out.CIUpper = make(map[string]float64, n)
	out.DF = 0
	out.ConfLevel = level
	out.Inference = types.MarginsInferenceResample
	inf := marginsInference{level: level}
	for i, k := range keys {
		se := math.NaN()
		if i < len(run.derivedSE) {
			se = run.derivedSE[i]
		}
		setMargin(&out, k, mg.Estimates[k], se, inf)
		if i < len(run.derivedP) {
			out.PValues[k] = run.derivedP[i]
		}
	}
	return &out
}

// marginsDerive is a built-in REG_OLS / REG_GLM resample's per-replicate
// hook (refitReplicate): the replicate refit's effect of every term of
// base (the full fit's block — its Terms and Contrasts) in Terms order.
// nil when spec carries no margins block. On the link scale and on an
// identity link (all of REG_OLS) the effect is the term's coefficient;
// otherwise glmMarginEstimates over the refit's design. predictors is
// the refit's active set (spec.Predictors: margins refuse selection).
func marginsDerive(spec *types.RegressionSpec, glm bool, fam glmFamily, predictors []string, base *types.RegressionMarginsResult) func(*refitResult) []float64 {
	if spec.Margins == nil || base == nil {
		return nil
	}
	terms := base.Terms
	if !glm || spec.Margins.EffectiveScale() == types.MarginsScaleLink || identityLink(fam) {
		return func(r *refitResult) []float64 {
			out := make([]float64, len(terms))
			for i, t := range terms {
				if j := slices.Index(predictors, t); j >= 0 && j < len(r.Slopes) {
					out[i] = r.Slopes[j]
				}
			}
			return out
		}
	}
	contrasts := base.Contrasts
	return func(r *refitResult) []float64 {
		beta := make([]float64, 0, len(r.Slopes)+1)
		beta = append(append(beta, r.Intercept), r.Slopes...)
		prior := r.Prior
		if prior == nil {
			prior = make([]float64, len(r.Eta))
			for i := range prior {
				prior[i] = 1
			}
		}
		return glmMarginEstimates(spec, fam, predictors, terms, contrasts, r.X, prior, r.Eta, beta)
	}
}

// glmMarginEstimates is the response-scale effect of every term (terms
// order) of a REG_GLM fit on a non-identity link — glmMargins' estimates
// by the same expressions in the same order, without the gradient or the
// covariance (a replicate needs no delta-method SE): β_j·m̄ (ame) or
// β_j·μ′(x̄ᵀβ) (mem), a term in contrasts its glmContrast. xRows the
// fitted design (n × k row-major, intercept first), prior the prior
// weights, eta the linear predictor, beta the coefficients.
func glmMarginEstimates(spec *types.RegressionSpec, fam glmFamily, predictors, terms, contrasts []string, xRows, prior, eta, beta []float64) []float64 {
	k := len(beta)
	var scale float64
	var xbar []float64
	if spec.Margins.EffectiveType() == types.MarginsMEM {
		xbar = designMeans(xRows, prior, k)
		etaBar := 0.0
		for c := 0; c < k; c++ {
			etaBar += float64(xbar[c] * beta[c])
		}
		scale = fam.DmuDeta(etaBar)
	} else {
		sw := 0.0
		for i := range eta {
			sw += prior[i]
		}
		for i := range eta {
			scale += float64(prior[i] * fam.DmuDeta(eta[i]))
		}
		scale /= sw
	}
	out := make([]float64, len(terms))
	for i, t := range terms {
		j := slices.Index(predictors, t) + 1
		if slices.Contains(contrasts, t) {
			out[i], _ = glmContrast(fam, xRows, prior, beta, xbar, k, j)
			continue
		}
		out[i] = float64(beta[j] * scale)
	}
	return out
}
