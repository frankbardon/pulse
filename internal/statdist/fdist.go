package statdist

import "math"

// FSurvival returns P(F ≥ f) for an F variate with (df1, df2) degrees of
// freedom (R's pf(f, df1, df2, lower.tail = FALSE)): the F-test p-value,
// through the regularized incomplete beta
//
//	P(F ≥ f) = I_{df2/(df2 + df1·f)}(df2/2, df1/2)
//
// with x = df2/(df2 + df1·f) and its complement y formed directly, so
// neither tail loses precision. 1 for f ≤ 0, NaN for a non-positive df
// or a NaN argument. On the bit contract (doc.go): the incomplete beta is
// RegularizedIncompleteBetaXY and the one product is barriered. The
// nested-model comparison (internal/regcompare) reads it.
func FSurvival(f, df1, df2 float64) float64 {
	if !(df1 > 0) || !(df2 > 0) || math.IsNaN(f) {
		return math.NaN()
	}
	if f <= 0 {
		return 1
	}
	if math.IsInf(f, 1) {
		return 0
	}
	num := float64(df1 * f)
	var x, y float64
	if num > df2 {
		r := df2 / num
		x, y = r/(1+r), 1/(1+r)
	} else {
		r := num / df2
		x, y = 1/(1+r), r/(1+r)
	}
	return RegularizedIncompleteBetaXY(df2/2.0, df1/2.0, x, y)
}
