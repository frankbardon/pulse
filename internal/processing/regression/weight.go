package regression

import (
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Row weighting for the regression engines — the engine half of
// .claude/reference/weighting.md (Weighted inference). A weight-aware
// engine reads its slot's STAMPED weight (processing.StampWeightsWith
// leaves RegressionSpec.Weight set only when a weight applies), folds
// each listwise-complete row with that row's weight, and evaluates the
// frequency formula on w* (weighting.Basis: N* = Σw under frequency,
// Kish n_eff under probability). Unweighted, every row weighs exactly
// 1, which reproduces the unweighted fit bit for bit.

// rowWeights is the weight one regression engine reads. The zero value
// is unweighted.
type rowWeights struct {
	spec  *types.WeightSpec
	basis weighting.Basis
}

// newRowWeights reads a stamped regression slot's resolved weight (nil:
// the fit runs unweighted).
func newRowWeights(spec *types.RegressionSpec) rowWeights {
	w := spec.Weight.Spec()
	return rowWeights{spec: w, basis: weighting.BasisOf(w)}
}

// weighted reports whether the fit carries a weight.
func (rw rowWeights) weighted() bool { return rw.spec != nil }

// of is the weight rec contributes with: exactly 1 unweighted; the row's
// weight when it is valid and positive; ok=false otherwise — an invalid
// weight is excluded (and counted once per weight field by the
// orchestrator's processing.WeightRowTally) and a zero weight
// contributes nothing, so neither enters the fit or NObs. Call it only
// after the row's target and predictors are known present: a row
// dropped listwise never has its weight judged.
func (rw rowWeights) of(rec Record) (float64, bool) {
	if rw.spec == nil {
		return 1, true
	}
	w, present := rec.NumericValue(rw.spec.Field)
	if weighting.Classify(w, present, rw.spec.EffectiveKind()) != weighting.Valid || w == 0 {
		return 0, false
	}
	return w, true
}

// note writes SumWeights (and, under kind probability, NEff) onto a
// fitted result; no-op unweighted, so an unweighted result is unchanged.
func (rw rowWeights) note(res *types.RegressionResult, sumW, sumWSq float64) {
	if !rw.weighted() {
		return
	}
	res.SumWeights = sumW
	if rw.basis == weighting.Probability {
		res.NEff = weighting.KishNEff(sumW, sumWSq)
	}
}

// LowNEffFloor is the Kish n_eff below which a probability-weighted fit
// of spec reports PULSE_WEIGHT_LOW_NEFF: the raw-row floor p + 1 the
// fit itself enforces (PROCESSING_REGRESSION_INSUFFICIENT_DATA), p
// being the predictor count. Its residual df N* − p − 1 is then
// negative, so the t p-values are undefined.
func LowNEffFloor(spec *types.RegressionSpec) int { return len(spec.Predictors) + 1 }
