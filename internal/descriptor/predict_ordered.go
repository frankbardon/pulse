package descriptor

import (
	stderrors "errors"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/orderedcat"
	"github.com/frankbardon/pulse/types"
)

// validateOrderedAggregations reports every AGG_MIN / AGG_MAX /
// AGG_MEDIAN / AGG_PERCENTILE slot whose params.ordered /
// params.levels the ordered-categorical rule (internal/orderedcat)
// refuses — the runtime constructor's (and decimal arm's) own call, so
// code, message and details agree by construction (U66 E9-S2). It
// replaces the accepted-types table's row for such a slot
// (validateFieldAcceptance skips it). Every aggregation slot a Request
// carries is walked: the top-level list, the crosstab cell(s) and the
// margin aggregations.
//
// A field neither the schema nor the projected column set carries is
// the field-reference walk's refusal, reported once there; a derived
// column (projected only) is refused ordered_needs_categorical, as the
// runtime constructor — which sees the cohort schema — refuses it. A
// weighted slot over a decimal128 field the weight resolution refused
// is not reported again (validateFieldAcceptance's rule).
func validateOrderedAggregations(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, inst *InstanceSnapshot, weightErr error) {
	if req == nil || schema == nil {
		return
	}
	weightRefusedField := ""
	if weightErr != nil {
		var werr *errors.CodedError
		if stderrors.As(weightErr, &werr) && werr.Code == errors.PULSE_WEIGHT_UNSUPPORTED {
			weightRefusedField, _ = werr.Details["value_field"].(string)
		}
	}
	check := func(a *types.Aggregation) {
		if a == nil {
			return
		}
		op := opRoute(inst, a.Type)
		if !orderedcat.Requested(op, a.Params) {
			return
		}
		if schema.Field(a.Field) == nil && !projected[a.Field] {
			return
		}
		_, cerr := orderedcat.Resolve(op, a.Field, a.Params, schema)
		if cerr == nil || (cerr.Code == errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL && a.Field == weightRefusedField) {
			return
		}
		addCodedError(env, cerr)
	}
	for _, a := range req.Aggregations {
		check(a)
	}
	if ct := req.Crosstab; ct != nil {
		for _, cell := range ct.CellAggregations() {
			check(cell)
		}
		for _, a := range ct.MarginAggregations {
			check(a)
		}
	}
}
