package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// runSelection drives the Selection modifier. The caller supplies:
//
//   - spec        : the full regression spec (Selection / Criterion read
//     here; everything else funneled into the inner fit).
//   - records     : the buffered record set after filters / features.
//   - predictors  : the original predictor list — selection picks a
//     subset of these.
//   - critFn      : closure that returns (AIC, BIC) for an active set
//     given an already-completed refitResult. The driver
//     refits each candidate via `fitter` and then asks
//     critFn for the criterion value. For OLS the critFn
//     inputs are (RSS, n, k0); for GLM (Deviance, n, k0).
//   - fitter      : refits the inner engine on (records, activePredictors).
//   - finalizeFn  : after the active set is chosen, fits the final model
//     and packages a RegressionResult (with the inner
//     engine's own SE / p-value / R² / etc).
//
// Returns the final RegressionResult with SelectedFeatures populated
// and Selection / Criterion echoed.
func runSelection(
	spec *types.RegressionSpec,
	records []Record,
	predictors []string,
	critFn func(fit *refitResult, k0 int) float64,
	fitter func([]Record, []string) (*refitResult, error),
	finalizeFn func([]Record, []string) (*types.RegressionResult, error),
) (*types.RegressionResult, error) {
	if spec.Criterion != "aic" && spec.Criterion != "bic" {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"regression Selection requires Criterion ∈ {aic, bic}; got "+spec.Criterion,
			map[string]any{"selection": spec.Selection, "criterion": spec.Criterion},
		)
	}

	var active []string
	switch spec.Selection {
	case "forward":
		active = forwardSelect(records, predictors, critFn, fitter)
	case "backward":
		active = backwardSelect(records, predictors, critFn, fitter)
	case "stepwise":
		active = stepwiseSelect(records, predictors, critFn, fitter)
	default:
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"unknown Selection value: "+spec.Selection+" (expected forward | backward | stepwise)",
			map[string]any{"selection": spec.Selection},
		)
	}

	// Refit once at the selected active set; finalizeFn returns the
	// SE / p-values / R² / etc on this final model.
	out, err := finalizeFn(records, active)
	if err != nil {
		return nil, err
	}
	out.Selection = spec.Selection
	out.Criterion = spec.Criterion
	out.SelectedFeatures = active
	return out, nil
}

// forwardSelect implements forward (greedy) selection. Start at the
// intercept-only model; at each step try adding each remaining
// predictor and pick the one that lowers the criterion the most. Stop
// when no addition lowers it.
func forwardSelect(
	records []Record,
	predictors []string,
	critFn func(fit *refitResult, k0 int) float64,
	fitter func([]Record, []string) (*refitResult, error),
) []string {
	active := make([]string, 0, len(predictors))
	remaining := append([]string(nil), predictors...)

	// Baseline: intercept-only fit.
	currentCrit := math.Inf(1)
	if base, err := fitter(records, active); err == nil {
		currentCrit = finiteOrInf(critFn(base, len(active)))
	}

	for len(remaining) > 0 {
		bestCrit := currentCrit
		bestIdx := -1
		var bestCandidate []string
		for i, candidate := range remaining {
			next := append(append([]string(nil), active...), candidate)
			fit, err := fitter(records, next)
			if err != nil {
				continue
			}
			c := finiteOrInf(critFn(fit, len(next)))
			if c < bestCrit {
				bestCrit = c
				bestIdx = i
				bestCandidate = next
			}
		}
		if bestIdx == -1 {
			break
		}
		active = bestCandidate
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
		currentCrit = bestCrit
	}
	return active
}

// backwardSelect implements backward elimination. Start with every
// predictor; at each step try removing each one and pick the removal
// that lowers the criterion the most. Stop when no removal lowers it.
//
// If the initial full model fails to fit (e.g., rank deficient), we
// fall back to forward selection so the caller always gets a result.
func backwardSelect(
	records []Record,
	predictors []string,
	critFn func(fit *refitResult, k0 int) float64,
	fitter func([]Record, []string) (*refitResult, error),
) []string {
	active := append([]string(nil), predictors...)
	currentCrit := math.Inf(1)
	if fit, err := fitter(records, active); err == nil {
		currentCrit = finiteOrInf(critFn(fit, len(active)))
	} else {
		// Full model can't fit; degrade to forward selection.
		return forwardSelect(records, predictors, critFn, fitter)
	}

	for len(active) > 0 {
		bestCrit := currentCrit
		bestRemoveIdx := -1
		for i := range active {
			next := make([]string, 0, len(active)-1)
			next = append(next, active[:i]...)
			next = append(next, active[i+1:]...)
			fit, err := fitter(records, next)
			if err != nil {
				continue
			}
			c := finiteOrInf(critFn(fit, len(next)))
			if c < bestCrit {
				bestCrit = c
				bestRemoveIdx = i
			}
		}
		if bestRemoveIdx == -1 {
			break
		}
		active = append(active[:bestRemoveIdx], active[bestRemoveIdx+1:]...)
		currentCrit = bestCrit
	}
	return active
}

// stepwiseSelect alternates forward and backward at each cycle. At
// each pass we try every addition (from `remaining`) and every removal
// (from `active`); if any single move lowers the criterion we apply
// the best one and repeat. Stop when no single move (forward or
// backward) improves the score.
//
// Start from the intercept-only model — selection finishes faster on
// underspecified data than starting from the full model and trying to
// shrink it. The bidirectional sweep keeps results identical to
// MASS::stepAIC's default behavior in R.
func stepwiseSelect(
	records []Record,
	predictors []string,
	critFn func(fit *refitResult, k0 int) float64,
	fitter func([]Record, []string) (*refitResult, error),
) []string {
	active := make([]string, 0, len(predictors))
	remaining := append([]string(nil), predictors...)
	currentCrit := math.Inf(1)
	if base, err := fitter(records, active); err == nil {
		currentCrit = finiteOrInf(critFn(base, len(active)))
	}

	for {
		bestCrit := currentCrit
		bestKind := 0 // 0 none, 1 add, 2 remove
		bestIdx := -1
		var bestCandidate []string

		// Try adding each remaining predictor.
		for i, candidate := range remaining {
			next := append(append([]string(nil), active...), candidate)
			fit, err := fitter(records, next)
			if err != nil {
				continue
			}
			c := finiteOrInf(critFn(fit, len(next)))
			if c < bestCrit {
				bestCrit = c
				bestKind = 1
				bestIdx = i
				bestCandidate = next
			}
		}
		// Try removing each active predictor.
		for i := range active {
			next := make([]string, 0, len(active)-1)
			next = append(next, active[:i]...)
			next = append(next, active[i+1:]...)
			fit, err := fitter(records, next)
			if err != nil {
				continue
			}
			c := finiteOrInf(critFn(fit, len(next)))
			if c < bestCrit {
				bestCrit = c
				bestKind = 2
				bestIdx = i
				bestCandidate = next
			}
		}

		if bestKind == 0 {
			break
		}
		if bestKind == 1 {
			active = bestCandidate
			remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
		} else {
			// Removal: shift the removed predictor back into `remaining` so
			// future cycles can re-add it (stepwise's bidirectional sweep).
			removed := active[bestIdx]
			active = bestCandidate
			remaining = append(remaining, removed)
		}
		currentCrit = bestCrit
	}
	return active
}
