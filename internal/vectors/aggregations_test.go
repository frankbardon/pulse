package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestResolveAggregations_Rules pins the vector-aggregator contract:
// exactly one of vector / fields, no field, a known vector, strict
// params (labels for inline fields only, one per member), and every
// other aggregator — built-in or extension — refusing a vector input,
// on aggregations, the crosstab cell and its margin aggregations.
func TestResolveAggregations_Rules(t *testing.T) {
	vm := func(mut func(*types.Aggregation)) *types.Aggregation {
		a := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"q_*"}}
		if mut != nil {
			mut(a)
		}
		return a
	}
	vecs := []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_2"}}}
	cases := []struct {
		name   string
		req    *types.Request
		code   errors.Code
		reason string
	}{
		{"ok fields", &types.Request{Aggregations: []*types.Aggregation{vm(nil)}}, "", ""},
		{"ok vector", &types.Request{Vectors: vecs, Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Fields, a.Vector = nil, "v" })}}, "", ""},
		{"ok labels", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"labels":["a","b"]}`) })}}, "", ""},
		{"field", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Field = "q_1" })}}, errors.SERVICE_VALIDATION, "field_on_vector_aggregation"},
		{"both", &types.Request{Vectors: vecs, Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Vector = "v" })}}, errors.SERVICE_VALIDATION, "vector_and_fields"},
		{"neither", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Fields = nil })}}, errors.SERVICE_VALIDATION, "no_vector_or_fields"},
		{"unknown vector", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Fields, a.Vector = nil, "nope" })}}, errors.PULSE_VECTOR_UNKNOWN, ""},
		{"unknown param", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"metric":"cosine"}`) })}}, errors.SERVICE_VALIDATION, "bad_params"},
		{"labels with vector", &types.Request{Vectors: vecs, Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) {
			a.Fields, a.Vector, a.Params = nil, "v", json.RawMessage(`{"labels":["a","b"]}`)
		})}}, errors.SERVICE_VALIDATION, "labels_with_vector"},
		{"labels count", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"labels":["a"]}`) })}}, errors.PULSE_VECTOR_LABELS_MISMATCH, ""},
		{"member type", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Fields = []string{"label"} })}}, errors.PULSE_VECTOR_MEMBER_TYPE, ""},
		{"sum with fields", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Fields: []string{"q_2"}}}}, errors.SERVICE_VALIDATION, "vector_not_accepted"},
		{"count with vector", &types.Request{Vectors: vecs, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q_1", Vector: "v"}}}, errors.SERVICE_VALIDATION, "vector_not_accepted"},
		{"crosstab cell", &types.Request{Crosstab: &types.CrosstabSpec{Cell: vm(func(a *types.Aggregation) { a.Fields = nil })}}, errors.SERVICE_VALIDATION, "no_vector_or_fields"},
		{"margin aggregation", &types.Request{Crosstab: &types.CrosstabSpec{MarginAggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q_1", Fields: []string{"q_2"}}}}}, errors.SERVICE_VALIDATION, "vector_not_accepted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveAggregations(c.req, groupSchema(), nil, nil)
			if c.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Code != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if c.reason != "" && err.Details["reason"] != c.reason {
				t.Errorf("reason = %v, want %s (%v)", err.Details["reason"], c.reason, err)
			}
		})
	}
}

// TestResolveAggregations_Extension: a registered extension aggregator
// that sets a vector input is refused (the extension contract reads one
// field); an unknown name is left to the unknown-type refusal.
func TestResolveAggregations_Extension(t *testing.T) {
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: "AGG_ACME_X", Field: "q_1", Fields: []string{"q_2"}}}}
	if _, err := ResolveAggregations(req, groupSchema(), nil, nil); err != nil {
		t.Fatalf("unknown name judged: %v", err)
	}
	_, err := ResolveAggregations(req, groupSchema(), nil, func(n string) bool { return n == "AGG_ACME_X" })
	if err == nil || err.Details["reason"] != "vector_not_accepted" {
		t.Fatalf("err = %v, want vector_not_accepted", err)
	}
}

// TestResolveAggregations_RouteHidesType: a hidden vector aggregator
// resolves nothing (its unknown-type refusal is elsewhere).
func TestResolveAggregations_RouteHidesType(t *testing.T) {
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_SUM}}}
	in, err := ResolveAggregations(req, groupSchema(), func(types.AggregationType) types.AggregationType { return "" }, nil)
	if err != nil || len(in) != 0 {
		t.Fatalf("hidden type resolved: %v %v", in, err)
	}
}

// TestAggregationSlotRewrite: the factory's slot carries the literal
// members, the default label from the slot as written and the
// resolved labels in params — a named vector's labels survive.
func TestAggregationSlotRewrite(t *testing.T) {
	req := &types.Request{
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"q_*"}, Labels: []string{"first", "second"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_SUM, Vector: "v"}, {Type: types.AGG_VEC_MEAN, Fields: []string{"q_2", "q_1"}, Label: "m"}},
	}
	in, err := ResolveAggregations(req, groupSchema(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := AggregationSlotRewrite(req.Aggregations[0], in[req.Aggregations[0]])
	if a.Label != "AGG_VEC_SUM_v" || a.Vector != "" || !reflect.DeepEqual(a.Fields, []string{"q_1", "q_2"}) ||
		string(a.Params) != `{"labels":["first","second"]}` {
		t.Errorf("named vector rewrite = %+v (params %s)", a, a.Params)
	}
	if req.Aggregations[0].Vector != "v" || req.Aggregations[0].Label != "" {
		t.Errorf("caller's slot mutated: %+v", req.Aggregations[0])
	}
	b := AggregationSlotRewrite(req.Aggregations[1], in[req.Aggregations[1]])
	if b.Label != "m" || !reflect.DeepEqual(b.Fields, []string{"q_2", "q_1"}) || string(b.Params) != `{"labels":["q_2","q_1"]}` {
		t.Errorf("inline rewrite = %+v (params %s)", b, b.Params)
	}
}

// TestAggregationInputMembers_AndReferenced: inline fields expand for
// projection (crosstab slots included); a named vector counts as
// referenced.
func TestAggregationInputMembers_AndReferenced(t *testing.T) {
	req := &types.Request{
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"q_1"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_SUM, Vector: "v"}, {Type: types.AGG_VEC_MEAN, Fields: []string{"q_*"}}},
	}
	got, ok := AggregationInputMembers(req, groupSchema())
	if !ok || !reflect.DeepEqual(got, []string{"q_1", "q_2"}) {
		t.Errorf("members = %v %v", got, ok)
	}
	if !Referenced(req)["v"] {
		t.Error("vector named by an aggregation is not referenced")
	}
	bad := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Fields: []string{"zz_*"}}}}
	if _, ok := AggregationInputMembers(bad, groupSchema()); ok {
		t.Error("an unresolvable inline set must widen the projection")
	}
}

// TestArrayValuedCrosstabRefusal: the unconditional crosstab gate —
// cell or margin aggregation, any normalize — and nothing else.
func TestArrayValuedCrosstabRefusal(t *testing.T) {
	vec := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"q_*"}}
	sum := &types.Aggregation{Type: types.AGG_SUM, Field: "q_1"}
	for _, c := range []struct {
		name string
		spec *types.CrosstabSpec
		slot string
	}{
		{"scalar", &types.CrosstabSpec{Cell: sum}, ""},
		{"cell", &types.CrosstabSpec{Cell: vec}, "crosstab.cell"},
		{"cell normalize none", &types.CrosstabSpec{Cell: vec, Normalize: types.CrosstabNormalizeNone}, "crosstab.cell"},
		{"margin", &types.CrosstabSpec{Cell: sum, MarginAggregations: []*types.Aggregation{sum, vec}}, "crosstab.margin_aggregations[1]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := ArrayValuedCrosstabRefusal(c.spec, nil)
			if c.slot == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Code != errors.PROCESSING_CONFIG || err.Details["slot"] != c.slot || err.Details["reason"] != "array_valued" {
				t.Fatalf("err = %v, want PROCESSING_CONFIG at %s", err, c.slot)
			}
		})
	}
	hidden := ArrayValuedCrosstabRefusal(&types.CrosstabSpec{Cell: vec}, func(types.AggregationType) types.AggregationType { return "" })
	if hidden != nil {
		t.Errorf("a hidden type is refused as unknown elsewhere, not here: %v", hidden)
	}
}
