// Package regexponent is the one rule and the one arithmetic behind a
// regression's `exponentiate: true` (types.RegressionSpec.Exponentiate,
// U68 E3-S2): which regression types report their coefficients on the
// ratio scale, and the exponentiated figures themselves. The no-execute
// predict pass (internal/descriptor) and the engine
// (internal/processing/regression) both read it, so the two refuse the
// same requests with the same code, message and details. It reads the
// spec and the fit's own result only and imports neither side.
//
// REG_MULTINOMIAL reports relative-risk ratios exp(β) on every
// "<category>:<term>" key (Stata mlogit, rrr); REG_ORDINAL cumulative
// odds ratios exp(β) on its predictors only — a threshold is a cut
// point, not an effect (exp(coef(polr))). Each key carries the
// delta-method standard error exp(β)·SE and the exponentiated interval
// exp(lower), exp(upper), read off whatever the fit already reports
// (classical, robust or resampled standard errors and the intervals
// built on them), so the figures follow the fit's covariance without
// a second computation. The raw coefficient table never moves.
package regexponent

import (
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// Requires is details.requires of the refusal: the regression types
// that report exponentiated coefficients.
const Requires = "type REG_MULTINOMIAL or REG_ORDINAL"

// Admits reports whether a regression of type t honours exponentiate.
func Admits(t types.RegressionType) bool {
	return t == types.REG_MULTINOMIAL || t == types.REG_ORDINAL
}

// Check returns the PROCESSING_CONFIG refusal of a spec setting
// exponentiate on a type that does not report it (details {type, slot
// "exponentiate", requires[, name]}), or nil. The built-in engines'
// ValidateRegression and predict (checkRegressionSpec) both call it; an
// extension model's spec meets the same predicate (Admits) through the
// shared-slot table.
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || !spec.Exponentiate || Admits(spec.Type) {
		return nil
	}
	details := map[string]any{"type": string(spec.Type), "slot": "exponentiate", "requires": Requires}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(spec.Type)+" does not accept exponentiate: exponentiated coefficients are REG_MULTINOMIAL's relative-risk ratios and REG_ORDINAL's cumulative odds ratios; drop it",
		details)
}

// Apply sets res.Exponentiated when spec asks for it: exp of every
// reported coefficient (REG_ORDINAL: the predictors' only), exp(β)·SE
// where the fit reports a standard error and exp of each interval
// bound where it reports one. A non-finite figure stays non-finite
// (null on the wire). A failed fit (nil res, res.Error) or a spec that
// does not ask is untouched, so an absent knob is byte-identical.
func Apply(spec *types.RegressionSpec, res *types.RegressionResult) {
	if spec == nil || !spec.Exponentiate || !Admits(spec.Type) || res == nil || res.Error != nil || len(res.Coefficients) == 0 {
		return
	}
	keep := func(string) bool { return true }
	if spec.Type == types.REG_ORDINAL {
		keep = func(k string) bool { return slices.Contains(spec.Predictors, k) }
	}
	out := &types.RegressionExponentiated{Estimates: map[string]float64{}}
	for k, b := range res.Coefficients {
		if !keep(k) {
			continue
		}
		e := detmath.Exp(b)
		out.Estimates[k] = e
		if se, ok := res.StdErrors[k]; ok {
			if out.StdErrors == nil {
				out.StdErrors = map[string]float64{}
			}
			out.StdErrors[k] = float64(e * se)
		}
		if lo, ok := res.CILower[k]; ok {
			if out.CILower == nil {
				out.CILower = map[string]float64{}
			}
			out.CILower[k] = detmath.Exp(lo)
		}
		if hi, ok := res.CIUpper[k]; ok {
			if out.CIUpper == nil {
				out.CIUpper = map[string]float64{}
			}
			out.CIUpper[k] = detmath.Exp(hi)
		}
	}
	res.Exponentiated = out
}
