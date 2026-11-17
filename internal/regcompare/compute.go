package regcompare

import (
	"maps"
	"math"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// Compute runs comparison p over its two finished fits (the batch's
// ComposedResponse.Fits results for p.Restricted and p.Full) and the
// comparison figures each fit's engine offered (model.RegressionFigures,
// g0 / g1 — internal only, never on a result). A fit that failed on its
// data, or unequal row counts, fail the comparison alone: the result
// carries Error (the fit's own code with details.fit, or
// PULSE_COMPARISON_ROWS_DIFFER) and a nil error. A fitted fit without
// its figures — a runtime that did not capture them — is an invariant
// violation (PROCESSING_INTERNAL).
func Compute(p Plan, restricted, full *types.RegressionResult, g0, g1 *model.RegressionFigures) (types.FitComparisonResult, error) {
	out := types.FitComparisonResult{Name: p.Name, Restricted: p.Restricted, Full: p.Full, Test: p.Test}
	for _, side := range []struct {
		name string
		res  *types.RegressionResult
		figs *model.RegressionFigures
	}{{p.Restricted, restricted, g0}, {p.Full, full, g1}} {
		if side.res == nil {
			return out, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"comparison "+p.Name+" reads fit "+side.name+", which produced no result",
				map[string]any{"compare": p.Index, "fit": side.name})
		}
		if se := side.res.Error; se != nil {
			// The fit failed on its data: the comparison fails alone
			// with the fit's own code, located by details.fit.
			details := maps.Clone(se.Details)
			if details == nil {
				details = map[string]any{}
			}
			details["fit"] = side.name
			out.Error = &types.SlotError{Code: se.Code, Message: "fit " + side.name + " failed: " + se.Message, Details: details}
			return out, nil
		}
		if side.figs == nil {
			return out, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"comparison "+p.Name+" reads fit "+side.name+", which carries no comparison figures",
				map[string]any{"compare": p.Index, "fit": side.name})
		}
	}
	if restricted.NObs != full.NObs || restricted.SumWeights != full.SumWeights {
		out.Error = &types.SlotError{
			Code:    string(errors.PULSE_COMPARISON_ROWS_DIFFER),
			Message: "fits " + p.Restricted + " and " + p.Full + " used different rows, so they cannot be compared",
			Details: map[string]any{
				"restricted": p.Restricted, "full": p.Full,
				"restricted_n_obs": restricted.NObs, "full_n_obs": full.NObs,
			},
		}
		return out, nil
	}
	// The tested coefficients: each tested term's keys in the full
	// fit's figures (REG_MULTINOMIAL's one per non-reference category;
	// the term itself elsewhere), in the figures' order.
	tested := testedKeys(g1, p.Terms)
	if p.Declared {
		if r0, r1 := otherTerms(g0, nil), otherTerms(g1, tested); !slices.Equal(r0, r1) {
			// A declared model's non-predictor terms (an ordinal
			// model's thresholds; a multinomial fit's restricted keys,
			// named by its categories) are named by its fitted levels:
			// the fits estimated different ones, so they are not
			// nested.
			out.Error = &types.SlotError{
				Code:    string(errors.PULSE_COMPARISON_ROWS_DIFFER),
				Message: "fits " + p.Restricted + " and " + p.Full + " estimated different terms beside the tested ones (outcome levels), so they cannot be compared",
				Details: map[string]any{
					"reason":     "terms_differ",
					"restricted": p.Restricted, "full": p.Full,
					"restricted_terms": r0, "full_terms": r1,
				},
			}
			return out, nil
		}
	}
	// q counts the tested coefficients: len(p.Terms) but on
	// REG_MULTINOMIAL, (K − 1)·len(p.Terms) — anova.multinom's Df.
	q := float64(len(tested))
	out.Distribution = p.Distribution
	out.Terms = slices.Clone(p.Terms)
	out.DF = q
	out.NObs = full.NObs
	out.RestrictedDFResidual, out.FullDFResidual = g0.DFResidual, g1.DFResidual
	out.RestrictedDeviance, out.FullDeviance = g0.Deviance, g1.Deviance
	switch p.Test {
	case types.ComparisonTestF:
		// anova: ((D₀ − D₁)/q)/φ̂₁ on (q, df₁).
		phi := g1.Dispersion
		out.Dispersion = phi
		out.DFResidual = g1.DFResidual
		out.Statistic = math.NaN()
		if g1.DFResidual > 0 {
			out.Statistic = ((g0.Deviance - g1.Deviance) / q) / phi
		}
		out.PValue = statdist.FSurvival(out.Statistic, q, g1.DFResidual)
	case types.ComparisonTestLRT:
		switch {
		case p.LogLik:
			// anova.negbin: 2ℓ₁ − 2ℓ₀, each model at its own θ̂.
			out.RestrictedTwoLogLik, out.FullTwoLogLik = restricted.TwoLogLik, full.TwoLogLik
			out.Statistic = full.TwoLogLik - restricted.TwoLogLik
		default:
			phi := 1.0
			if !p.FixedDispersion {
				phi = g1.Dispersion
			}
			out.Dispersion = phi
			out.Statistic = (g0.Deviance - g1.Deviance) / phi
		}
		out.PValue = chiSquareP(out.Statistic, q)
	case types.ComparisonTestWald:
		w := wald(g1, tested)
		if p.Distribution == types.ComparisonDistributionF {
			out.DFResidual = g1.WaldDF
			out.Statistic = w / q
			if !(g1.WaldDF > 0) {
				out.Statistic = math.NaN()
			}
			out.PValue = statdist.FSurvival(out.Statistic, q, g1.WaldDF)
		} else {
			out.Statistic = w
			out.PValue = chiSquareP(w, q)
		}
	default:
		return out, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"comparison "+p.Name+" has no resolved test", map[string]any{"compare": p.Index})
	}
	return out, nil
}

// chiSquareP is the chi-square tail at x, NaN on a NaN statistic.
func chiSquareP(x, df float64) float64 {
	if math.IsNaN(x) {
		return math.NaN()
	}
	return statdist.ChiSquareSurvival(x, df)
}

// wald is b_Sᵀ V_SS⁻¹ b_S over the terms (in the figures' order), NaN
// when V_SS is not positive definite or a cell is not finite.
func wald(g *model.RegressionFigures, terms []string) float64 {
	idx := make([]int, len(terms))
	for k, t := range terms {
		idx[k] = slices.Index(g.Terms, t)
		if idx[k] < 0 {
			return math.NaN()
		}
	}
	q := len(idx)
	v, err := linalg.NewSym(q, nil)
	if err != nil {
		return math.NaN()
	}
	b := make([]float64, q)
	for a := 0; a < q; a++ {
		b[a] = g.Coefficients[idx[a]]
		for c := a; c < q; c++ {
			x := g.Covariance[idx[a]][idx[c]]
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return math.NaN()
			}
			v.Set(a, c, x)
		}
	}
	x, err := linalg.SolveSPD(v, linalg.NewVec(b))
	if err != nil {
		return math.NaN()
	}
	w := 0.0
	for a := 0; a < q; a++ {
		w += float64(b[a] * x.At(a))
	}
	return w
}

// testedKeys is the coefficient keys of terms in g, in g.Terms order:
// each term's TermKeys (a multinomial fit's "<category>:<term>" keys),
// or the term itself when g maps none. A term g does not fit stays
// (unsorted, after the fitted ones), so wald reads it as missing.
func testedKeys(g *model.RegressionFigures, terms []string) []string {
	var keys []string
	for _, t := range terms {
		if ks, ok := g.TermKeys[t]; ok {
			keys = append(keys, ks...)
		} else {
			keys = append(keys, t)
		}
	}
	pos := func(k string) int {
		if i := slices.Index(g.Terms, k); i >= 0 {
			return i
		}
		return len(g.Terms)
	}
	slices.SortStableFunc(keys, func(a, b string) int { return pos(a) - pos(b) })
	return keys
}

// otherTerms is g's terms outside tested, sorted: the terms both fits of
// a nested pair must share.
func otherTerms(g *model.RegressionFigures, tested []string) []string {
	out := []string{}
	for _, t := range g.Terms {
		if !slices.Contains(tested, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}
