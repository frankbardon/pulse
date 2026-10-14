package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
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
		{"margin aggregation", &types.Request{Crosstab: &types.CrosstabSpec{MarginAggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q_1", Fields: []string{"q_2"}}}}}, errors.SERVICE_VALIDATION, "vector_not_accepted"}, {"element cell", &types.Request{Crosstab: &types.CrosstabSpec{Cell: vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"element":"q_2"}`) })}}, "", ""},
		{"element margin", &types.Request{Crosstab: &types.CrosstabSpec{MarginAggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"element":"q_1"}`) })}}}, "", ""},
		{"element by label", &types.Request{Crosstab: &types.CrosstabSpec{Cell: vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"labels":["a","b"],"element":"b"}`) })}}, "", ""},
		{"element unknown", &types.Request{Crosstab: &types.CrosstabSpec{Cell: vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"element":"q_9"}`) })}}, errors.SERVICE_VALIDATION, "element_unknown"},
		{"element ambiguous", &types.Request{Crosstab: &types.CrosstabSpec{Cell: vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"labels":["a","a"],"element":"a"}`) })}}, errors.SERVICE_VALIDATION, "element_ambiguous"},
		{"element with expand", &types.Request{Crosstab: &types.CrosstabSpec{Cell: vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"element":"q_1","expand":true}`) })}}, errors.SERVICE_VALIDATION, "element_with_expand"},
		{"element outside crosstab", &types.Request{Aggregations: []*types.Aggregation{vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"element":"q_1"}`) })}}, errors.SERVICE_VALIDATION, "element_outside_crosstab"},
		{"element not a string", &types.Request{Crosstab: &types.CrosstabSpec{Cell: vm(func(a *types.Aggregation) { a.Params = json.RawMessage(`{"element":1}`) })}}, errors.SERVICE_VALIDATION, "bad_params"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveAggregations(c.req, groupSchema(), AggregationKinds{})
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

// extKinds declares AGG_ACME_X a plain extension, AGG_ACME_V a
// VectorInput one and AGG_ACME_A a VectorInput + ArrayValued one.
func extKinds() AggregationKinds {
	return AggregationKinds{Extension: func(n string) ExtensionAggregation {
		switch n {
		case "AGG_ACME_X":
			return ExtensionAggregation{Registered: true}
		case "AGG_ACME_V":
			return ExtensionAggregation{Registered: true, VectorInput: true}
		case "AGG_ACME_A":
			return ExtensionAggregation{Registered: true, VectorInput: true, ArrayValued: true}
		}
		return ExtensionAggregation{}
	}}
}

// TestResolveAggregations_Extension: a plain registered extension
// aggregator refuses a vector input; one declared VectorInput resolves
// by the built-in rules — its own params kept, labels / expand read
// leniently, expand only when ArrayValued; an unknown name is left to
// the unknown-type refusal.
func TestResolveAggregations_Extension(t *testing.T) {
	agg := func(typ, field string, fields []string, params string) *types.Request {
		a := &types.Aggregation{Type: types.AggregationType(typ), Field: field, Fields: fields}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		return &types.Request{Aggregations: []*types.Aggregation{a}}
	}
	cases := []struct {
		name   string
		req    *types.Request
		kinds  AggregationKinds
		code   errors.Code
		reason string
		n      int // resolved slots
	}{
		{"unknown name, no extensions", agg("AGG_ACME_X", "q_1", []string{"q_2"}, ""), AggregationKinds{}, "", "", 0},
		{"plain extension refuses", agg("AGG_ACME_X", "q_1", []string{"q_2"}, ""), extKinds(), errors.SERVICE_VALIDATION, "vector_not_accepted", 0},
		{"vector extension resolves", agg("AGG_ACME_V", "", []string{"q_*"}, `{"k":3}`), extKinds(), "", "", 1},
		{"vector extension labels", agg("AGG_ACME_V", "", []string{"q_1", "q_2"}, `{"k":3,"labels":["a","b"]}`), extKinds(), "", "", 1},
		{"vector extension bad labels", agg("AGG_ACME_V", "", []string{"q_1"}, `{"labels":"a"}`), extKinds(), errors.SERVICE_VALIDATION, "bad_params", 0},
		{"vector extension field", agg("AGG_ACME_V", "q_1", []string{"q_2"}, ""), extKinds(), errors.SERVICE_VALIDATION, "field_on_vector_aggregation", 0},
		{"vector extension neither", agg("AGG_ACME_V", "", nil, ""), extKinds(), errors.SERVICE_VALIDATION, "no_vector_or_fields", 0},
		{"scalar extension expand", agg("AGG_ACME_V", "", []string{"q_1"}, `{"expand":true}`), extKinds(), errors.SERVICE_VALIDATION, "expand_not_array_valued", 0},
		{"array extension expand", agg("AGG_ACME_A", "", []string{"q_1", "q_2"}, `{"expand":true}`), extKinds(), "", "", 1},
		{"array extension member type", agg("AGG_ACME_A", "", []string{"label"}, ""), extKinds(), errors.PULSE_VECTOR_MEMBER_TYPE, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := ResolveAggregations(c.req, groupSchema(), c.kinds)
			if c.code == "" {
				if err != nil || len(in) != c.n {
					t.Fatalf("resolved %d (%v), want %d", len(in), err, c.n)
				}
				return
			}
			if err == nil || err.Code != c.code || (c.reason != "" && err.Details["reason"] != c.reason) {
				t.Fatalf("err = %v, want %s %s", err, c.code, c.reason)
			}
		})
	}
}

// TestAggregationKinds_Extension: the instance view classifies a
// declared extension like a built-in — vector input, array value,
// expand and its output columns, the crosstab refusal — and a hidden
// name like an unknown one.
func TestAggregationKinds_Extension(t *testing.T) {
	k := extKinds()
	if !k.IsVector("AGG_ACME_V") || k.IsArrayValued("AGG_ACME_V") || !k.IsArrayValued("AGG_ACME_A") || k.IsVector("AGG_ACME_X") {
		t.Fatal("extension declarations not read")
	}
	if !k.IsVector(types.AGG_VEC_SUM) || !k.IsArrayValued(types.AGG_VEC_MEAN) || k.IsArrayValued(types.AGG_SUM) {
		t.Fatal("built-ins misread")
	}
	hidden := AggregationKinds{Route: func(types.AggregationType) types.AggregationType { return "" }, Extension: k.Extension}
	if hidden.IsArrayValued("AGG_ACME_A") || hidden.IsVector("AGG_ACME_A") {
		t.Fatal("a hidden extension still classified")
	}
	a := &types.Aggregation{Type: "AGG_ACME_A", Fields: []string{"q_1", "q_2"}, Label: "p", Params: json.RawMessage(`{"expand":true,"k":1}`)}
	if !AggregationExpands(a, k) || AggregationExpands(a, AggregationKinds{}) {
		t.Fatal("AggregationExpands ignores the extension declaration")
	}
	cols, ok := AggregationOutputColumns(&types.Request{}, a, groupSchema(), k)
	if !ok || !reflect.DeepEqual(cols, []string{"p[q_1]", "p[q_2]"}) {
		t.Fatalf("columns = %v %v", cols, ok)
	}
	if err := ArrayValuedCrosstabRefusal(&types.CrosstabSpec{Cell: a}, k); err == nil || err.Details["reason"] != "array_valued" {
		t.Fatalf("crosstab refusal = %v", err)
	}
	if err := ArrayValuedCrosstabRefusal(&types.CrosstabSpec{Cell: a}, AggregationKinds{}); err != nil {
		t.Fatalf("built-in view refused an unknown name: %v", err)
	}
}

// TestAggregationKinds_NonScalar: the non-scalar class is the built-in
// rich lifts plus the array-valued class plus an extension declared
// NonScalar; a hidden type is not classed.
func TestAggregationKinds_NonScalar(t *testing.T) {
	k := AggregationKinds{Extension: func(name string) ExtensionAggregation {
		switch name {
		case "AGG_ACME_N":
			return ExtensionAggregation{Registered: true, NonScalar: true}
		case "AGG_ACME_A":
			return ExtensionAggregation{Registered: true, VectorInput: true, ArrayValued: true}
		case "AGG_ACME_X":
			return ExtensionAggregation{Registered: true}
		}
		return ExtensionAggregation{}
	}}
	for _, typ := range []types.AggregationType{types.AGG_WELFORD, types.AGG_SET_UNION, types.AGG_SET_INTERSECTION,
		types.AGG_SET_FREQUENCY, types.AGG_VEC_MEAN, types.AGG_VEC_SUM, "AGG_ACME_N", "AGG_ACME_A"} {
		if !k.IsNonScalar(typ) {
			t.Errorf("%s not classed non-scalar", typ)
		}
	}
	for _, typ := range []types.AggregationType{types.AGG_SUM, types.AGG_MODE, types.AGG_SET_CARDINALITY_SUM, "AGG_ACME_X", "AGG_ACME_UNKNOWN"} {
		if k.IsNonScalar(typ) {
			t.Errorf("%s classed non-scalar", typ)
		}
	}
	hidden := AggregationKinds{Route: func(types.AggregationType) types.AggregationType { return "" }, Extension: k.Extension}
	if hidden.IsNonScalar(types.AGG_WELFORD) || hidden.IsNonScalar("AGG_ACME_N") {
		t.Error("a hidden type still classed non-scalar")
	}
}

// TestAggregationKinds_Code: a categorical code is an input-valued
// aggregation (built-in AGG_MODE, or an extension declared
// EmitsInputValue) over a categorical_* or packed_bool input; over a
// numeric or date input it is a magnitude, and a figure computed over
// a categorical (AGG_MODE_COUNT, AGG_DISTINCT_COUNT) is never a code.
func TestAggregationKinds_Code(t *testing.T) {
	k := AggregationKinds{Extension: func(name string) ExtensionAggregation {
		switch name {
		case "AGG_ACME_PICK":
			return ExtensionAggregation{Registered: true, EmitsInputValue: true}
		case "AGG_ACME_X":
			return ExtensionAggregation{Registered: true}
		}
		return ExtensionAggregation{}
	}}
	codeInputs := []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32, encoding.FieldTypePackedBool}
	magnitudeInputs := []encoding.FieldType{encoding.FieldTypeF64, encoding.FieldTypeU8, encoding.FieldTypeU4, encoding.FieldTypeDate, encoding.FieldTypeDateTime, encoding.FieldTypeDecimal128}
	for _, typ := range []types.AggregationType{types.AGG_MODE, "AGG_ACME_PICK"} {
		for _, ft := range codeInputs {
			if !k.IsCode(typ, ft) {
				t.Errorf("%s over %s not classed a code", typ, ft)
			}
		}
		for _, ft := range magnitudeInputs {
			if k.IsCode(typ, ft) {
				t.Errorf("%s over %s classed a code", typ, ft)
			}
		}
	}
	for _, typ := range []types.AggregationType{types.AGG_MODE_COUNT, types.AGG_DISTINCT_COUNT, types.AGG_FREQUENCY, types.AGG_COUNT, "AGG_ACME_X", "AGG_ACME_UNKNOWN"} {
		for _, ft := range codeInputs {
			if k.IsCode(typ, ft) {
				t.Errorf("%s over %s classed a code", typ, ft)
			}
		}
	}
	hidden := AggregationKinds{Route: func(types.AggregationType) types.AggregationType { return "" }, Extension: k.Extension}
	if hidden.IsCode(types.AGG_MODE, encoding.FieldTypeCategoricalU8) || hidden.IsCode("AGG_ACME_PICK", encoding.FieldTypeCategoricalU8) {
		t.Error("a hidden type still classed a code")
	}
}

// TestAggregationSlotRewrite_ExtensionKeepsParams: an extension's
// rewritten slot keeps its own params beside the resolved labels.
func TestAggregationSlotRewrite_ExtensionKeepsParams(t *testing.T) {
	a := &types.Aggregation{Type: "AGG_ACME_A", Fields: []string{"q_*"}, Params: json.RawMessage(`{"k":3,"expand":true}`)}
	req := &types.Request{Aggregations: []*types.Aggregation{a}}
	in, err := ResolveAggregations(req, groupSchema(), extKinds())
	if err != nil {
		t.Fatal(err)
	}
	rw := AggregationSlotRewrite(a, in[a])
	var got map[string]any
	if err := json.Unmarshal(rw.Params, &got); err != nil {
		t.Fatal(err)
	}
	if got["k"] != float64(3) || got["expand"] != true || len(got["labels"].([]any)) != len(rw.Fields) || rw.Vector != "" {
		t.Fatalf("rewrite = %+v params %s", rw, rw.Params)
	}
	if string(a.Params) != `{"k":3,"expand":true}` {
		t.Fatal("caller's slot mutated")
	}
}

// TestResolveAggregations_RouteHidesType: a hidden vector aggregator
// resolves nothing (its unknown-type refusal is elsewhere).
func TestResolveAggregations_RouteHidesType(t *testing.T) {
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_SUM}}}
	in, err := ResolveAggregations(req, groupSchema(), AggregationKinds{Route: func(types.AggregationType) types.AggregationType { return "" }})
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
	in, err := ResolveAggregations(req, groupSchema(), AggregationKinds{})
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
			err := ArrayValuedCrosstabRefusal(c.spec, AggregationKinds{})
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
	elem := &types.Aggregation{Type: types.AGG_VEC_SUM, Fields: []string{"q_*"}, Params: json.RawMessage(`{"element":"q_1"}`)}
	if err := ArrayValuedCrosstabRefusal(&types.CrosstabSpec{Cell: elem, MarginAggregations: []*types.Aggregation{elem}}, AggregationKinds{}); err != nil {
		t.Errorf("an element slot is a scalar: %v", err)
	}
	// An extension's params.element is its own key: still refused.
	extElem := &types.Aggregation{Type: "AGG_ACME_A", Fields: []string{"q_*"}, Params: json.RawMessage(`{"element":"q_1"}`)}
	if err := ArrayValuedCrosstabRefusal(&types.CrosstabSpec{Cell: extElem}, extKinds()); err == nil || err.Details["reason"] != "array_valued" {
		t.Errorf("an extension element slot = %v, want array_valued", err)
	}
	hidden := ArrayValuedCrosstabRefusal(&types.CrosstabSpec{Cell: vec}, AggregationKinds{Route: func(types.AggregationType) types.AggregationType { return "" }})
	if hidden != nil {
		t.Errorf("a hidden type is refused as unknown elsewhere, not here: %v", hidden)
	}
}

// TestAggregationOutputColumns: an array slot writes its label; an
// expanding slot writes "<label>[<element label>]" per member in member
// order (a named vector's labels, params.labels, else the members) and
// keeps expand through the slot rewrite; an unresolvable expanding slot
// falls back to its label and reports it.
func TestAggregationOutputColumns(t *testing.T) {
	vecs := []types.VectorSpec{{Name: "v", Fields: []string{"q_*"}, Labels: []string{"first", "second"}}}
	exp := json.RawMessage(`{"expand":true}`)
	cases := []struct {
		name   string
		agg    *types.Aggregation
		want   []string
		ok     bool
		expand bool
	}{
		{"array", &types.Aggregation{Type: types.AGG_VEC_MEAN, Vector: "v"}, []string{"AGG_VEC_MEAN_v"}, true, false},
		{"scalar", &types.Aggregation{Type: types.AGG_SUM, Field: "q_1"}, []string{"AGG_SUM_q_1"}, true, false},
		{"named vector", &types.Aggregation{Type: types.AGG_VEC_MEAN, Vector: "v", Params: exp}, []string{"AGG_VEC_MEAN_v[first]", "AGG_VEC_MEAN_v[second]"}, true, true},
		{"inline fields", &types.Aggregation{Type: types.AGG_VEC_SUM, Fields: []string{"q_2", "q_1"}, Label: "s", Params: exp}, []string{"s[q_2]", "s[q_1]"}, true, true},
		{"inline labels", &types.Aggregation{Type: types.AGG_VEC_SUM, Fields: []string{"q_*"}, Label: "s",
			Params: json.RawMessage(`{"expand":true,"labels":["a","b"]}`)}, []string{"s[a]", "s[b]"}, true, true},
		{"unresolvable", &types.Aggregation{Type: types.AGG_VEC_SUM, Vector: "nope", Label: "s", Params: exp}, []string{"s"}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Vectors: vecs, Aggregations: []*types.Aggregation{c.agg}}
			got, ok := AggregationOutputColumns(req, c.agg, groupSchema(), AggregationKinds{})
			if !reflect.DeepEqual(got, c.want) || ok != c.ok {
				t.Errorf("columns = %v, %v; want %v, %v", got, ok, c.want, c.ok)
			}
			if AggregationExpands(c.agg, AggregationKinds{}) != c.expand {
				t.Errorf("AggregationExpands = %v, want %v", !c.expand, c.expand)
			}
		})
	}

	// expand survives the factory rewrite (the runtime reads it there).
	a := &types.Aggregation{Type: types.AGG_VEC_MEAN, Vector: "v", Params: exp}
	req := &types.Request{Vectors: vecs, Aggregations: []*types.Aggregation{a}}
	in, err := ResolveAggregations(req, groupSchema(), AggregationKinds{})
	if err != nil {
		t.Fatal(err)
	}
	if !in[a].Expand {
		t.Error("resolved input lost expand")
	}
	if rw := AggregationSlotRewrite(a, in[a]); !AggregationExpands(rw, AggregationKinds{}) {
		t.Errorf("rewrite dropped expand: %s", rw.Params)
	}

	// Duplicate element labels: refused under expand only.
	dup := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"q_*"}, Params: json.RawMessage(`{"expand":true,"labels":["x","x"]}`)}
	if _, err := ResolveAggregations(&types.Request{Aggregations: []*types.Aggregation{dup}}, groupSchema(), AggregationKinds{}); err == nil ||
		err.Details["reason"] != "expand_duplicate_labels" {
		t.Errorf("duplicate labels under expand = %v, want expand_duplicate_labels", err)
	}
	dup.Params = json.RawMessage(`{"labels":["x","x"]}`)
	if _, err := ResolveAggregations(&types.Request{Aggregations: []*types.Aggregation{dup}}, groupSchema(), AggregationKinds{}); err != nil {
		t.Errorf("duplicate labels on an array slot refused: %v", err)
	}
}

// TestAggregationColumns: the series-overlay column lister. A scalar
// slot writes its label; an expanding slot its element columns over
// the schema (read once, only for an expanding slot), or — no schema —
// from its inline element labels (the prepared slot the runtime
// holds), else unknown. The prepared slot lists exactly the columns
// predict reads over the schema.
func TestAggregationColumns(t *testing.T) {
	vecs := []types.VectorSpec{{Name: "v", Fields: []string{"q_*"}, Labels: []string{"first", "second"}}}
	exp := json.RawMessage(`{"expand":true}`)
	aggs := []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "q_1", Label: "n"},
		{Type: types.AGG_VEC_MEAN, Vector: "v", Label: "m", Params: exp},
		{Type: types.AGG_VEC_SUM, Fields: []string{"q_*"}, Label: "s", Params: json.RawMessage(`{"expand":true,"labels":["a","b"]}`)},
		{Type: types.AGG_VEC_SUM, Fields: []string{"q_*"}, Label: "u"},
		nil,
	}
	req := &types.Request{Vectors: vecs, Aggregations: aggs}
	loads := 0
	withSchema := AggregationColumns(req, func() *encoding.Schema { loads++; return groupSchema() }, AggregationKinds{})
	noSchema := AggregationColumns(req, nil, AggregationKinds{})
	for i, c := range []struct {
		with, without []string
		knownWithout  bool
	}{
		{[]string{"n"}, []string{"n"}, true},
		{[]string{"m[first]", "m[second]"}, nil, false},
		{[]string{"s[a]", "s[b]"}, []string{"s[a]", "s[b]"}, true},
		{[]string{"u"}, []string{"u"}, true},
		{nil, nil, true},
	} {
		if got, known := withSchema(i); !reflect.DeepEqual(got, c.with) || !known {
			t.Errorf("schema: aggregation %d = %v, %v; want %v", i, got, known, c.with)
		}
		if got, known := noSchema(i); !reflect.DeepEqual(got, c.without) || known != c.knownWithout {
			t.Errorf("no schema: aggregation %d = %v, %v; want %v, %v", i, got, known, c.without, c.knownWithout)
		}
	}
	if loads != 1 {
		t.Errorf("schema loaded %d times, want once", loads)
	}
	if got, known := withSchema(9); got != nil || !known {
		t.Errorf("out of range = %v, %v", got, known)
	}

	// The prepared slot (the factory rewrite the runtime holds) lists,
	// with no schema, the columns the authored one does over it.
	in, err := ResolveAggregations(req, groupSchema(), AggregationKinds{})
	if err != nil {
		t.Fatal(err)
	}
	prepared := AggregationSlotRewrite(aggs[1], in[aggs[1]])
	got, known := AggregationColumns(&types.Request{Aggregations: []*types.Aggregation{prepared}}, nil, AggregationKinds{})(0)
	if !known || !reflect.DeepEqual(got, []string{"m[first]", "m[second]"}) {
		t.Errorf("prepared slot = %v, %v; want the schema's columns", got, known)
	}
}

// TestElementSelection: SelectsElement / ElementMarginClass classify a
// built-in element slot as its scalar twin's margin class, and the slot
// rewrite narrows it to the one selected member (labels too).
func TestElementSelection(t *testing.T) {
	mk := func(typ types.AggregationType, params string) *types.Aggregation {
		return &types.Aggregation{Type: typ, Fields: []string{"q_*"}, Label: "x", Params: json.RawMessage(params)}
	}
	for _, c := range []struct {
		a     *types.Aggregation
		kinds AggregationKinds
		class types.MarginReducibility
		ok    bool
	}{
		{mk(types.AGG_VEC_MEAN, `{"element":"q_2"}`), AggregationKinds{}, types.MarginMeanReducible, true},
		{mk(types.AGG_VEC_SUM, `{"element":"q_2"}`), AggregationKinds{}, types.MarginSummable, true},
		{mk(types.AGG_VEC_MEAN, `{}`), AggregationKinds{}, "", false},
		{mk(types.AGG_VEC_MEAN, `{"element":3}`), AggregationKinds{}, "", false},
		{mk("AGG_ACME_A", `{"element":"q_2"}`), extKinds(), "", false},
		{mk(types.AGG_VEC_MEAN, `{"element":"q_2"}`), AggregationKinds{Route: func(types.AggregationType) types.AggregationType { return "" }}, "", false},
	} {
		class, ok := ElementMarginClass(c.a, c.kinds)
		if class != c.class || ok != c.ok || SelectsElement(c.a, c.kinds) != c.ok {
			t.Errorf("%s %s: class (%q, %v), want (%q, %v)", c.a.Type, c.a.Params, class, ok, c.class, c.ok)
		}
	}

	a := mk(types.AGG_VEC_MEAN, `{"labels":["first","second"],"element":"second"}`)
	req := &types.Request{Crosstab: &types.CrosstabSpec{Cell: a}}
	in, err := ResolveAggregations(req, groupSchema(), AggregationKinds{})
	if err != nil {
		t.Fatal(err)
	}
	if !in[a].HasElement || in[a].Element != 1 {
		t.Fatalf("input = %+v, want element 1", in[a])
	}
	rw := AggregationSlotRewrite(a, in[a])
	if !reflect.DeepEqual(rw.Fields, []string{in[a].Members.Members[1]}) {
		t.Errorf("rewritten fields = %v, want the second member alone", rw.Fields)
	}
	p, perr := DecodeVecAggParams("x", rw.Type, rw.Params)
	if perr != nil || !reflect.DeepEqual(p.Labels, []string{"second"}) || p.Element == nil || *p.Element != "second" {
		t.Errorf("rewritten params = %s (%v)", rw.Params, perr)
	}
	// A slot without element keeps every member.
	b := mk(types.AGG_VEC_MEAN, `{}`)
	in, _ = ResolveAggregations(&types.Request{Crosstab: &types.CrosstabSpec{Cell: b}}, groupSchema(), AggregationKinds{})
	if in[b].HasElement || len(AggregationSlotRewrite(b, in[b]).Fields) != len(in[b].Members.Members) {
		t.Errorf("a slot without element was narrowed: %+v", in[b])
	}
}
