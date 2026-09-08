package synth

import (
	"math"
	"sort"
)

// MinShapeFitObservations is the minimum number of retained samples a
// numeric field needs before ProfileOptions.FitShape attempts a mixture
// fit at all. Below this threshold the field falls back to the plain
// normal capture unconditionally — mirroring MinPairObservations'
// philosophy elsewhere in this package (a thin sample can't support a
// richer model), though here it gates whether a fit is even ATTEMPTED
// rather than emitting a warning about an already-shipped result.
const MinShapeFitObservations = 50

// shapeFitEMIterations bounds the EM refinement passes for the
// 2-component mixture fit. EM performs coordinate ascent on the
// log-likelihood, so it never diverges — it can only plateau — and a
// fixed, generous iteration count is simpler and just as safe as a
// convergence-delta stopping rule for the profile-sized inputs this
// story targets (reservoir-capped at 10,000 samples, see profile.go).
const shapeFitEMIterations = 50

// shapeFitMinStdFraction floors each fitted component's std at this
// fraction of the field's overall (single-normal) std. Without a floor,
// EM can collapse a component onto a single point (or a tight cluster
// of near-duplicate values), driving its variance toward zero; that
// component's likelihood then diverges toward +Inf and makes the
// mixture look like an unconditional "improvement" regardless of
// whether real bimodal structure exists.
const shapeFitMinStdFraction = 0.05

// shapeFitMinSeparationStds is the minimum distance between the two
// fitted component means, expressed in units of their averaged std,
// below which a fit is rejected as "not really two components" even if
// BIC alone would have accepted it. GMM log-likelihood surfaces have
// local maxima that describe sampling noise rather than genuine
// multimodality, especially near the null (single-normal) case; this is
// the "bimodality... heuristic" this story's own notes call for,
// applied as a second, independent gate alongside BIC rather than
// instead of it — see fitNumericShape.
const shapeFitMinSeparationStds = 0.75

// ShapeProfile is declared in profile.go, beside NumericProfile — this
// file holds only the fitting math that populates it.

// fitNumericShape decides whether a numeric field's captured samples
// are better explained by a 2-component Gaussian mixture than by the
// single normal(mean, std) SpecFromProfile reconstructs by default. It
// returns nil when they are not — including when there is not enough
// data to try, the data has no spread to split on, or EM does not
// converge to two well-separated components.
//
// Selection criterion: Bayesian Information Criterion (BIC).
//
//	BIC = -2*logLikelihood + k*log(n)
//
// where k is the model's number of free parameters (2 for a single
// normal: mean, std; 5 for a 2-component mixture: 2 means + 2 stds + 1
// free weight, since the second weight is determined by the first). A
// 2-component mixture ALWAYS fits the training sample at least as well
// as a single normal in raw log-likelihood terms — it strictly contains
// the single-normal model as a special case (equal means/stds, or one
// weight at 0) — so comparing raw log-likelihood alone would switch
// every numeric field to a mixture regardless of its actual shape.
// BIC's k*log(n) penalty is exactly the term that keeps a mixture from
// being selected unless the extra components earn back more likelihood
// than their parameter cost — the "genuine improvement" bar this
// story's acceptance criteria calls for, without an arbitrary manually-
// tuned margin.
//
// A second, independent guard (shapeFitMinSeparationStds) rejects a
// BIC-accepted fit whose two components are not meaningfully apart in
// units of their own std — this catches the case where EM's local
// optimum technically improves likelihood via two heavily-overlapping
// components describing sampling noise rather than true bimodality,
// which is a known failure mode of comparing nested Gaussian mixtures
// by likelihood ratio / BIC near the null (the regularity conditions
// for the usual asymptotics do not hold at the single-component
// boundary).
//
// Limitations (documented, not fixed by this story, matching E4-S1's
// own deferral note): fixed at exactly 2 components — no BIC sweep over
// component counts, even though the underlying "mixture" sampler
// accepts any component count >= 2; EM here is a single deterministic
// run from percentile-based initial means (25th/75th percentile of the
// sample), not multi-start, so a genuinely trimodal or a heavily
// skewed-but-unimodal source may fit a 2-component approximation that
// is serviceable but not optimal.
func fitNumericShape(samples []float64, mean, std float64) *ShapeProfile {
	n := len(samples)
	if n < MinShapeFitObservations || std <= 0 {
		return nil
	}
	m1, m2, s1, s2, w1, w2, mixLL, ok := fitTwoComponentEM(samples, std)
	if !ok {
		return nil
	}
	avgStd := (s1 + s2) / 2
	if avgStd <= 0 || math.Abs(m1-m2) < shapeFitMinSeparationStds*avgStd {
		return nil
	}
	normalLL := normalLogLikelihood(samples, mean, std)
	lnN := math.Log(float64(n))
	bicNormal := -2*normalLL + 2*lnN
	bicMixture := -2*mixLL + 5*lnN
	if bicMixture >= bicNormal {
		return nil
	}
	return &ShapeProfile{
		Means:   []float64{m1, m2},
		Stds:    []float64{s1, s2},
		Weights: []float64{w1, w2},
	}
}

// fitTwoComponentEM runs a fixed number of EM iterations for a
// 2-component Gaussian mixture, initialized deterministically from the
// 25th/75th percentile of samples — no RNG involved, so profile capture
// stays deterministic given the same source data. Returns ok=false when
// the data has no spread to split on (25th and 75th percentile land on
// the same value) or when a component's responsibility collapses to
// (near) zero during fitting — either of which makes a 2-component
// description meaningless.
func fitTwoComponentEM(samples []float64, overallStd float64) (m1, m2, s1, s2, w1, w2, ll float64, ok bool) {
	n := len(samples)
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	m1 = sorted[n/4]
	m2 = sorted[(3*n)/4]
	if m1 == m2 {
		return 0, 0, 0, 0, 0, 0, 0, false
	}

	s1, s2 = overallStd, overallStd
	minStd := overallStd * shapeFitMinStdFraction
	if minStd <= 0 {
		minStd = 1e-6
	}
	w1, w2 = 0.5, 0.5
	resp := make([]float64, n)

	for iter := 0; iter < shapeFitEMIterations; iter++ {
		// E-step: responsibility of component 1 for each sample.
		for i, x := range samples {
			p1 := w1 * normalPDF(x, m1, s1)
			p2 := w2 * normalPDF(x, m2, s2)
			total := p1 + p2
			if total <= 0 {
				resp[i] = 0.5
			} else {
				resp[i] = p1 / total
			}
		}
		// M-step: re-estimate weights, means, stds from responsibilities.
		var sumR1, sumR2, sumX1, sumX2 float64
		for i, x := range samples {
			r1 := resp[i]
			r2 := 1 - r1
			sumR1 += r1
			sumR2 += r2
			sumX1 += r1 * x
			sumX2 += r2 * x
		}
		if sumR1 < 1e-6 || sumR2 < 1e-6 {
			// One component has claimed (almost) no mass — the fit has
			// collapsed to one effective component. Report failure
			// rather than a degenerate near-single-normal "mixture".
			return 0, 0, 0, 0, 0, 0, 0, false
		}
		newM1 := sumX1 / sumR1
		newM2 := sumX2 / sumR2
		var sumSq1, sumSq2 float64
		for i, x := range samples {
			d1 := x - newM1
			d2 := x - newM2
			sumSq1 += resp[i] * d1 * d1
			sumSq2 += (1 - resp[i]) * d2 * d2
		}
		newS1 := math.Sqrt(sumSq1 / sumR1)
		newS2 := math.Sqrt(sumSq2 / sumR2)
		if newS1 < minStd {
			newS1 = minStd
		}
		if newS2 < minStd {
			newS2 = minStd
		}
		m1, m2 = newM1, newM2
		s1, s2 = newS1, newS2
		w1, w2 = sumR1/float64(n), sumR2/float64(n)
	}

	var total float64
	for _, x := range samples {
		p1 := w1 * normalPDF(x, m1, s1)
		p2 := w2 * normalPDF(x, m2, s2)
		mix := p1 + p2
		if mix <= 0 {
			mix = math.SmallestNonzeroFloat64
		}
		total += math.Log(mix)
	}
	// Order components by mean ascending for a deterministic, readable
	// captured shape regardless of which half of the split each
	// component's mass converged toward.
	if m1 > m2 {
		m1, m2 = m2, m1
		s1, s2 = s2, s1
		w1, w2 = w2, w1
	}
	return m1, m2, s1, s2, w1, w2, total, true
}

// normalPDF is the standard Gaussian density.
func normalPDF(x, mean, std float64) float64 {
	variance := std * std
	return math.Exp(-((x-mean)*(x-mean))/(2*variance)) / math.Sqrt(2*math.Pi*variance)
}

// normalLogLikelihood is the total log-likelihood of samples under a
// single Normal(mean, std).
func normalLogLikelihood(samples []float64, mean, std float64) float64 {
	if std <= 0 {
		return math.Inf(-1)
	}
	variance := std * std
	logNorm := -0.5 * math.Log(2*math.Pi*variance)
	var ll float64
	for _, x := range samples {
		d := x - mean
		ll += logNorm - (d*d)/(2*variance)
	}
	return ll
}
