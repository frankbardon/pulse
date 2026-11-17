package regression

import (
	"context"
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// multinomial_modifiers.go: REG_MULTINOMIAL under the Resample and
// Selection modifiers (U68 E2-S2, #658). A refit is one fresh fit of
// the baseline-category logit over a row subset (a resample replicate)
// or under a predictor subset (a selection candidate), driven by the
// extension drivers' loops (RunExtensionModifiers over resample_core.go
// and selection.go): a replicate is the "<category>:<term>" coefficient
// vector in the base fit's order.
//
// Every refit after the slot's first successful one — the base fit, or
// under selection the search's first candidate — is pinned to that
// fit's categories and reference (decision D5), so every replicate
// reports the same keys: a refit's rows outside the pinned categories
// are not fitted, and a pinned category no row of the refit carries is
// PROCESSING_REGRESSION_INSUFFICIENT_DATA reason
// model.ReasonReplicateEmptyLevel. A bootstrap skips that replicate, a
// jackknife fails the slot alone (its variance needs every replicate),
// a selection candidate is dropped.
//
// Selection is term-level (decision D11): a predictor enters or leaves
// across all K − 1 non-reference categories at once, and the criterion
// is deviance + k·edf with edf the fit's coefficient count (K − 1)·(terms
// + 1) — MASS::stepAIC on multinom (extractAIC's multinom edf), k = 2
// (aic) or ln(n_obs) (bic). Resampled p-values are the Wald z on the
// replicate SE (jackknife) or the percentile p (bootstrap), as for the
// built-ins; the intervals are Wald normal on the replicate SE.

// multinomialPin is the categories and reference every modifier refit
// keeps.
type multinomialPin struct {
	cats []string
	ref  string
}

// problem is the pinned multinomialProblem: the pin's categories in its
// order, its reference, every row's category indexed — or the
// replicate_empty_level refusal of the first pinned category seen
// lacks.
func (pin *multinomialPin) problem(x []float64, labels []string, prior []float64, n, q int, seen map[string]bool) (*multinomialProblem, error) {
	for _, c := range pin.cats {
		if !seen[c] {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA,
				fmt.Sprintf("REG_MULTINOMIAL: a refit's rows carry no %q target, a category the full fit has (every resample replicate and selection candidate keeps the full fit's categories %v and reference): a jackknife needs every replicate, so the slot fails; a bootstrap skips the replicate", c, pin.cats),
				map[string]any{"reason": model.ReasonReplicateEmptyLevel, "level": c, "levels": slices.Clone(pin.cats)})
		}
	}
	return newMultinomialProblem(x, labels, prior, n, q, pin.cats, slices.Index(pin.cats, pin.ref)), nil
}

// fitModified runs e.spec's Selection (first) and Resample over records
// through RunExtensionModifiers, every refit after the first pinned.
func (e *multinomialEngine) fitModified(ctx context.Context, records []Record) (*types.RegressionResult, error) {
	var pin *multinomialPin
	fit := func(recs []Record, predictors []string) (*types.RegressionResult, []string, error) {
		inner := *e.spec
		inner.Predictors = slices.Clone(predictors)
		inner.Resample, inner.JackknifeGroups, inner.JackknifeBy, inner.BootstrapIters, inner.RNGSeed = "", 0, "", 0, 0
		inner.Selection, inner.Criterion = "", ""
		refit := &multinomialEngine{spec: &inner, levels: e.levels}
		res, keys, err := refit.fitRows(recs, pin)
		if err != nil {
			return nil, nil, err
		}
		if pin == nil {
			pin = &multinomialPin{cats: slices.Clone(res.Categories), ref: res.Reference}
		}
		return res, keys, nil
	}
	// The multinomial fit always reports its deviance (nil
	// missingDeviance), and the pin keeps every refit's keys the base
	// fit's, so a key mismatch is an engine fault.
	return RunExtensionModifiers(ctx, e.spec, true, records, fit, nil, func(base, got []string) error {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"REG_MULTINOMIAL: a pinned refit reported other coefficients than the base fit",
			map[string]any{"type": string(types.REG_MULTINOMIAL), "base": base, "got": got})
	})
}
