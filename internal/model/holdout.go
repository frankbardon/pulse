package model

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// HoldoutKey is the RegressionSpec key of the holdout predicate
// (details.slot of its refusals).
const HoldoutKey = "holdout"

// HoldoutScorerRefusal is the one rule over a regression's holdout and
// its model: the held-out rows are scored by the fit's RowScorer, so a
// regression whose type the instance cannot score (scorable — the
// runtime registry's ScorableRegression, predict's scorableRegression)
// is PULSE_MODEL_SCORE_INVALID reason no_scorer, details {regression,
// slot: "holdout", reason[, name]}. The field-reference walk calls it,
// so predict and the run refuse the same request identically. Nil when
// spec sets no holdout or its type is scorable.
func HoldoutScorerRefusal(spec *types.RegressionSpec, scorable ScorableFunc) *errors.CodedError {
	if spec == nil || spec.Holdout == "" {
		return nil
	}
	if scorable == nil {
		scorable = ScorableType
	}
	if scorable(spec.Type) {
		return nil
	}
	details := map[string]any{"regression": string(spec.Type), "slot": HoldoutKey, "reason": ReasonNoScorer}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_SCORE_INVALID,
		"regression "+string(spec.Type)+" cannot take a holdout: nothing can score its fit (a registered regression needs RegressionRegistration.Scorer); drop holdout",
		details)
}

// HoldoutEmptyReason is details.reason of the per-slot failure of a
// holdout fit that scored no held-out row
// (PROCESSING_REGRESSION_INSUFFICIENT_DATA).
const HoldoutEmptyReason = "holdout_empty"

// HoldoutEmpty is that failure: the predicate held out no row whose
// target, predictors (and weight) the scorer could read, so no
// out-of-sample figure exists. Data-dependent, so the slot fails alone.
func HoldoutEmpty(spec *types.RegressionSpec) *errors.CodedError {
	details := map[string]any{"regression": string(spec.Type), "slot": HoldoutKey, "reason": HoldoutEmptyReason, "holdout": spec.Holdout}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
		"regression "+string(spec.Type)+" holdout "+strconv.Quote(spec.Holdout)+" held out no scorable row (true on none, or every held-out row has a null target or predictor)",
		details)
}
