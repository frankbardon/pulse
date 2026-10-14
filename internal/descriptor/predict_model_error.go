package descriptor

import (
	"encoding/json"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// Model-error aggregator predict checks (AGG_MAPE / AGG_RMSE / AGG_MAE,
// fit-scoring E2-S1). The runtime rules live in
// internal/processing/aggregator_model_error.go (modelErrorConfigError)
// plus the decimal dispatch that runs before any aggregator factory;
// predict may not import that package, so the params rules are
// re-derived here and the type rules read the same accepted-types table
// (internal/fieldaccept) — same code, message and details — and
// TestModelError_PredictMatchesRuntime (root package) pins the two
// together.

// isModelErrorAgg reports whether t is one of the three model-error
// aggregators.
func isModelErrorAgg(t types.AggregationType) bool {
	switch t {
	case types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE:
		return true
	}
	return false
}

// modelErrorParams mirrors processing's typed params decode; the type
// NAME matters too — a decode error message quotes it, and predict's
// message must equal the runtime's.
type modelErrorParams struct {
	PredictionField string `json:"prediction_field"`
}

// validateModelErrorAggs reports, per model-error slot (aggregations,
// the crosstab cell and its margin aggregations), the first refusal the
// runtime would raise. A slot whose type the instance hides routes to
// "" and is skipped, exactly like a never-registered name.
func validateModelErrorAggs(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if req == nil || schema == nil {
		return
	}
	check := func(agg *types.Aggregation) {
		if agg == nil || !isModelErrorAgg(opRoute(opts.Instance, agg.Type)) {
			return
		}
		if err := modelErrorPredictCheck(agg, schema); err != nil {
			addCodedError(env, err)
		}
	}
	for _, agg := range req.Aggregations {
		check(agg)
	}
	if req.Crosstab != nil {
		check(req.Crosstab.Cell)
		for _, agg := range req.Crosstab.MarginAggregations {
			check(agg)
		}
	}
}

// modelErrorPredictCheck mirrors the runtime order: the decimal
// dispatch on Field (before any factory) — the accepted-types table's
// decimal rule, which validateFieldAcceptance reports, so nothing more
// is judged here — then the params decode, then modelErrorConfigError.
func modelErrorPredictCheck(agg *types.Aggregation, schema *encoding.Schema) error {
	name := string(agg.Type)
	if f := schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
		return nil
	}
	var params modelErrorParams
	if len(agg.Params) > 0 {
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				name+": invalid params: "+err.Error(),
				map[string]any{"aggregation": name})
		}
	}
	if params.PredictionField == "" {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			name+" requires params.prediction_field (the column holding the predicted value)",
			map[string]any{"aggregation": name, "field": agg.Field})
	}
	if cerr := fieldaccept.Check(name, fieldaccept.SlotField, agg.Field, schema); cerr != nil {
		return cerr
	}
	if cerr := fieldaccept.Check(name, fieldaccept.SlotPrediction, params.PredictionField, schema); cerr != nil {
		return cerr
	}
	return nil
}
