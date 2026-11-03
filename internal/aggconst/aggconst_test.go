package aggconst_test

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/aggconst"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/types"
)

func ft(name string) encoding.FieldType {
	t, ok := encoding.ParseFieldType(name)
	if !ok {
		panic(name)
	}
	return t
}

// TestLiteral_PerType: every field type reads its literal into the
// value the aggregator writes, and refuses a literal outside the type.
func TestLiteral_PerType(t *testing.T) {
	t.Parallel()

	dec, _, _ := encoding.ParseDecimal128("-0012.50")
	ok := []struct {
		typ, lit string
		want     aggconst.Constant
	}{
		{"u4", `15`, aggconst.Constant{Number: 15}},
		{"u8", `0`, aggconst.Constant{Number: 0}},
		{"u8", `2.0`, aggconst.Constant{Number: 2}},
		{"u16", `65535`, aggconst.Constant{Number: 65535}},
		{"u32", `4294967295`, aggconst.Constant{Number: 4294967295}},
		{"u64", `9007199254740992`, aggconst.Constant{Number: 1 << 53}},
		{"f32", `0.1`, aggconst.Constant{Number: float64(float32(0.1))}},
		{"f64", `-1e300`, aggconst.Constant{Number: -1e300}},
		{"packed_bool", `true`, aggconst.Constant{Number: 1}},
		{"packed_bool", `false`, aggconst.Constant{Number: 0}},
		{"date", `"1970-01-01"`, aggconst.Constant{Number: 0}},
		{"date", `"1969-12-31"`, aggconst.Constant{Number: -1}},
		{"date", `"2024-01-15"`, aggconst.Constant{Number: 19737}},
		{"datetime", `"2024-01-15T10:30:00Z"`, aggconst.Constant{Number: 1705314600}},
		{"datetime", `"2024-01-15T12:30:00+02:00"`, aggconst.Constant{Number: 1705314600}},
		{"decimal128", `"-0012.50"`, aggconst.Constant{Decimal: dec, Scale: 2, Precision: 4}},
		{"categorical_u16", `"north"`, aggconst.Constant{Label: "north"}},
		{"set_u8", `[]`, aggconst.Constant{Labels: []string{}}},
		{"set_u256", `["b","a"]`, aggconst.Constant{Labels: []string{"b", "a"}}},
		{"vec_f32", `[0.1, 2]`, aggconst.Constant{Vector: []float64{float64(float32(0.1)), 2}}},
		{"vec_f64", `[0.1, 2]`, aggconst.Constant{Vector: []float64{0.1, 2}}},
		{"f64", `null`, aggconst.Constant{Null: true}},
		{"set_u8", `null`, aggconst.Constant{Null: true}},
	}
	for _, c := range ok {
		got, r := aggconst.Literal(ft(c.typ), json.RawMessage(c.lit))
		if r != nil {
			t.Errorf("%s %s refused: %v", c.typ, c.lit, r)
			continue
		}
		c.want.Type = ft(c.typ)
		if c.want.Decimal.Mantissa() != nil && got.Decimal.Cmp(c.want.Decimal) == 0 {
			got.Decimal = c.want.Decimal
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s = %+v, want %+v", c.typ, c.lit, got, c.want)
		}
	}

	bad := []struct{ typ, lit string }{
		{"u4", `16`}, {"u8", `-1`}, {"u8", `1.5`}, {"u8", `"1"`}, {"u16", `65536`}, {"u32", `4294967296`},
		{"u64", `9007199254740993`}, {"u64", `1e300`}, {"f32", `3.5e38`}, {"f64", `"1"`}, {"f64", `true`},
		{"packed_bool", `1`}, {"packed_bool", `"true"`},
		{"date", `"2024-1-5"`}, {"date", `19737`}, {"date", `"2024-01-15T00:00:00Z"`},
		{"datetime", `"2024-01-15T10:30:00"`}, {"datetime", `"2024-01-15T10:30:00.5Z"`}, {"datetime", `1705314600`},
		{"decimal128", `12.5`}, {"decimal128", `"1e3"`}, {"decimal128", `"1,000"`},
		{"categorical_u8", `""`}, {"categorical_u8", `3`},
		{"set_u8", `["a","a"]`}, {"set_u8", `["a",""]`}, {"set_u8", `"a"`}, {"set_u8", `["a","b","c","d","e","f","g","h","i"]`},
		{"vec_f64", `[1]`}, {"vec_f64", `[1,"2"]`}, {"vec_f32", `[1,1e39]`}, {"vec_f64", `1`},
	}
	for _, c := range bad {
		_, r := aggconst.Literal(ft(c.typ), json.RawMessage(c.lit))
		if r == nil || r.Code != errors.PROCESSING_CONFIG || r.Details["reason"] != aggconst.ReasonValueInvalid {
			t.Errorf("%s %s: refusal %+v, want value_invalid", c.typ, c.lit, r)
		}
	}
}

// TestFromNumber_RangeAndRoundTrip: an expression's number is held to
// the type's range, and every accepted one renders to the literal that
// reads back to the same constant.
func TestFromNumber_RangeAndRoundTrip(t *testing.T) {
	t.Parallel()

	ok := []struct {
		typ string
		v   float64
		lit string
	}{
		{"u4", 15, `15`}, {"u64", 1 << 53, `9007199254740992`}, {"f32", 0.1, `0.10000000149011612`},
		{"f64", 0.7, `0.7`}, {"packed_bool", 1, `true`}, {"packed_bool", 0, `false`},
		{"date", -1, `"1969-12-31"`}, {"date", 19737, `"2024-01-15"`},
		{"datetime", 1705314600, `"2024-01-15T10:30:00Z"`},
	}
	for _, c := range ok {
		got, r := aggconst.FromNumber(ft(c.typ), c.v)
		if r != nil {
			t.Fatalf("%s %v refused: %v", c.typ, c.v, r)
		}
		if lit := string(got.LiteralJSON()); lit != c.lit {
			t.Errorf("%s %v renders %s, want %s", c.typ, c.v, lit, c.lit)
		}
		back, r := aggconst.Literal(ft(c.typ), got.LiteralJSON())
		if r != nil || !reflect.DeepEqual(back, got) {
			t.Errorf("%s %v: literal reads back %+v (%v), want %+v", c.typ, c.v, back, r, got)
		}
	}
	bad := []struct {
		typ string
		v   float64
	}{
		{"u4", 16}, {"u8", -1}, {"u64", 1<<53 + 2}, {"f32", 1e39}, {"packed_bool", 2},
		{"date", 3_000_000}, {"date", -800_000}, {"datetime", 1e12},
	}
	for _, c := range bad {
		if _, r := aggconst.FromNumber(ft(c.typ), c.v); r == nil || r.Details["reason"] != aggconst.ReasonExprOutOfRange {
			t.Errorf("%s %v: refusal %+v, want expr_out_of_range", c.typ, c.v, r)
		}
	}
	for _, typ := range []string{"decimal128", "categorical_u8", "set_u8", "vec_f64"} {
		if _, r := aggconst.FromNumber(ft(typ), 1); r == nil || r.Details["reason"] != aggconst.ReasonExprTypeUnsupported {
			t.Errorf("%s: refusal %+v, want expr_type_unsupported", typ, r)
		}
	}
}

// TestParse_Refusals: the params object's shape.
func TestParse_Refusals(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		``:                                     aggconst.ReasonParamsInvalid,
		`[1]`:                                  aggconst.ReasonParamsInvalid,
		`{"type":"f64","value":1,"k":2}`:       aggconst.ReasonParamsInvalid,
		`{"value":1}`:                          aggconst.ReasonTypeInvalid,
		`{"type":"i32","value":1}`:             aggconst.ReasonTypeInvalid,
		`{"type":3,"value":1}`:                 aggconst.ReasonTypeInvalid,
		`{"type":"f64","value":1,"expr":"1"}`:  aggconst.ReasonValueAndExpr,
		`{"type":"f64"}`:                       aggconst.ReasonValueMissing,
		`{"type":"f64","expr":""}`:             aggconst.ReasonExprInvalid,
		`{"type":"f64","expr":1}`:              aggconst.ReasonExprInvalid,
		`{"type":"vec_f64","expr":"1"}`:        aggconst.ReasonExprTypeUnsupported,
		`{"type":"categorical_u8","expr":"1"}`: aggconst.ReasonExprTypeUnsupported,
	}
	for raw, want := range cases {
		_, r := aggconst.Parse(json.RawMessage(raw))
		if r == nil || r.Code != errors.PROCESSING_CONFIG || r.Details["reason"] != want {
			t.Errorf("%s: refusal %+v, want %s", raw, r, want)
		}
	}
	// value null is present: it emits null, it is not "missing".
	if s, r := aggconst.Parse(json.RawMessage(`{"type":"u8","value":null}`)); r != nil || !s.HasValue {
		t.Errorf("value null: %+v %v", s, r)
	}
}

// TestCheckSlot: no field, vector or fields; a label.
func TestCheckSlot(t *testing.T) {
	t.Parallel()

	for _, a := range []*types.Aggregation{
		{Type: types.AGG_CONSTANT, Label: "c", Field: "x"},
		{Type: types.AGG_CONSTANT, Label: "c", Vector: "v"},
		{Type: types.AGG_CONSTANT, Label: "c", Fields: []string{"x", "y"}},
	} {
		if r := aggconst.CheckSlot(a); r == nil || r.Details["reason"] != aggconst.ReasonFieldNotAccepted {
			t.Errorf("%+v: %+v, want field_not_accepted", a, r)
		}
	}
	if r := aggconst.CheckSlot(&types.Aggregation{Type: types.AGG_CONSTANT}); r == nil || r.Details["reason"] != aggconst.ReasonLabelMissing {
		t.Errorf("no label: %+v", r)
	}
	if r := aggconst.CheckSlot(&types.Aggregation{Type: types.AGG_CONSTANT, Label: "c"}); r != nil {
		t.Errorf("valid slot refused: %v", r)
	}
}

func testEnv(t *testing.T) scalarslot.Env {
	t.Helper()
	params, cerr := exprenv.NormalizeParams(map[string]any{"k": 7, "share": 0.25})
	if cerr != nil {
		t.Fatal(cerr)
	}
	return scalarslot.Env{Params: params, Extensions: []string{"boost"}}
}

// TestEval_StaticAndDeferred: an expression over params resolves on a
// static judgement; one calling a request-bound function is left
// unresolved with its reads; a scalar-slot refusal is renamed expr_*.
func TestEval_StaticAndDeferred(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	c, un, ok, r := aggconst.Eval(json.RawMessage(`{"type":"u8","expr":"params.k * 2"}`), env, env.Declared(), false)
	if r != nil || un != nil || !ok || c.Number != 14 {
		t.Fatalf("params expression: %+v %+v %v %v", c, un, ok, r)
	}
	_, un, ok, r = aggconst.Eval(json.RawMessage(`{"type":"f64","expr":"boost(1)"}`), env, env.Declared(), false)
	if r != nil || ok || un == nil || un.Expression != "boost(1)" || !reflect.DeepEqual(un.Reads, []string{"boost"}) {
		t.Fatalf("bound function: %+v %v %v", un, ok, r)
	}
	for src, want := range map[string]string{
		"params.share":  aggconst.ReasonExprNotInteger,
		"x + 1":         aggconst.ReasonExprInvalid,
		`"a"`:           aggconst.ReasonExprNotNumeric,
		"log(0)":        aggconst.ReasonExprNotFinite,
		"params.k * 40": aggconst.ReasonExprOutOfRange,
	} {
		raw, _ := json.Marshal(map[string]string{"type": "u8", "expr": src})
		_, _, _, r := aggconst.Eval(raw, env, env.Declared(), false)
		if r == nil || r.Details["reason"] != want {
			t.Errorf("%s: %+v, want %s", src, r, want)
		}
	}
	raw := json.RawMessage(`{"type":"f64","expr":"params.nope"}`)
	if _, _, _, r := aggconst.Eval(raw, env, env.Declared(), false); r == nil || r.Code != errors.PULSE_PARAMS_UNDECLARED || r.Details["param"] != "expr" {
		t.Errorf("undeclared param: %+v", r)
	}
}

// TestResolveRequest_RewritesExpressionsOnly: the pass rewrites every
// AGG_CONSTANT expr — aggregations, crosstab cells, margin
// aggregations — into its literal on a copy; a request with none comes
// back as the same pointer, and the caller's slots never change.
func TestResolveRequest_RewritesExpressionsOnly(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	envFn := func() (scalarslot.Env, exprenv.Bound) { return env, env.Declared() }
	plain := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_CONSTANT, Label: "c", Params: json.RawMessage(`{"type":"u8","value":1}`)}}}
	if res := aggconst.ResolveRequest(plain, envFn, true); res.Request != plain || res.Refusals != nil || res.Unresolved != nil {
		t.Fatalf("literal-only request rewritten: %+v", res)
	}

	expr := json.RawMessage(`{"type":"date","expr":"params.k"}`)
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}, {Type: types.AGG_CONSTANT, Label: "c", Params: expr}},
		Crosstab: &types.CrosstabSpec{
			Cells:              []*types.Aggregation{{Type: types.AGG_CONSTANT, Label: "m", Params: json.RawMessage(`{"type":"f64","expr":"params.share"}`)}},
			MarginAggregations: []*types.Aggregation{{Type: types.AGG_CONSTANT, Label: "g", Params: json.RawMessage(`{"type":"f64","expr":"boost(1)"}`)}},
		},
	}
	res := aggconst.ResolveRequest(req, envFn, false)
	if res.Request == req || res.Request.Aggregations[0] != req.Aggregations[0] {
		t.Fatal("the pass must copy the request and keep untouched slots")
	}
	if got := string(res.Request.Aggregations[1].Params); got != `{"type":"date","value":"1970-01-08"}` {
		t.Errorf("rewritten aggregation params %s", got)
	}
	if got := string(res.Request.Crosstab.Cells[0].Params); got != `{"type":"f64","value":0.25}` {
		t.Errorf("rewritten cell params %s", got)
	}
	if string(req.Aggregations[1].Params) != string(expr) {
		t.Error("the caller's slot was mutated")
	}
	want := []aggconst.Unresolved{{Path: "crosstab.margin_aggregations[0].params.expr", Expression: "boost(1)", Reads: []string{"boost"}}}
	if !reflect.DeepEqual(res.Unresolved, want) {
		t.Errorf("unresolved %+v, want %+v", res.Unresolved, want)
	}

	bad := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_CONSTANT, Label: "c", Params: json.RawMessage(`{"type":"u4","expr":"params.k * 3"}`)}}}
	res = aggconst.ResolveRequest(bad, envFn, true)
	if len(res.Refusals) != 1 || res.Refusals[0].Path != "aggregations[0]" || res.Refusals[0].Refusal.Details["reason"] != aggconst.ReasonExprOutOfRange {
		t.Errorf("refusals %+v", res.Refusals)
	}
}

// TestRead_RefusesAnUnresolvedExpression: the factory's reading never
// evaluates — an expression reaching it is a skipped pass.
func TestRead_RefusesAnUnresolvedExpression(t *testing.T) {
	t.Parallel()

	if _, r := aggconst.Read(json.RawMessage(`{"type":"f64","expr":"1"}`)); r == nil || r.Code != errors.PROCESSING_INTERNAL {
		t.Errorf("expr at the factory: %+v", r)
	}
	if c, r := aggconst.Read(json.RawMessage(`{"type":"f64","value":2}`)); r != nil || c.Number != 2 {
		t.Errorf("literal: %+v %v", c, r)
	}
}

// TestNonNumeric_ByType: only the numeric family writes one number.
func TestNonNumeric_ByType(t *testing.T) {
	t.Parallel()

	for b := 0; b < 256; b++ {
		f := encoding.FieldType(b)
		if !f.IsKnown() {
			continue
		}
		a := &types.Aggregation{Type: types.AGG_CONSTANT, Params: json.RawMessage(`{"type":"` + f.String() + `"}`)}
		want := f.IsDecimal() || f.HasDictionary() || f.IsVector()
		if got := aggconst.NonNumeric(a); got != want || got == aggconst.Numeric(f) {
			t.Errorf("%s: NonNumeric %v, want %v", f, got, want)
		}
	}
	if aggconst.NonNumeric(&types.Aggregation{Type: types.AGG_SUM, Params: json.RawMessage(`{"type":"set_u8"}`)}) {
		t.Error("another aggregator is never a non-numeric constant")
	}
	_ = math.NaN
}
