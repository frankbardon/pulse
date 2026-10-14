package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_FieldAcceptance_DecimalReportedOnce: an aggregation the
// accepted-types table refuses on a decimal128 field is ONE error, the
// runtime's PROCESSING_CONFIG — the decimal advisory does not repeat it,
// and Strict does not promote a second error beside it.
func TestPredict_FieldAcceptance_DecimalReportedOnce(t *testing.T) {
	data := buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 6,
			Description: "Amount in USD with 6 decimal places of precision."},
	}})
	for _, op := range []types.AggregationType{types.AGG_MODE, types.AGG_FREQUENCY} {
		for _, strict := range []bool{false, true} {
			req := &types.Request{Aggregations: []*types.Aggregation{{Type: op, Field: "amount", Params: []byte(`{"value":"1"}`)}}}
			env := predictFromBytes(data, req, &PredictOptions{Strict: strict})
			if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PROCESSING_CONFIG) {
				t.Errorf("%s strict=%v: errors %+v, want one PROCESSING_CONFIG", op, strict, env.Errors)
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
	hiddenNameParity(t, "GROUP_RANGE", "GROUP_NEVER_REGISTERED", func(name string, opts *PredictOptions) string {
		return predictOutcome(t, data, &types.Request{
			Groups:       []*types.Group{{Type: types.GroupType(name), Field: "tags"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}, opts)
	})
}
