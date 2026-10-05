package regression

import (
	"math"

	"github.com/frankbardon/pulse/internal/statdist"
)

// pValueForCoefficient returns the two-sided p-value of a coefficient
// estimate under the null β = 0, distributed as Student's t with df
// degrees of freedom, via the shared internal/statdist implementation
// (the same one the TEST_* operators use).
//
// df is a float: a weighted fit's residual df is N* − p − 1, fractional
// under kind probability.
//
// Degenerate inputs:
//   - df ≤ 0 (n − p − 1 < 1)        : returns NaN
//   - se == 0 with non-zero coef    : returns 0 (perfectly significant
//     by limit, but in practice this
//     signals a singular fit caught
//     upstream)
//   - se == 0 with zero coef        : returns NaN (statistic undefined)
//   - coef NaN                      : returns NaN
func pValueForCoefficient(coef, se, df float64) float64 {
	if df <= 0 {
		return math.NaN()
	}
	if math.IsNaN(coef) {
		return math.NaN()
	}
	if se == 0 {
		if coef == 0 {
			return math.NaN()
		}
		return 0
	}
	t := coef / se
	return statdist.StudentTTwoSidedP(t, df)
}

// waldZTwoSidedP returns the two-sided Wald-z p-value 2·(1 − Φ(|β/SE|))
// under the null β = 0, where Φ is the standard normal CDF. REG_GLM
// uses this instead of the Student's t form because the binomial /
// poisson dispersion is fixed at 1 by the family assumption — there is
// no residual variance estimate that introduces extra degrees-of-
// freedom uncertainty into the test statistic. Gamma is wired but not
// numerically validated this phase; its Wald-z values are emitted on a
// dispersion=1 assumption that gamma callers should treat skeptically.
//
// Degenerate inputs:
//   - se == 0 with non-zero coef → returns 0 (perfectly significant).
//   - se == 0 with zero coef     → returns NaN (statistic undefined).
//   - coef NaN                   → returns NaN.
func waldZTwoSidedP(coef, se float64) float64 {
	if math.IsNaN(coef) {
		return math.NaN()
	}
	if se == 0 {
		if coef == 0 {
			return math.NaN()
		}
		return 0
	}
	z := coef / se
	if math.IsInf(z, 0) {
		return 0
	}
	// Two-sided: 2 · (1 − Φ(|z|)) = math.Erfc(|z|/√2).
	return math.Erfc(math.Abs(z) / math.Sqrt2)
}
