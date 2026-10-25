package sem

import (
	"math"

	"github.com/frankbardon/pulse/internal/detmath"
)

// The ordinal (two-step) numerics — normal.go, bvn.go, ordinal.go and
// ordinal_scores.go — are FMA-free and call only the portable detmath
// kernels (plus the correctly rounded math.Sqrt), so a threshold, a
// polychoric or a polyserial correlation is a function of the data
// alone: identical bits on amd64 and arm64 (TestPolychoric_GoldenBits).
// Every product that feeds an addition or a subtraction is written
// float64(a*b), which the Go specification forbids the compiler to fuse
// into a multiply-add (TestPolychoric_NoFusibleProducts,
// TestPolychoric_NoFMAContraction). Never drop one of these conversions,
// and never reorder a loop: the operation order is the contract.

const (
	// invSqrt2Pi is 1/√(2π) (R's M_1_SQRT_2PI).
	invSqrt2Pi = 0.398942280401432677939946059934
)

// stdNormPDF is the standard normal density φ(x).
func stdNormPDF(x float64) float64 {
	if math.IsInf(x, 0) {
		return 0
	}
	return float64(invSqrt2Pi * detmath.Exp(float64(-0.5*float64(x*x))))
}

// mvphiA are the Chebyshev coefficients of Genz's MVPHI (Schonfelder,
// Math. Comp. 32 (1978) pp. 1232-1240), the normal CDF inside the
// bivariate-normal port (bvn.go), as published in mvtdstpack.f and SciPy's scipy/stats/mvndst.f.
var mvphiA = [44]float64{
	6.10143081923200417926465815756e-1,
	-4.34841272712577471828182820888e-1,
	1.76351193643605501125840298123e-1,
	-6.0710795609249414860051215825e-2,
	1.7712068995694114486147141191e-2,
	-4.321119385567293818599864968e-3,
	8.54216676887098678819832055e-4,
	-1.27155090609162742628893940e-4,
	1.1248167243671189468847072e-5, 3.13063885421820972630152e-7,
	-2.70988068537762022009086e-7, 3.0737622701407688440959e-8,
	2.515620384817622937314e-9, -1.028929921320319127590e-9,
	2.9944052119949939363e-11, 2.6051789687266936290e-11,
	-2.634839924171969386e-12, -6.43404509890636443e-13,
	1.12457401801663447e-13, 1.7281533389986098e-14,
	-4.264101694942375e-15, -5.45371977880191e-16,
	1.58697607761671e-16, 2.0899837844334e-17,
	-5.900526869409e-18, -9.41893387554e-19, 2.14977356470e-19,
	4.6660985008e-20, -7.243011862e-21, -2.387966824e-21,
	1.91177535e-22, 1.20482568e-22, -6.72377e-25, -5.747997e-24,
	-4.28493e-25, 2.44856e-25, 4.3793e-26, -8.151e-27, -3.089e-27,
	9.3e-29, 1.74e-28, 1.6e-29, -8.0e-30, -2.0e-30,
}

// stdNormCDF is the standard normal CDF Φ(z): a line-for-line port of
// Genz's MVPHI (accurate to 1e-15; the lower tail keeps full relative
// precision, so the upper tail is Φ(−z)). ±Inf map to 1 / 0, NaN to NaN.
func stdNormCDF(z float64) float64 {
	const (
		rtwo = 1.414213562373095048801688724209
		im   = 24
	)
	if math.IsNaN(z) {
		return math.NaN()
	}
	xa := math.Abs(z) / rtwo
	var p float64
	if xa > 100 {
		p = 0
	} else {
		t := (float64(8*xa) - 30) / (float64(4*xa) + 15)
		var bm, b, bp float64
		for i := im; i >= 0; i-- {
			bp = b
			b = bm
			bm = float64(t*b) - bp + mvphiA[i]
		}
		p = float64(float64(detmath.Exp(float64(-xa*xa))*(bm-bp)) / 4)
	}
	if z > 0 {
		p = 1 - p
	}
	return p
}

// stdNormQuantile is Φ⁻¹(p): Wichura's AS 241 (PPND16, about 1 part in
// 1e16), the algorithm of R's qnorm that lavaan's thresholds use. p ≤ 0
// is −Inf, p ≥ 1 +Inf, NaN NaN. Below exp(−27²) ≈ 2.5e−317 (a
// proportion no finite weight ratio reaches) the r ≤ 27 rational
// continues rather than R's asymptotic series.
func stdNormQuantile(p float64) float64 {
	switch {
	case math.IsNaN(p):
		return math.NaN()
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	}
	q := p - 0.5
	if math.Abs(q) <= 0.425 {
		r := 0.180625 - float64(q*q)
		num := float64(r*2509.0809287301226727) + 33430.575583588128105
		num = float64(num*r) + 67265.770927008700853
		num = float64(num*r) + 45921.953931549871457
		num = float64(num*r) + 13731.693765509461125
		num = float64(num*r) + 1971.5909503065514427
		num = float64(num*r) + 133.14166789178437745
		num = float64(num*r) + 3.387132872796366608
		den := float64(r*5226.495278852854561) + 28729.085735721942674
		den = float64(den*r) + 39307.89580009271061
		den = float64(den*r) + 21213.794301586595867
		den = float64(den*r) + 5394.1960214247511077
		den = float64(den*r) + 687.1870074920579083
		den = float64(den*r) + 42.313330701600911252
		den = float64(den*r) + 1
		return float64(q*num) / den
	}
	tail := p
	if q > 0 {
		tail = 1 - p
	}
	r := math.Sqrt(-detmath.Log(tail))
	var val float64
	if r <= 5 {
		r += -1.6
		num := float64(r*7.7454501427834140764e-4) + 0.0227238449892691845833
		num = float64(num*r) + 0.24178072517745061177
		num = float64(num*r) + 1.27045825245236838258
		num = float64(num*r) + 3.64784832476320460504
		num = float64(num*r) + 5.7694972214606914055
		num = float64(num*r) + 4.6303378461565452959
		num = float64(num*r) + 1.42343711074968357734
		den := float64(r*1.05075007164441684324e-9) + 5.475938084995344946e-4
		den = float64(den*r) + 0.0151986665636164571966
		den = float64(den*r) + 0.14810397642748007459
		den = float64(den*r) + 0.68976733498510000455
		den = float64(den*r) + 1.6763848301838038494
		den = float64(den*r) + 2.05319162663775882187
		den = float64(den*r) + 1
		val = num / den
	} else {
		r += -5
		num := float64(r*2.01033439929228813265e-7) + 2.71155556874348757815e-5
		num = float64(num*r) + 0.0012426609473880784386
		num = float64(num*r) + 0.026532189526576123093
		num = float64(num*r) + 0.29656057182850489123
		num = float64(num*r) + 1.7848265399172913358
		num = float64(num*r) + 5.4637849111641143699
		num = float64(num*r) + 6.6579046435011037772
		den := float64(r*2.04426310338993978564e-15) + 1.4215117583164458887e-7
		den = float64(den*r) + 1.8463183175100546818e-5
		den = float64(den*r) + 7.868691311456132591e-4
		den = float64(den*r) + 0.0148753612908506148525
		den = float64(den*r) + 0.13692988092273580531
		den = float64(den*r) + 0.59983220655588793769
		den = float64(den*r) + 1
		val = num / den
	}
	if q < 0 {
		val = -val
	}
	return val
}
