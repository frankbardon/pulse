package winformula

import (
	"encoding/json"
	"reflect"
	"testing"

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
		_, ce := Compile(src, bound, false)
		if ok != (ce == nil) {
			t.Errorf("Compile(%q) = %v, want ok %v", src, ce, ok)
		}
		if ce != nil && (ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR || ce.Details["expression"] != src) {
			t.Errorf("Compile(%q) = %s %v", src, ce.Code, ce.Details)
		}
	}
	if _, ce := Compile("a * params.k", bound, true); ce == nil || ce.Code != errors.PULSE_PARAMS_FIELD_COLLISION {
		t.Errorf("params column: %v, want PULSE_PARAMS_FIELD_COLLISION", ce)
	}

	p, ce := Compile("a", bound, false)
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
	inf, _ := Compile("a / b", bound, false)
	if _, ok, err := inf.Eval(map[string]any{"a": 1.0, "b": 0.0}); ok || err != nil {
		t.Errorf("1/0: ok %v err %v, want null", ok, err)
	}
	_, _, err := p.Eval(map[string]any{"a": true})
	if ce, isCoded := err.(*errors.CodedError); !isCoded || ce.Code != errors.PROCESSING_RUNTIME || ce.Details["returned_type"] != "bool" {
		t.Errorf("bool row result: %v, want PROCESSING_RUNTIME", err)
	}
}
