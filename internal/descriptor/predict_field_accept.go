package descriptor

import (
	"encoding/json"
	stderrors "errors"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// validateFieldAcceptance reports every aggregation, attribute, group
// and filterer slot whose schema field the accepted-types table
// (internal/fieldaccept) refuses for its operator — the type check the
// runtime constructor makes, with the runtime's code, once per slot.
// Windows, features, regressions and tests consult the table in their
// own validators.
//
// The lookup keys on the operator's route, so a profile-hidden operator
// predicts as never registered (the table is never consulted for it).
// A field the schema does not carry (an unknown name, a derived column)
// is left to the field-reference walk; a vector field to its
// PULSE_VECTOR_FIELD_NOT_SCALAR refusal.
//
// weightErr is the request's weight-resolution refusal, if any. The
// runtime resolves weights before any aggregator dispatch, so a weighted
// slot over a decimal128 value field is refused PULSE_WEIGHT_UNSUPPORTED
// there and never reaches the decimal rule: its decimal refusal is not
// reported a second time.
func validateFieldAcceptance(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, weightErr error) {
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
	check := func(op string, slot fieldaccept.Slot, field string) {
		if op == "" {
			return
		}
		cerr := fieldaccept.Check(op, slot, field, schema)
		if cerr == nil || (cerr.Code == errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL && field == weightRefusedField) {
			return
		}
		if ownTypeValidator[op] && cerr.Code != errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL {
			return
		}
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
	}
	agg := func(a *types.Aggregation) {
		if a == nil {
			return
		}
		op := string(opRoute(inst, a.Type))
		check(op, fieldaccept.SlotField, a.Field)
		if op == string(types.AGG_RATIO) {
			// AGG_RATIO sums its two params columns, numerator first —
			// the constructor's order, after its params parse (a
			// malformed or incomplete params block is its refusal).
			var p struct {
				Num string `json:"numerator_field"`
				Den string `json:"denominator_field"`
			}
			if json.Unmarshal(a.Params, &p) != nil || p.Num == "" || p.Den == "" {
				return
			}
			if cerr := fieldaccept.Check(op, fieldaccept.SlotNumerator, p.Num, schema); cerr != nil {
				env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
				return
			}
			check(op, fieldaccept.SlotDenominator, p.Den)
		}
	}
	group := func(g *types.Group) {
		if g != nil {
			check(string(opRoute(inst, g.Type)), fieldaccept.SlotField, g.Field)
		}
	}
	for _, a := range req.Aggregations {
		agg(a)
	}
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		op := string(opRoute(inst, a.Type))
		check(op, fieldaccept.SlotField, a.Field)
		check(op, fieldaccept.SlotTarget, a.Target)
		for _, p := range a.Predictors {
			check(op, fieldaccept.SlotPredictor, p)
		}
	}
	for _, g := range req.Groups {
		group(g)
	}
	if ct := req.Crosstab; ct != nil {
		for _, g := range ct.Rows {
			group(g)
		}
		for _, g := range ct.Columns {
			group(g)
		}
		agg(ct.Cell)
		for _, a := range ct.MarginAggregations {
			agg(a)
		}
	}
	for _, f := range req.Filterers {
		if f == nil || !filterTypeChecked(opRoute(inst, f.Type), f.Values) {
			continue
		}
		check(string(opRoute(inst, f.Type)), fieldaccept.SlotField, f.Field)
	}
}

// ownTypeValidator are the operators whose own predict validator reads
// the table in the runtime constructor's order, among its other
// refusals (validateModelErrorAggs, validateCodeIn,
// validateDatePartParams): their row is not reported a second time here.
// The decimal rule is still reported here — it runs before any
// aggregator constructor.
var ownTypeValidator = map[string]bool{
	string(types.AGG_MAE): true, string(types.AGG_MAPE): true, string(types.AGG_RMSE): true,
	string(types.ATTR_CODE_IN): true, string(types.ATTR_DATE_PART): true,
}

// filterTypeChecked reports whether the table judges a filterer's field
// type. FILTER_TRUE / FILTER_FALSE read only a packed_bool in strict
// mode (no values, or ["strict"]); ["truthy"] coerces any scalar, and
// any other values list is the runtime's mode refusal, raised before the
// type is read.
func filterTypeChecked(t types.FiltererType, values []string) bool {
	if t != types.FILTER_TRUE && t != types.FILTER_FALSE {
		return true
	}
	return len(values) == 0 || (len(values) == 1 && values[0] == "strict")
}
