package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_VectorTestInputs: a vector-input test (TEST_HOTELLING_T2)
// predicts valid and streamable with its members from a named vector
// (which then raises no PULSE_VECTOR_UNREFERENCED) or inline fields;
// every refusal of the vector leaf reaches predict as an error with the
// leaf's code, and the runtime's FieldRefRefusal returns the same one.
func TestPredict_VectorTestInputs(t *testing.T) {
	vecs := []types.VectorSpec{{Name: "items", Fields: []string{"q_1", "q_2", "q_3"}}}
	hot := func(mod func(*types.Test)) *types.Test {
		s := &types.Test{Type: types.TEST_HOTELLING_T2}
		mod(s)
		return s
	}
	cases := []struct {
		name   string
		test   *types.Test
		post   *types.Test
		code   errors.Code
		reason string
	}{
		{name: "named vector, two-sample", test: hot(func(s *types.Test) { s.Vector, s.SplitBy = "items", "region" })},
		{name: "inline fields, mu", test: hot(func(s *types.Test) {
			s.Fields = []string{"q_*"}
			s.Params = json.RawMessage(`{"mu": [1, 2, 3], "missing": "listwise"}`)
		})},
		{name: "both", test: hot(func(s *types.Test) { s.Vector, s.Fields = "items", []string{"q_1"} }), code: errors.SERVICE_VALIDATION, reason: "vector_and_fields"},
		{name: "neither", test: hot(func(s *types.Test) {}), code: errors.SERVICE_VALIDATION, reason: "no_vector_or_fields"},
		{name: "pairwise", test: hot(func(s *types.Test) {
			s.Vector = "items"
			s.Params = json.RawMessage(`{"missing": "pairwise"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "pairwise_unsupported"},
		{name: "mu length", test: hot(func(s *types.Test) {
			s.Vector = "items"
			s.Params = json.RawMessage(`{"mu": [1]}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "unknown vector", test: hot(func(s *types.Test) { s.Vector = "nope" }), code: errors.PULSE_VECTOR_UNKNOWN},
		{name: "non-numeric member", test: hot(func(s *types.Test) { s.Fields = []string{"q_1", "region"} }), code: errors.PULSE_VECTOR_MEMBER_TYPE},
		{name: "paired", test: hot(func(s *types.Test) {
			s.Vector, s.Fields2 = "items", []string{"q_3", "q_1", "q_2"}
			s.Params = json.RawMessage(`{"mu": [0, 0, 1]}`)
		})},
		{name: "unequal two-sample", test: hot(func(s *types.Test) {
			s.Vector, s.SplitBy = "items", "region"
			s.Params = json.RawMessage(`{"variance": "unequal"}`)
		})},
		{name: "paired length mismatch", test: hot(func(s *types.Test) { s.Vector, s.Fields2 = "items", []string{"q_1"} }), code: errors.SERVICE_VALIDATION, reason: "vector2_length_mismatch"},
		{name: "manova", test: &types.Test{Type: types.TEST_MANOVA, Vector: "items", SplitBy: "region"}},
		{name: "manova without split_by", test: &types.Test{Type: types.TEST_MANOVA, Vector: "items"}, code: errors.SERVICE_VALIDATION, reason: "split_by_required"},
		{name: "manova bad param", test: &types.Test{Type: types.TEST_MANOVA, Vector: "items", SplitBy: "region", Params: json.RawMessage(`{"variance": "unequal"}`)}, code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "paired with split_by", test: hot(func(s *types.Test) { s.Vector, s.Vector2, s.SplitBy = "items", "items", "region" }), code: errors.SERVICE_VALIDATION, reason: "paired_with_split_by"},
		{name: "unequal one-sample", test: hot(func(s *types.Test) {
			s.Vector = "items"
			s.Params = json.RawMessage(`{"variance": "unequal"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "variance_unequal_not_two_sample"},
		{name: "unequal paired", test: hot(func(s *types.Test) {
			s.Vector, s.Vector2 = "items", "items"
			s.Params = json.RawMessage(`{"variance": "unequal"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "variance_unequal_not_two_sample"},
		{name: "field on vector test", test: hot(func(s *types.Test) { s.Vector, s.Field = "items", "q_1" }), code: errors.SERVICE_VALIDATION, reason: "field_on_vector_test"},
		{name: "vector on a scalar test", test: &types.Test{Type: types.TEST_T, Field: "q_1", Vector: "items"}, code: errors.SERVICE_VALIDATION, reason: "vector_not_accepted"},
		{name: "vector on a post-test", post: &types.Test{Type: types.TEST_TREND, Field: "s", Vector: "items"}, code: errors.SERVICE_VALIDATION, reason: "vector_not_accepted"},
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "q_3", Type: encoding.FieldTypeF64}, {Name: "q_1", Type: encoding.FieldTypeF64},
		{Name: "q_2", Type: encoding.FieldTypeU8}, {Name: "region", Type: encoding.FieldTypeCategoricalU8},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Vectors:      vecs,
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}},
			}
			if c.test != nil {
				req.Tests = []*types.Test{c.test}
			}
			if c.post != nil {
				req.PostTests = []*types.Test{c.post}
			}
			env := predictFromBytes(vectorPredictSchema(t), req, nil)
			res := env.Data.(*descriptor.PredictResult)
			if c.code == "" {
				if len(env.Errors) != 0 || !res.Valid {
					t.Fatalf("errors = %v, want a valid request", env.Errors)
				}
				if !res.Streamable {
					t.Errorf("a Hotelling test with a streamable aggregation must predict streamable")
				}
				for _, w := range env.Warnings {
					if w.Code == string(errors.PULSE_VECTOR_UNREFERENCED) && c.test.Vector != "" {
						t.Errorf("vector referenced by the test reported unreferenced: %v", w)
					}
				}
				if err := FieldRefRefusal(req, schema, nil); err != nil {
					t.Errorf("runtime refuses a valid request: %v", err)
				}
				return
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("errors = %v, want %s", env.Errors, c.code)
			}
			if c.reason != "" && env.Errors[0].Details["reason"] != c.reason {
				t.Fatalf("reason = %v, want %s", env.Errors[0].Details["reason"], c.reason)
			}
			if res.Valid {
				t.Error("a refused request predicts valid")
			}
			rt := asCoded(t, FieldRefRefusal(req, schema, nil))
			if rt.Code != c.code || rt.Message != env.Errors[0].Message {
				t.Errorf("runtime refusal %s %q, predict %s %q", rt.Code, rt.Message, env.Errors[0].Code, env.Errors[0].Message)
			}
		})
	}
}

// TestPredict_HotellingSplitByNumericRefused: split_by must name a
// categorical field, like every split test.
func TestPredict_HotellingSplitByNumericRefused(t *testing.T) {
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}},
		Tests:        []*types.Test{{Type: types.TEST_HOTELLING_T2, Fields: []string{"q_1", "q_3"}, SplitBy: "q_2"}},
	}
	env := predictFromBytes(vectorPredictSchema(t), req, nil)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.SERVICE_VALIDATION) || env.Errors[0].Details["split_by"] != "q_2" {
		t.Fatalf("errors = %v, want one split_by SERVICE_VALIDATION", env.Errors)
	}
}
