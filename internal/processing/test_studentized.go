package processing

import (
	"math"
	"sort"
)

// Studentized-range distribution helpers used by TEST_TUKEY_HSD.
// Implemented as nested adaptive Gauss–Kronrod quadrature without
// external dependencies.
//
// Definition: Q(k, ν) is the studentized range distribution — the
// distribution of the range of k iid N(0, σ²) variables divided by an
// independent estimate of σ (a chi distribution scaled by 1/ν).
//
//	P(Q > q | k, ν) = ∫₀^∞ S_R(q·s, k) · h(s; ν) ds
//
// where:
//
//	S_R(t, k) = P(R > t) for R the range of k iid N(0, 1), written
//	            without cancellation (see normalRangeSurvival)
//	h(s; ν)   = density of √(χ²_ν / ν)  =  2 · (ν/2)^{ν/2} / Γ(ν/2)
//	                                     · s^{ν−1} · exp(−ν s² / 2)
//
// Integrating the survival directly (rather than 1 − CDF) keeps full
// relative precision in the far tail. Accuracy: relative 1e-10 or
// better against a high-precision R quadrature for k ∈ [2, 20],
// ν ∈ [2, 1e4] and p down to 1e-300 (reference_oracle_test.go). R's own
// ptukey() is only ~1e-8 accurate and computes the tail as 1 − CDF.

// normalRangeCDF returns P(R ≤ t | k) where R is the range of k iid
// N(0, 1) variables. Returns 0 for t ≤ 0 and 1 for k ≤ 1.
func normalRangeCDF(t float64, k int) float64 {
	return 1 - normalRangeSurvival(t, k)
}

// normalRangeSurvival returns P(R > t | k) for the range R of k iid
// N(0, 1) variables:
//
//	P(R > t) = k ∫ φ(z) · (a^{k−1} − (a − c)^{k−1}) dz,
//	a = P(Z > z),  c = P(Z > z + t)
//	         = −k ∫ φ(z) · a^{k−1} · expm1((k−1)·log1p(−c/a)) dz
//
// (z is the sample minimum). The expm1/log1p form never subtracts two
// numbers near 1, so the far tail keeps its relative precision.
func normalRangeSurvival(t float64, k int) float64 {
	if k <= 1 {
		return 0
	}
	if t <= 0 {
		return 1
	}
	km1 := float64(k - 1)
	f := func(z float64) float64 {
		a := 0.5 * math.Erfc(z/math.Sqrt2)
		if a == 0 {
			return 0
		}
		c := 0.5 * math.Erfc((z+t)/math.Sqrt2)
		phi := math.Exp(-0.5*z*z) / math.Sqrt(2*math.Pi)
		return -float64(k) * phi * math.Pow(a, km1) * math.Expm1(km1*math.Log1p(-c/a))
	}
	// The integrand mass sits near the midpoint z = −t/2 (the minimum
	// and maximum straddle zero); outside [−t−9, 9] it is below 1e-17
	// of the total.
	center := -t / 2
	lo, hi := -t-9, 9.0
	return adaptiveGK15(f, dyadicBreaks(center, 1, lo, hi), 1e-12)
}

// studentizedRangeSurvival returns P(Q > q | k, ν), the survival
// function used as the Tukey HSD p-value. df = ν.
//
// For ν = ∞ collapses to S_R(q, k).
func studentizedRangeSurvival(q float64, k int, df float64) float64 {
	if q <= 0 {
		return 1
	}
	if math.IsInf(df, 1) {
		return normalRangeSurvival(q, k)
	}
	lgHalfNu, _ := math.Lgamma(df / 2)
	// log of the leading constant in h(s; ν): 2 · (ν/2)^{ν/2} / Γ(ν/2),
	// kept in log space to avoid overflow at large ν.
	lnLeading := math.Log(2) + (df/2)*math.Log(df/2) - lgHalfNu
	integrand := func(s float64) float64 {
		if s <= 0 {
			return 0
		}
		lnH := lnLeading + (df-1)*math.Log(s) - df*s*s/2
		if lnH < -745 {
			return 0
		}
		return math.Exp(lnH) * normalRangeSurvival(q*s, k)
	}
	// The chi density peaks at s = 1 with width σ = 1/√(2ν); in the far
	// tail the integrand's mass shifts to s* ≈ ν / (ν + q²/4), where the
	// density and S_R(q·s) ≈ exp(−q²s²/4) balance. Break around both
	// so no Kronrod panel can straddle a narrow peak unseen.
	sigma := math.Min(1/math.Sqrt(2*df), 0.5)
	sHi := 1 + 40*sigma
	sStar := df / (df + q*q/4)
	sigmaStar := math.Min(1/math.Sqrt(2*(df+q*q/4)), 0.5)
	breaks := dyadicBreaks(1, sigma, 0, sHi)
	if math.Abs(sStar-1) > 2*sigma {
		breaks = append(breaks, dyadicBreaks(sStar, sigmaStar, 0, sHi)...)
	}
	p := adaptiveGK15(integrand, breaks, 1e-11)
	if p > 1 {
		return 1
	}
	return p
}

// studentizedRangeInverse returns q such that P(Q > q | k, df) = alpha,
// the Tukey HSD critical value. Solved on log(p) by Illinois-modified
// regula falsi inside an expanding bracket, so the root carries the
// survival's own accuracy.
func studentizedRangeInverse(alpha float64, k int, df float64) float64 {
	if math.IsNaN(alpha) || alpha <= 0 || alpha >= 1 || k < 2 {
		return math.NaN()
	}
	logAlpha := math.Log(alpha)
	g := func(q float64) float64 {
		return math.Log(studentizedRangeSurvival(q, k, df)) - logAlpha
	}
	lo, hi := 0.0, 4.0
	glo, ghi := math.Inf(1), g(hi)
	for ghi > 0 {
		lo, glo = hi, ghi
		hi *= 2
		if hi > 1e12 {
			return math.Inf(1)
		}
		ghi = g(hi)
	}
	// Illinois-modified regula falsi: superlinear, never leaves the
	// bracket, and needs no derivative of the nested integral.
	var x float64
	side := 0
	for range 200 {
		if math.IsInf(glo, 1) {
			x = 0.5 * (lo + hi)
		} else {
			x = (lo*ghi - hi*glo) / (ghi - glo)
			if !(x > lo && x < hi) {
				x = 0.5 * (lo + hi)
			}
		}
		gx := g(x)
		// |g| is the relative error in the survival; 1e-13 of it moves q
		// by far less than the 1e-10 the oracle allows.
		if math.Abs(gx) <= 1e-13 || hi-lo <= 4*epsilon*hi {
			return x
		}
		if gx > 0 {
			lo, glo = x, gx
			if side == +1 {
				ghi /= 2
			}
			side = +1
		} else {
			hi, ghi = x, gx
			if side == -1 && !math.IsInf(glo, 1) {
				glo /= 2
			}
			side = -1
		}
		if hi-lo <= 1e-14*hi {
			break
		}
	}
	return 0.5 * (lo + hi)
}

// dyadicBreaks returns breakpoints center ± w·{0, 1, 3, 9, …} clipped
// to [lo, hi], always including lo and hi. Panels therefore grow
// geometrically away from the peak, which a 15-point Kronrod rule
// resolves on the first pass; adaptive bisection does the rest.
func dyadicBreaks(center, w, lo, hi float64) []float64 {
	out := []float64{lo, hi}
	if center > lo && center < hi {
		out = append(out, center)
	}
	for step := w; step <= hi-lo; step *= 3 {
		if v := center - step; v > lo && v < hi {
			out = append(out, v)
		}
		if v := center + step; v > lo && v < hi {
			out = append(out, v)
		}
	}
	return out
}

// Gauss–Kronrod 7/15 nodes and weights on [−1, 1] (QUADPACK qk15).
// gk15Nodes[i] for odd i are the 7-point Gauss nodes.
var (
	gk15Nodes = [8]float64{
		0.991455371120812639206854697526329,
		0.949107912342758524526189684047851,
		0.864864423359769072789712788640926,
		0.741531185599394439863864773280788,
		0.586087235467691130294144845693013,
		0.405845151377397166906606412076961,
		0.207784955007898467600689403773245,
		0.000000000000000000000000000000000,
	}
	gk15KronrodWeights = [8]float64{
		0.022935322010529224963732008058970,
		0.063092092629978553290700663189204,
		0.104790010322250183839876322541518,
		0.140653259715525918745189590510238,
		0.169004726639267902826583426598550,
		0.190350578064785409913256402421014,
		0.204432940075298892414161999234649,
		0.209482141084727828012999174891714,
	}
	gk7GaussWeights = [4]float64{
		0.129484966168869693270611432679082,
		0.279705391489276667901467771423780,
		0.381830050505118944950369775488975,
		0.417959183673469387755102040816327,
	}
)

// gk15 integrates f over [a, b], returning the Kronrod estimate and
// QUADPACK's error estimate: |Kronrod − Gauss| rescaled by the panel's
// mean absolute deviation (resasc · min(1, (200·|K−G|/resasc)^1.5)),
// which tracks the true Kronrod error far more closely than |K−G|.
func gk15(f func(float64) float64, a, b float64) (float64, float64) {
	c := 0.5 * (a + b)
	h := 0.5 * (b - a)
	var fv [15]float64
	fv[14] = f(c)
	kron := fv[14] * gk15KronrodWeights[7]
	gauss := fv[14] * gk7GaussWeights[3]
	for i := range 7 {
		dx := h * gk15Nodes[i]
		fv[2*i], fv[2*i+1] = f(c-dx), f(c+dx)
		s := fv[2*i] + fv[2*i+1]
		kron += gk15KronrodWeights[i] * s
		if i%2 == 1 {
			gauss += gk7GaussWeights[i/2] * s
		}
	}
	mean := 0.5 * kron
	resasc := gk15KronrodWeights[7] * math.Abs(fv[14]-mean)
	for i := range 7 {
		resasc += gk15KronrodWeights[i] * (math.Abs(fv[2*i]-mean) + math.Abs(fv[2*i+1]-mean))
	}
	resasc *= math.Abs(h)
	err := math.Abs((kron - gauss) * h)
	if resasc != 0 && err != 0 {
		err = resasc * math.Min(1, math.Pow(200*err/resasc, 1.5))
	}
	return kron * h, err
}

// adaptiveGK15 integrates f over [min(breaks), max(breaks)] starting
// from the given breakpoints, repeatedly bisecting the panel with the
// largest error estimate until the summed error is at most
// relTol·|total|. Deterministic: panels are processed in a fixed order.
func adaptiveGK15(f func(float64) float64, breaks []float64, relTol float64) float64 {
	sort.Float64s(breaks)
	type panel struct{ a, b, val, err float64 }
	panels := make([]panel, 0, 64)
	for i := 0; i+1 < len(breaks); i++ {
		a, b := breaks[i], breaks[i+1]
		if b <= a {
			continue
		}
		v, e := gk15(f, a, b)
		panels = append(panels, panel{a, b, v, e})
	}
	const maxPanels = 2000
	for len(panels) < maxPanels {
		total, errSum := 0.0, 0.0
		worst := 0
		for i, p := range panels {
			total += p.val
			errSum += p.err
			if p.err > panels[worst].err {
				worst = i
			}
		}
		if errSum <= relTol*math.Abs(total) || panels[worst].err == 0 {
			return total
		}
		w := panels[worst]
		m := 0.5 * (w.a + w.b)
		if m <= w.a || m >= w.b {
			return total
		}
		lv, le := gk15(f, w.a, m)
		rv, re := gk15(f, m, w.b)
		panels[worst] = panel{w.a, m, lv, le}
		panels = append(panels, panel{m, w.b, rv, re})
	}
	total := 0.0
	for _, p := range panels {
		total += p.val
	}
	return total
}
