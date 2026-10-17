package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/types"
)

// Predict for the inferential prior-period kinds (U47 #328):
// OVERLAY_T_VS_PRIOR, OVERLAY_Z_VS_PRIOR and OVERLAY_PROP_Z_VS_PRIOR,
// on a grouped Process (SERIES) host or a crosstab with no column
// grouper. Every rule is the runtime's (internal/processing
// overlay_vs_prior.go), raised with the same code, message and details
// through the shared overlayhost helpers, in the runtime's order:
//
//  1. the pre-pass (ValidateOverlays): a crosstab WITH a column grouper
//     (overlayhost.PriorColumnsRefusal), a multi-measure crosstab
//     (overlayhost.MeasureHostRefusal);
//  2. Ref / Scope (validateOverlayPriorTest, the shared SERIES rule on
//     both hosts) and Level / Within (validateOverlayLevelWithinPredict);
//  3. params.field / params.partition_by (the SERIES validators; on a
//     crosstab, validateOverlayPriorMeasure resolves them against the
//     cell and the row groupers);
//  4. the measure (validateOverlayPriorMeasure): its type
//     (overlayhost.PriorMeasureRefusal), a normalised crosstab for the
//     proportion kind, components disabled, an extension measure's
//     declared schema for the T / Z kinds.

// validateOverlayPriorTest is step 2's Ref / Scope rule: the SERIES rule
// verbatim, which the crosstab table delegates to for these kinds.
func validateOverlayPriorTest(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, opts *PredictOptions, index int) {
	if cerr := overlayhost.SeriesSpecRefusal(spec, opts.overlayRoute(spec.Kind), index); cerr != nil {
		addCodedError(env, cerr)
	}
}

// validateOverlayPriorMeasure is steps 3 (crosstab host) and 4. cols is
// the series host's column resolver (seriesfield.Columns); run only on
// a spec no earlier rule refused.
func validateOverlayPriorMeasure(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, opts *PredictOptions, route types.OverlayKind, index int, cols seriesfield.Columns) {
	if req == nil || spec == nil || !overlayhost.PriorTestKind(route) {
		return
	}
	var measure *types.Aggregation
	switch {
	case req.Crosstab != nil:
		if req.Crosstab.HasMeasures() {
			return // refused by the pre-pass
		}
		measure = req.Crosstab.Cell
		if measure != nil {
			if _, err := seriesfield.Resolve([]*types.Aggregation{measure}, spec, index, nil); err != nil {
				addCodedError(env, err)
				return
			}
		}
		if _, err := seriespartition.Depth(req.Crosstab.Rows, spec, index); err != nil {
			addCodedError(env, err)
			return
		}
	case len(req.Groups) > 0 && len(req.Aggregations) > 0:
		scored, err := seriesfield.Resolve(req.Aggregations, spec, index, cols)
		if err != nil || scored.Index < 0 || scored.Index >= len(req.Aggregations) {
			return // validateOverlaySeriesField reports it
		}
		measure = req.Aggregations[scored.Index]
	default:
		return
	}
	var measureType types.AggregationType
	if measure != nil {
		measureType = measure.Type
	}
	if cerr := overlayhost.PriorMeasureRefusal(spec.Kind, route, measureType, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}
	if req.Crosstab != nil {
		if cerr := overlayhost.PriorNormalizedRefusal(spec.Kind, route, req.Crosstab.NormalizeOrDefault(), index); cerr != nil {
			addCodedError(env, cerr)
			return
		}
	}
	if opts.componentsDisabled(req) {
		env.AddError(string(errors.PULSE_OVERLAY_COMPONENTS_REQUIRED), overlayhost.PriorComponentsRequiredMessage(spec.Kind),
			map[string]any{"index": index, "kind": string(spec.Kind)})
		return
	}
	if overlayhost.PriorTestUsesWelford(route) && measureType != "" && !overlayhost.PriorMeasureBuiltin(measureType) &&
		welfordTripleCellRefused(measureType, opts) {
		addCodedError(env, overlayhost.PriorWelfordRefusal(spec.Kind, measureType, index))
	}
}
