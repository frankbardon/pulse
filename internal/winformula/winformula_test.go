package winformula

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// TestReads_FoldedParamsAreNoColumn: the names an expression reads, in
// first-use order, `$env.name` members included and the `params`
// identifier dropped when every use of it is a parameter read.
func TestReads_FoldedParamsAreNoColumn(t *testing.T) {
	t.Parallel()

	for src, want := range map[string][]string{
		"a / b + a":            {"a", "b"},
		"a * params.k":         {"a"},
		"params + a":           {"params", "a"},
		`$env.c - abs(d)`:      {"c", "d"},
		"let z = a; z * 2 + b": {"a", "b"},
	} {
		got, ok := Reads(src)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("Reads(%q) = %v, %v; want %v", src, got, ok, want)
		}
	}
	if _, ok := Reads("a +"); ok {
		t.Error("a syntax error parsed")
	}
}

// TestCompile_ResultAndEval: a number or untyped result compiles, any
// other is PULSE_FORMULA_COMPILE_ERROR naming the site; a row's result
// converts to float64, nil and non-finite read null, any other value is
// PROCESSING_RUNTIME.
func TestCompile_ResultAndEval(t *testing.T) {
	t.Parallel()

	params, _ := exprenv.NormalizeParams(map[string]any{"k": json.Number("2")})
	bound := exprenv.Bound{Params: params}
	for src, ok := range map[string]bool{
		"a / b": true, "a": true, "1 + 2": true, "a ** 2": true, "a * params.k": true,
		"a > b": false, `"x"`: false, "[a]": false, "{\"k\": a}": false,
	} {
		_, ce := Compile(src, nil, bound, false)
		if ok != (ce == nil) {
			t.Errorf("Compile(%q) = %v, want ok %v", src, ce, ok)
		}
		if ce != nil && (ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR || ce.Details["expression"] != src) {
			t.Errorf("Compile(%q) = %s %v", src, ce.Code, ce.Details)
		}
	}
	if _, ce := Compile("a * params.k", nil, bound, true); ce == nil || ce.Code != errors.PULSE_PARAMS_FIELD_COLLISION {
		t.Errorf("params column: %v, want PULSE_PARAMS_FIELD_COLLISION", ce)
	}

	p, ce := Compile("a", nil, bound, false)
	if ce != nil {
		t.Fatal(ce)
	}
	for _, tc := range []struct {
		in   any
		want float64
		ok   bool
	}{
		{int64(3), 3, true}, {uint8(7), 7, true}, {float32(0.5), 0.5, true}, {nil, 0, false},
	} {
		v, ok, err := p.Eval(map[string]any{"a": tc.in})
		if err != nil || ok != tc.ok || v != tc.want {
			t.Errorf("Eval(%v) = %v, %v, %v", tc.in, v, ok, err)
		}
	}
	inf, _ := Compile("a / b", nil, bound, false)
	if _, ok, err := inf.Eval(map[string]any{"a": 1.0, "b": 0.0}); ok || err != nil {
		t.Errorf("1/0: ok %v err %v, want null", ok, err)
	}
	_, _, err := p.Eval(map[string]any{"a": true})
	if ce, isCoded := err.(*errors.CodedError); !isCoded || ce.Code != errors.PROCESSING_RUNTIME || ce.Details["returned_type"] != "bool" {
		t.Errorf("bool row result: %v, want PROCESSING_RUNTIME", err)
	}
}

// typedSchema carries one field of each row-cell kind Columns types.
func typedSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, l := range []string{"a", "b"} {
		if _, err := dict.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "n", Type: encoding.FieldTypeU8},
		{Name: "id", Type: encoding.FieldTypeU64},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Scale: 2},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
		{Name: "x", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
	}}
}

// TestColumns_RecordRowsTypedOutputRowsNot: over the record rows every
// schema field is declared as its row cell (a categorical a string, a
// set its labels, a decimal and a u64 their float64 value; a duplicate
// keeps its first occurrence); over the output rows, or without a
// schema, nothing is.
func TestColumns_RecordRowsTypedOutputRowsNot(t *testing.T) {
	t.Parallel()

	s := typedSchema(t)
	got := Columns(s, true)
	want := map[string]reflect.Type{
		"x": reflect.TypeOf(0.0), "n": reflect.TypeOf(0.0), "id": reflect.TypeOf(0.0), "amt": reflect.TypeOf(0.0),
		"cat": reflect.TypeOf(""), "tags": reflect.TypeOf([]string(nil)),
	}
	if len(got) != len(want) {
		t.Fatalf("Columns = %v, want %d names", got, len(want))
	}
	for name, typ := range want {
		if d, ok := got[name]; !ok || d.Nature().Type != typ {
			t.Errorf("%s declared %v, want %v", name, got[name], typ)
		}
	}
	if Columns(s, false) != nil || Columns(nil, true) != nil {
		t.Error("output rows or no schema declared columns")
	}
	if _, ce := Compile("cat * 2", got, exprenv.Bound{}, false); ce == nil || ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
		t.Errorf("cat * 2 typed: %v, want PULSE_FORMULA_COMPILE_ERROR", ce)
	}
	if _, ce := Compile("cat * 2", nil, exprenv.Bound{}, false); ce != nil {
		t.Errorf("cat * 2 untyped: %v, want a compile", ce)
	}
}

// TestCompile_TypedEvaluatesBitIdentically: a numeric expression typed
// over the record rows evaluates to the same bits as the untyped compile
// on every row — the typing refuses, it never moves a figure.
func TestCompile_TypedEvaluatesBitIdentically(t *testing.T) {
	t.Parallel()

	cols := Columns(typedSchema(t), true)
	for _, src := range []string{
		"x * n + id / 3", "x % 2.5 + n % 4", "x ** 1.5 - amt", "abs(x - n) / (id + 1)", "max(x, n) * min(amt, 3)",
		`cat == "a" ? x : -x`, `"b" in tags ? n * 0.1 : amt`, "len(cat) * x", "x == n ? 1 : 0", "int(x) + floor(amt)",
	} {
		typed, ce := Compile(src, cols, exprenv.Bound{}, false)
		if ce != nil {
			t.Fatalf("%q typed: %v", src, ce)
		}
		untyped, ce := Compile(src, nil, exprenv.Bound{}, false)
		if ce != nil {
			t.Fatalf("%q untyped: %v", src, ce)
		}
		for i := range 40 {
			x := float64(i)*0.37 - 4
			row := map[string]any{
				"x": x, "n": float64(i % 7), "id": float64(i*1_000_003 + 1), "amt": float64(i%9) * 0.25,
				"cat": []string{"a", "b"}[i%2], "tags": []string{"a", "b"}[:1+i%2],
			}
			a, aok, aerr := typed.Eval(row)
			b, bok, berr := untyped.Eval(row)
			if aok != bok || (aerr == nil) != (berr == nil) || math.Float64bits(a) != math.Float64bits(b) {
				t.Errorf("%q row %d: typed %v %v %v, untyped %v %v %v", src, i, a, aok, aerr, b, bok, berr)
			}
		}
	}
}
