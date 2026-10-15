package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_FieldAcceptance_DecimalReportedOnce: a built-in
// aggregation with no decimal arm on a decimal128 field is ONE error,
// the runtime's PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL (the table's
// decimal rule runs before the operator's own row, as the runtime's
// decimal dispatch runs before its constructor) — never a warning, and
// Strict does not promote a second error beside it.
func TestPredict_FieldAcceptance_DecimalReportedOnce(t *testing.T) {
	data := buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6,
			Description: "Amount in USD with 6 decimal places of precision."},
	}})
	for _, op := range []types.AggregationType{types.AGG_MODE, types.AGG_FREQUENCY, types.AGG_SET_UNION, types.AGG_MEDIAN, types.AGG_NULL_COUNT} {
		for _, strict := range []bool{false, true} {
			agg := &types.Aggregation{Type: op, Field: "amount"}
			if op == types.AGG_FREQUENCY { // the only one of them taking params.value
				agg.Params = []byte(`{"value":"1"}`)
			}
			req := &types.Request{Aggregations: []*types.Aggregation{agg}}
			env := predictFromBytes(data, req, &PredictOptions{Strict: strict})
			if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL) {
				t.Errorf("%s strict=%v: errors %+v, want one PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL", op, strict, env.Errors)
			}
			for _, w := range env.Warnings {
				if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL) {
					t.Errorf("%s strict=%v: decimal warning beside the refusal", op, strict)
				}
			}
		}
	}
}

// TestPredict_FieldAcceptance_HiddenOperatorIsNeverRegistered: the table
// is consulted on the operator's route, so a profile-hidden set operator
// on a non-set field predicts like a never-registered name, not with the
// table's type refusal.
func TestPredict_FieldAcceptance_HiddenOperatorIsNeverRegistered(t *testing.T) {
	schema := hiddenPredictSchema(t)
	schema.Fields = append(schema.Fields, encoding.Field{Name: "tags", Type: encoding.FieldTypeSetU8,
		Dictionary: makeDictionary(t, "a", "b"), Description: "Multi-select tags for the exam sitting"})
	data := buildTestPulseFile(t, schema)
	hiddenNameParity(t, "FILTER_SET_CONTAINS_ANY", "FILTER_NEVER_REGISTERED", func(name string, opts *PredictOptions) string {
		return predictOutcome(t, data, &types.Request{
			Filterers:    []*types.Filterer{{Type: types.FiltererType(name), Field: "score", Values: []string{"a"}}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}, opts)
	})
	hiddenNameParity(t, "GROUP_RANGE", "GROUP_NEVER_REGISTERED", func(name string, opts *PredictOptions) string {
		return predictOutcome(t, data, &types.Request{
			Groups:       []*types.Group{{Type: types.GroupType(name), Field: "tags"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}, opts)
	})
}

// TestPredict_FieldAcceptance_FilterTruthyMode: FILTER_TRUE / FILTER_FALSE
// read only a packed_bool in strict mode; values ["truthy"] coerces any
// scalar and is never refused on type.
func TestPredict_FieldAcceptance_FilterTruthyMode(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	for _, c := range []struct {
		values []string
		errs   int
	}{{nil, 1}, {[]string{"strict"}, 1}, {[]string{"truthy"}, 0}} {
		for _, op := range []types.FiltererType{types.FILTER_TRUE, types.FILTER_FALSE} {
			req := &types.Request{
				Filterers:    []*types.Filterer{{Type: op, Field: "score", Values: c.values}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
			}
			env := predictFromBytes(data, req, nil)
			if len(env.Errors) != c.errs {
				t.Errorf("%s values=%v: errors %+v, want %d", op, c.values, env.Errors, c.errs)
			}
		}
	}
}
