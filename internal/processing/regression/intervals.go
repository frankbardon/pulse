package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// defaultConfLevel is the coefficient confidence-interval coverage when
// RegressionSpec.ConfLevel is unset.
const defaultConfLevel = 0.95

// resolvedConfLevel is the coverage a REG_OLS / REG_GLM fit's intervals
// use: spec.ConfLevel, or 0.95 when unset. REG_BAYES_LINEAR resolves
// its own (bayesConfLevel), falling back to CredibleLevel.
func resolvedConfLevel(spec *types.RegressionSpec) float64 {
	if spec.ConfLevel != 0 {
		return spec.ConfLevel
	}
	return defaultConfLevel
}

// bayesConfLevel is REG_BAYES_LINEAR's interval coverage: ConfLevel,
// else CredibleLevel, else 0.95 — so a spec that sets only one of the
// two gets ci_lower / ci_upper equal to its credible_intervals.
func bayesConfLevel(spec *types.RegressionSpec) float64 {
	if spec.ConfLevel != 0 {
		return spec.ConfLevel
	}
	if spec.CredibleLevel != 0 {
		return spec.CredibleLevel
	}
	return defaultConfLevel
}

// validateConfLevel refuses a ConfLevel outside (0, 1) (NaN included);
// zero selects the default. Mirrored by predict's confLevelRefusal.
func validateConfLevel(spec *types.RegressionSpec) error {
	c := spec.ConfLevel
	if c == 0 || (c > 0 && c < 1) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_CONFIG,
		string(spec.Type)+" conf_level must satisfy 0 < conf_level < 1 (zero selects the default of 0.95)",
		map[string]any{"conf_level": c, "type": string(spec.Type)},
	)
}

// attachIntervals writes the two-sided coefficient confidence intervals
// onto res at coverage level: coefficient ∓ q·SE for every key the
// StdErrors map carries (a key without a standard error gets no
// interval, and a fit with none gets no interval fields at all).
//
// When studentT is set, q is the Student-t critical value on df (REG_OLS
// residual df N* − p − 1; REG_BAYES_LINEAR's posterior 2·a_n) and res.DF
// records df — NaN, so null on the wire, when df ≤ 0, which also makes
// every bound NaN. Otherwise q is the standard normal one (Wald) and DF
// is cleared. A zero standard error yields the point interval
// [coef, coef], exactly as credibleInterval does.
//
// Any interval fields res already carried are replaced, so a Resample
// pass can re-attach over the base fit's.
func attachIntervals(res *types.RegressionResult, level, df float64, studentT bool) {
	res.CILower, res.CIUpper, res.DF, res.ConfLevel = nil, nil, 0, 0
	if len(res.StdErrors) == 0 {
		return
	}
	var q float64
	if studentT {
		q = statdist.StudentTQuantile(0.5+level/2, df)
		res.DF = df
		if !(df > 0) {
			res.DF = math.NaN()
		}
	} else {
		q = statdist.NormalQuantile(0.5 + level/2)
	}
	lower := make(map[string]float64, len(res.StdErrors))
	upper := make(map[string]float64, len(res.StdErrors))
	for key, se := range res.StdErrors {
		coef := res.Coefficients[key]
		if se == 0 && !math.IsNaN(q) {
			lower[key], upper[key] = coef, coef
			continue
		}
		half := q * se
		lower[key], upper[key] = coef-half, coef+half
	}
	res.CILower, res.CIUpper, res.ConfLevel = lower, upper, level
}

// CheckConfLevel is validateConfLevel for a spec no built-in engine
// validates — an extension (registered) REG_* model's: PROCESSING_CONFIG
// on a conf_level outside (0, 1), the message and details predict
// raises.
func CheckConfLevel(spec *types.RegressionSpec) error {
	if spec == nil {
		return nil
	}
	return validateConfLevel(spec)
}

// AttachExtensionIntervals writes an extension (registered) REG_*
// model's coefficient confidence intervals at the spec's conf_level
// (0.95 unset) from the result's standard errors — the built-ins'
// attachIntervals, with the quantile rule of the matching built-in case:
//
//   - resampled (res.Resample set): the standard normal on the replicate
//     SE, as every built-in resample re-attaches;
//   - else, when the model reported its residual df (Fit.DF, "df" on
//     ReportedFigures): the Student-t quantile on that df, REG_OLS's
//     rule (and REG_GLM gamma's) — so an OLS-shaped model reporting
//     N* − p − 1 matches REG_OLS's intervals;
//   - else the standard normal (Wald), REG_GLM's binomial / poisson rule.
//
// A coefficient whose SE is NaN gets a NaN (null) interval, key kept;
// a zero SE the point interval. DF then follows the built-in contract
// (the df of the t the intervals read): the reported df on a t
// interval (NaN — null — and NaN bounds when it is ≤ 0), absent on a
// normal one, so a resampled fit drops a reported df from DF and from
// ReportedFigures exactly as a built-in resample clears it.
func AttachExtensionIntervals(res *types.RegressionResult, spec *types.RegressionSpec) {
	studentT := res.Resample == "" && slices.Contains(res.ReportedFigures, "df")
	attachIntervals(res, resolvedConfLevel(spec), res.DF, studentT)
	if !studentT && slices.Contains(res.ReportedFigures, "df") {
		// A copy: a resampled result shares the base fit's slice.
		res.ReportedFigures = slices.DeleteFunc(slices.Clone(res.ReportedFigures), func(k string) bool { return k == "df" })
	}
}
