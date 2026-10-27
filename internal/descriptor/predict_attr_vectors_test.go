package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_VectorAttributeInputs: a fitted vector attribute
// (ATTR_MAHALANOBIS) predicts valid and streamable (two-pass) with its
// members from a named vector (then not reported unreferenced) or inline
// fields, its fit_on filterers judged by the filterer rule against the
// attribute-step columns; every refusal reaches predict with the
// runtime's code and message.
func TestPredict_VectorAttributeInputs(t *testing.T) {
	t.Parallel()

	vecs := []types.VectorSpec{{Name: "items", Fields: []string{"q_1", "q_2", "q_3"}}}
	maha := func(mod func(*types.Attribute)) *types.Attribute {
		a := &types.Attribute{Type: types.ATTR_MAHALANOBIS}
		mod(a)
		return a
	}
	zq := &types.Attribute{Type: types.ATTR_ZSCORE, Field: "q_1", Label: "zq"}
	cases := []struct {
		name   string
		attrs  []*types.Attribute
		groups bool
		code   errors.Code
		reason string
		field  string
	}{
		{name: "named vector", attrs: []*types.Attribute{maha(func(a *types.Attribute) { a.Vector = "items" })}},
		{name: "inline glob, p output", attrs: []*types.Attribute{maha(func(a *types.Attribute) {
			a.Fields = []string{"q_*"}
			a.Params = json.RawMessage(`{"output": "p", "fit_on": "filtered"}`)
		})}},
		{name: "fit_on a source column", attrs: []*types.Attribute{maha(func(a *types.Attribute) {
			a.Vector = "items"
			a.Params = json.RawMessage(`{"fit_on": [{"type": "FILTER_INCLUDE", "field": "region", "values": ["N"]}]}`)
		})}},
		{name: "fit_on an earlier attribute", attrs: []*types.Attribute{zq, maha(func(a *types.Attribute) {
			a.Vector = "items"
			a.Params = json.RawMessage(`{"fit_on": [{"type": "FILTER_RANGE", "field": "zq", "values": ["-2", "2"]}]}`)
		})}},
		{name: "grouped runs buffered", groups: true, attrs: []*types.Attribute{maha(func(a *types.Attribute) { a.Vector = "items" })}},
		{name: "fit_on unknown field", attrs: []*types.Attribute{maha(func(a *types.Attribute) {
			a.Vector = "items"
			a.Params = json.RawMessage(`{"fit_on": [{"type": "FILTER_INCLUDE", "field": "nope", "values": ["N"]}]}`)
		})}, code: errors.SERVICE_VALIDATION, field: "nope"},
		{name: "fit_on total()", attrs: []*types.Attribute{maha(func(a *types.Attribute) {
			a.Vector = "items"
			a.Params = json.RawMessage(`{"fit_on": [{"type": "FILTER_EXPRESSION", "expression": "q_1 > total(q_1)"}]}`)
		})}, code: errors.PULSE_FORMULA_TOTAL_SITE},
		{name: "bad fit_on", attrs: []*types.Attribute{maha(func(a *types.Attribute) {
			a.Vector = "items"
			a.Params = json.RawMessage(`{"fit_on": "everything"}`)
		})}, code: errors.SERVICE_VALIDATION, reason: "bad_fit_on"},
		{name: "unknown vector", attrs: []*types.Attribute{maha(func(a *types.Attribute) { a.Vector = "nope" })}, code: errors.PULSE_VECTOR_UNKNOWN},
		{name: "non-numeric member", attrs: []*types.Attribute{maha(func(a *types.Attribute) { a.Fields = []string{"q_1", "region"} })}, code: errors.PULSE_VECTOR_MEMBER_TYPE},
		{name: "neither", attrs: []*types.Attribute{maha(func(*types.Attribute) {})}, code: errors.SERVICE_VALIDATION, reason: "no_vector_or_fields"},
		{name: "field on vector attribute", attrs: []*types.Attribute{maha(func(a *types.Attribute) { a.Vector, a.Field = "items", "q_1" })}, code: errors.SERVICE_VALIDATION, reason: "field_on_vector_attribute"},
		{name: "pc score", attrs: []*types.Attribute{{Type: types.ATTR_PC_SCORE, Vector: "items", Params: json.RawMessage(`{"component": 2, "rotation": "varimax", "components": 3}`)}}},
		{name: "pc score beyond the members", attrs: []*types.Attribute{{Type: types.ATTR_PC_SCORE, Vector: "items", Params: json.RawMessage(`{"component": 4}`)}},
			code: errors.SERVICE_VALIDATION, reason: "component_out_of_range"},
		{name: "pc score without component", attrs: []*types.Attribute{{Type: types.ATTR_PC_SCORE, Vector: "items"}}, code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "factor score", attrs: []*types.Attribute{{Type: types.ATTR_FACTOR_SCORE, Vector: "items", Params: json.RawMessage(`{"factor": 1, "factors": 1, "method": "bartlett", "rotation": "varimax"}`)}}},
		{name: "factor score beyond the members", attrs: []*types.Attribute{{Type: types.ATTR_FACTOR_SCORE, Vector: "items", Params: json.RawMessage(`{"factor": 4}`)}},
			code: errors.SERVICE_VALIDATION, reason: "factor_out_of_range"},
		{name: "factor score without factor", attrs: []*types.Attribute{{Type: types.ATTR_FACTOR_SCORE, Vector: "items"}}, code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "vector on a scalar attribute", attrs: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "q_1", Vector: "items"}}, code: errors.SERVICE_VALIDATION, reason: "vector_not_accepted"},
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "q_3", Type: encoding.FieldTypeF64}, {Name: "q_1", Type: encoding.FieldTypeF64},
		{Name: "q_2", Type: encoding.FieldTypeU8}, {Name: "region", Type: encoding.FieldTypeCategoricalU8},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Vectors:      vecs,
				Attributes:   c.attrs,
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}},
			}
			if c.groups {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
			}
			env := predictFromBytes(vectorPredictSchema(t), req, nil)
			res := env.Data.(*descriptor.PredictResult)
			if c.code == "" {
				if len(env.Errors) != 0 || !res.Valid {
					t.Fatalf("errors = %v, want a valid request", env.Errors)
				}
				reasons := strings.Join(res.StreamableReasons, " ")
				if c.groups {
					if res.Streamable || !strings.Contains(reasons, "two-pass attribute ATTR_MAHALANOBIS") {
						t.Errorf("a grouped fitted attribute must predict buffered: streamable %v, reasons %v", res.Streamable, res.StreamableReasons)
					}
				} else if !res.Streamable {
					t.Errorf("a lone fitted attribute must predict streamable (two-pass): %v", res.StreamableReasons)
				}
				named := false
				for _, a := range c.attrs {
					named = named || a.Vector != ""
				}
				for _, w := range env.Warnings {
					if named && w.Code == string(errors.PULSE_VECTOR_UNREFERENCED) {
						t.Errorf("vector referenced by the attribute reported unreferenced: %v", w)
					}
				}
				if err := FieldRefRefusal(req, schema, nil); err != nil {
					t.Errorf("runtime refuses a valid request: %v", err)
				}
				return
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				for _, e := range env.Errors {
					t.Logf("error %s: %s %v", e.Code, e.Message, e.Details)
				}
				t.Fatalf("errors = %v, want %s", env.Errors, c.code)
			}
			if c.reason != "" && env.Errors[0].Details["reason"] != c.reason {
				t.Fatalf("reason = %v, want %s", env.Errors[0].Details["reason"], c.reason)
			}
			if c.field != "" && env.Errors[0].Details["field"] != c.field {
				t.Fatalf("field = %v, want %s", env.Errors[0].Details["field"], c.field)
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

// TestAttributeDefaultLabel_VectorAttributes: predict projects the
// runtime's default label, so a later slot can read it, and a label
// that shadows a field is refused.
func TestAttributeDefaultLabel_VectorAttributes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		attr *types.Attribute
		want string
	}{
		{&types.Attribute{Type: types.ATTR_MAHALANOBIS, Vector: "items"}, "ATTR_MAHALANOBIS_items"},
		{&types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: []string{"q_2", "q_1"}}, "ATTR_MAHALANOBIS_q_2"},
		{&types.Attribute{Type: types.ATTR_PC_SCORE, Vector: "items", Params: json.RawMessage(`{"component": 2}`)}, "ATTR_PC_SCORE_items_PC2"},
		{&types.Attribute{Type: types.ATTR_FACTOR_SCORE, Vector: "items", Params: json.RawMessage(`{"factor": 2}`)}, "ATTR_FACTOR_SCORE_items_F2"},
	} {
		if got := attributeDefaultLabel(c.attr, nil); got != c.want {
			t.Errorf("label %q, want %q", got, c.want)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "q_1", Type: encoding.FieldTypeF64}, {Name: "q_2", Type: encoding.FieldTypeF64}}}
	req := &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Fields: []string{"q_1", "q_2"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "ATTR_MAHALANOBIS_q_1"}},
	}
	if err := FieldRefRefusal(req, schema, nil); err != nil {
		t.Fatalf("the default label is not visible downstream: %v", err)
	}
}
