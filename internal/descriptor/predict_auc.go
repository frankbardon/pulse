package descriptor

import (
	"encoding/json"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/aucspec"
	"github.com/frankbardon/pulse/types"
)

// AGG_AUC predict checks (model-workbench E2-S1). The rules live in the
// leaf internal/aucspec, which the runtime constructor
// (internal/processing/aggregator_auc.go) calls too, so both sides
// raise the same code, message and details; TestAUC_PredictMatchesRuntime
// (root package) pins the pair. A non-binary outcome VALUE is seen only
// at run time (PROCESSING_AUC_OUTCOME_NOT_BINARY); predict refuses the
// outcome columns whose TYPE cannot be binary.

// validateAUCAggs reports, per AGG_AUC slot (aggregations, the crosstab
// cell and its margin aggregations), the first refusal the runtime
// would raise. A slot whose type the instance hides routes to "" and is
// skipped, exactly like a never-registered name.
func validateAUCAggs(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if req == nil || schema == nil {
		return
	}
	check := func(agg *types.Aggregation) {
		if agg == nil || opRoute(opts.Instance, agg.Type) != types.AGG_AUC {
			return
		}
		if err := aucPredictCheck(agg, schema); err != nil {
			addCodedError(env, err)
		}
	}
	for _, agg := range req.Aggregations {
		check(agg)
	}
	if req.Crosstab != nil {
		for _, cell := range req.Crosstab.CellAggregations() {
			check(cell)
		}
		for _, agg := range req.Crosstab.MarginAggregations {
			check(agg)
		}
	}
}

// aucPredictCheck mirrors the runtime order: the decimal dispatch on
// Field (before any factory; validateFieldAcceptance reports it), then
// the params decode, then aucspec.ConfigError.
func aucPredictCheck(agg *types.Aggregation, schema *encoding.Schema) error {
	if f := schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
		return nil
	}
	var params aucspec.Params
	if len(agg.Params) > 0 {
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				aucspec.Name+": invalid params: "+err.Error(),
				map[string]any{"aggregation": aucspec.Name})
		}
	}
	return aucspec.ConfigError(agg.Field, params, schema)
}
