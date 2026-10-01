package synth

import (
	"math"
)

// This file is the recovery-SCALE half of the model-fidelity section
// (FU-19): how a generated value is turned into the number the recovery
// refit regresses, for the two distributions whose Q has no point
// inverse.
//
// # The problem it solves
//
// A modelled numeric is drawn as value = Q(Φ(μ + σz)), so the captured
// coefficients live on the LATENT scale and the recovery must put the
// generated data there too (synth/fidelity_models.go's header). For
// every continuous Q that is one call to latentFor. For `bernoulli` and
// `discrete` it is not possible at all: the Q is a step / staircase, a
// generated value pins the latent to an INTERVAL rather than a point,
// and no function of the value alone can recover the point inside it.
//
// latentFor therefore refuses both (see latentInvertible), which is
// correct and was also a measured gap. On the motivating survey cohort
// — where 90 of 122 fields are packed_bool and 90 of 105 captured models
// target one — the refusal took FidelityReport.Models from 14 comparable
// targets of 55 down to 2. The instrument that exists to confirm the
// conditioning survived generation had stopped covering the field types
// the conditioning is mostly about.
//
// # Why a raw score is not enough, and what makes this one honest
//
// The candidate remedy is an interval-midpoint probit score: give each
// level the latent Φ⁻¹ of its own cumulative MIDPOINT. It is monotone in
// the value and converges on the exact inverse as levels multiply, and
// WP-C rejected it anyway, for a reason that is exactly right: OLS on
// that score reports a systematically ATTENUATED coefficient for a
// generation path that is exactly correct, and at K = 2 it degenerates
// to the two-valued inverse-Mills-shaped construction the bernoulli arm
// had already turned down. A diagnostic that reports a wrong number is
// worse than one that reports nothing, because the wrong number reads as
// evidence.
//
// What closes it is computing the SAME score on BOTH sides:
//
//   - the OBSERVED side scores each generated value (this file's
//     scoreFunc);
//   - the EXPECTED side computes, for each row, the conditional mean of
//     that same score under the CAPTURED model — E[score | m], where m
//     is the row's own standardised linear prediction (this file's
//     expectedFunc).
//
// OLS is linear in its response: b = (X'X)⁻¹X'y. So if generation is
// faithful, E[observed score | X] equals the expected score row for row,
// and therefore E[b_observed | X] equals b_expected EXACTLY — not
// approximately, and with no distributional assumption beyond the
// generation formula itself. Dividing the observed coefficient by the
// ratio b_expected / captured returns it to the latent scale with the
// attenuation removed rather than estimated.
//
// Two consequences worth stating because they answer the two questions
// WP-C left open.
//
//  1. NO K GATE. K = 2 is not a degenerate case here; it is the case
//     with the smallest retention, which surfaces as the widest standard
//     error rather than as a wrong number. Gating on K would refuse the
//     type family the feature is mostly applied to for a defect the
//     construction does not have.
//
//  2. NO ORDERED-PROBIT REFIT. An ordered probit (which
//     processing/regression does not offer — binomial/probit is a
//     reserved, unimplemented link) would be a more EFFICIENT estimator,
//     not a more correct one, because the bias this construction would
//     otherwise carry is computed exactly rather than assumed away. It
//     would tighten the standard errors; it would not move the expected
//     value of the estimate.
//
// The cost is honestly reported rather than hidden: the retention factor
// rides every predictor entry (ModelPredictorFidelity.ScoreRetention),
// the standard error is inflated by the same factor the coefficient is,
// and a term whose retention falls below minScoreRetention is REFUSED
// rather than divided by a number that small.

// RecoveryScaleProbitScore is ModelFidelity.Scale for a target recovered
// through the calibrated interval-midpoint probit score rather than
// through a point latent inverse.
//
// It is on the wire because the two comparisons are not the same
// measurement and a reader holding one must be able to tell which. A
// latent-inverse entry's recovered coefficient is a direct estimate; a
// score-scale entry's has been divided by a retention factor that is
// itself exact but that widens the standard error, so the same numeric
// gap carries less evidence.
const RecoveryScaleProbitScore = "probit_score"

// minScoreRetention is the smallest fraction of a captured coefficient
// the calibrated score may retain before that term is refused instead of
// reported.
//
// The calibration divides the observed coefficient by the retention
// factor, so a tiny factor is a large multiplier on the estimate AND on
// its standard error. Above this floor the inflation is at most 20x and
// the widened standard error makes the flagging rule read correctly; far
// below it the term's expected effect on the score is indistinguishable
// from the arithmetic's own noise, and the ratio stops being a
// calibration and starts being a division by approximately nothing.
//
// A term at or below the floor ships with an Error rather than a number,
// exactly as an unrealized level does — the reader learns the term was
// not estimable here, which is a different statement from a recovered
// zero. 0.05 is deliberately generous: the smallest retention any real
// shape produces is the K = 2 case at roughly 0.5, and a term reaching
// this floor is one whose captured effect is so far out in the
// staircase's tail that no level boundary separates it.
const minScoreRetention = 0.05

// recoveryScore is how one target's generated values reach the recovery
// refit.
//
// For a latent-invertible distribution it is latentFor's own inverse and
// nothing else is populated: staircase is false, expected is nil, and
// the comparison runs on the latent scale exactly as it did before this
// file existed. The two arms are one mechanism with the identity
// calibration on one side.
type recoveryScore struct {
	// value maps one generated data-scale value onto the scale the refit
	// regresses, reporting ok=false for a value the distribution cannot
	// place at all.
	value latentFunc

	// staircase is true when value is a calibrated step score rather
	// than the exact latent inverse. When it is, expected is non-nil and
	// every coefficient the refit produces must be divided by its own
	// retention factor before it can be read beside a captured one.
	staircase bool

	// expected returns E[score | latent mean m] — the conditional mean
	// of value under faithful generation for a row whose standardised
	// linear prediction is m. nil unless staircase.
	expected func(m float64) float64
}

// buildRecoveryScore resolves fs onto the scale the recovery refit
// regresses. residualZ is the drawer's own residual standard deviation
// already divided by the target's marginal std — the same quantity
// modelDrawer.transform multiplies its z by — because the expected score
// is an integral over that residual and nothing else.
//
// The error return is latentFor's, unchanged, for a distribution that is
// neither invertible nor a staircase. That case is a drift bug rather
// than a property of the mathematics (a distribution admitted to
// fieldMoments and quantileFor with no arm here at all), and it must
// keep surfacing as an Error on the entry.
func buildRecoveryScore(fs FieldSpec, mean, std, residualZ float64) (recoveryScore, error) {
	if latent, err := latentFor(fs, mean, std); err == nil {
		return recoveryScore{value: latent}, nil
	} else if !isStaircaseDistribution(fs.Distribution) {
		return recoveryScore{}, err
	}

	values, cum, err := staircaseSupport(fs)
	if err != nil {
		return recoveryScore{}, err
	}
	return newStaircaseScore(values, cum, residualZ), nil
}

// isStaircaseDistribution names the distributions whose Q is a step or a
// staircase — exactly latentInvertible's complement, written out rather
// than negated so a reader of either sees the same two names.
func isStaircaseDistribution(distribution string) bool {
	switch distribution {
	case DistBernoulli, DistDiscrete:
		return true
	}
	return false
}

// staircaseSupport returns the ascending level values and their
// cumulative probabilities for a step/staircase distribution, in the
// exact form quantileFor's own arm uses so the score and the draw cannot
// disagree about where a boundary is.
//
// The bernoulli arm is written as the two-level staircase it is: levels
// {0, 1} with cumulative {1-p, 1}. quantileFor emits 1 iff p > 1-prev,
// which is the same boundary.
func staircaseSupport(fs FieldSpec) (values, cum []float64, err error) {
	switch fs.Distribution {
	case DistBernoulli:
		prev, _, perr := paramFloat(fs.Name, fs.Params, "p", 0.5)
		if perr != nil {
			return nil, nil, perr
		}
		return []float64{0, 1}, []float64{1 - prev, 1}, nil
	case DistDiscrete:
		levels, lerr := parseDiscreteLevels(fs)
		if lerr != nil {
			return nil, nil, lerr
		}
		return levels.values, levels.cum, nil
	}
	return nil, nil, nil
}

// newStaircaseScore builds the observed and expected halves of the
// calibrated score for one staircase support.
//
// The per-level score is Φ⁻¹ of the level's own cumulative MIDPOINT,
// which is monotone in the level and lands every level inside the latent
// range its own interval occupies. The cut points are Φ⁻¹ of the
// cumulative boundaries, i.e. precisely the latent thresholds
// quantileFor's staircase uses, so the expected score below integrates
// over the same intervals the draw fills.
func newStaircaseScore(values, cum []float64, residualZ float64) recoveryScore {
	k := len(values)
	scores := make([]float64, k)
	prev := 0.0
	for i := 0; i < k; i++ {
		scores[i] = phiInv((prev + cum[i]) / 2)
		prev = cum[i]
	}
	// cuts[i] is the latent boundary BELOW which level i+1 cannot be
	// drawn; there are k-1 of them, and the two outer boundaries are
	// handled as CDF 0 and 1 rather than as ±Inf so no arithmetic ever
	// sees an infinity.
	cuts := make([]float64, 0, k-1)
	for i := 0; i+1 < k; i++ {
		cuts = append(cuts, phiInv(cum[i]))
	}

	// levelOf maps a generated value onto its level index. The writer
	// stores the rounded integer and the support is the set of integers
	// the staircase emits, so the match is exact in practice; the
	// half-step tolerance is what keeps a decimal-scaled support (a
	// hand-authored `discrete` over f64 levels) from falling off the end
	// of its own scale.
	levelOf := func(v float64) (int, bool) {
		best, bestDist := -1, math.Inf(1)
		for i, lv := range values {
			if d := math.Abs(v - lv); d < bestDist {
				best, bestDist = i, d
			}
		}
		if best < 0 {
			return 0, false
		}
		tol := 0.5
		if k > 1 {
			// Half the narrowest gap, so a support with levels closer
			// together than 1 apart still classifies unambiguously.
			narrowest := math.Inf(1)
			for i := 0; i+1 < k; i++ {
				if g := values[i+1] - values[i]; g < narrowest {
					narrowest = g
				}
			}
			if narrowest/2 < tol {
				tol = narrowest / 2
			}
		}
		if bestDist > tol {
			return 0, false
		}
		return best, true
	}

	value := func(v float64) (float64, bool) {
		i, ok := levelOf(v)
		if !ok {
			return 0, false
		}
		return scores[i], true
	}

	expected := func(m float64) float64 {
		if residualZ <= 0 {
			// A deterministic prediction: the latent is m exactly, so
			// the expected score is the score of the level m falls in.
			i := 0
			for i < len(cuts) && m > cuts[i] {
				i++
			}
			return scores[i]
		}
		var sum, lower float64
		for i := 0; i < k; i++ {
			upper := 1.0
			if i < len(cuts) {
				upper = phi((cuts[i] - m) / residualZ)
			}
			sum += float64(scores[i] * (upper - lower))
			lower = upper
		}
		return sum
	}

	return recoveryScore{value: value, staircase: true, expected: expected}
}
