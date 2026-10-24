package exprenv

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/errors"
)

// TestParams_NormalizeTypes pins how each value binds: integers exact
// (int64, then uint64), every other number float64, lists by the one
// slice type all their elements fit, a Go float as its JSON text.
func TestParams_NormalizeTypes(t *testing.T) {
	raw := map[string]any{
		"i":      json.Number("3"),
		"big":    json.Number("9007199254740993"),
		"u":      json.Number("18446744073709551615"),
		"f":      json.Number("3.0"),
		"e":      json.Number("1e3"),
		"s":      "west",
		"b":      true,
		"ints":   []any{json.Number("3"), json.Number("4")},
		"uints":  []any{json.Number("1"), json.Number("18446744073709551615")},
		"mixn":   []any{json.Number("1"), json.Number("2.5")},
		"strs":   []any{"a", "b"},
		"bools":  []any{true, false},
		"empty":  []any{},
		"goint":  7,
		"goflt":  2.0,
		"gohalf": 2.5,
		"goslc":  []int{1, 2},
	}
	got, cerr := NormalizeParams(raw)
	if cerr != nil {
		t.Fatalf("NormalizeParams: %v", cerr)
	}
	want := Params{
		"i": int64(3), "big": int64(9007199254740993), "u": uint64(18446744073709551615),
		"f": 3.0, "e": 1000.0, "s": "west", "b": true,
		"ints": []int64{3, 4}, "uints": []uint64{1, 18446744073709551615}, "mixn": []float64{1, 2.5},
		"strs": []string{"a", "b"}, "bools": []bool{true, false}, "empty": []any{},
		"goint": int64(7), "goflt": int64(2), "gohalf": 2.5, "goslc": []int64{1, 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeParams:\n got %#v\nwant %#v", got, want)
	}
	if p, err := NormalizeParams(nil); p != nil || err != nil {
		t.Fatalf("nil params: %v %v", p, err)
	}
}

// TestParams_NormalizeRefusals: each invalid shape is PULSE_PARAMS_INVALID
// with its reason.
func TestParams_NormalizeRefusals(t *testing.T) {
	cases := []struct {
		name   string
		raw    map[string]any
		reason string
	}{
		{"null", map[string]any{"x": nil}, ParamsReasonNull},
		{"null element", map[string]any{"x": []any{json.Number("1"), nil}}, ParamsReasonNull},
		{"dotted name", map[string]any{"a.b": json.Number("1")}, ParamsReasonName},
		{"leading digit", map[string]any{"1a": json.Number("1")}, ParamsReasonName},
		{"empty name", map[string]any{"": json.Number("1")}, ParamsReasonName},
		{"mixed list", map[string]any{"x": []any{json.Number("1"), "a"}}, ParamsReasonMixedList},
		{"bool and string", map[string]any{"x": []any{true, "a"}}, ParamsReasonMixedList},
		{"object", map[string]any{"x": map[string]any{"a": json.Number("1")}}, ParamsReasonUnsupported},
		{"nested list", map[string]any{"x": []any{[]any{json.Number("1")}}}, ParamsReasonUnsupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, cerr := NormalizeParams(c.raw)
			if cerr == nil || cerr.Code != errors.PULSE_PARAMS_INVALID || cerr.Details["reason"] != c.reason {
				t.Fatalf("got %v (details %v), want PULSE_PARAMS_INVALID reason %s", cerr, detailsOf(cerr), c.reason)
			}
		})
	}
}

func detailsOf(c *errors.CodedError) map[string]any {
	if c == nil {
		return nil
	}
	return c.Details
}

// TestParams_ReadAndRefuse pins the static rule: member reads are
// parameter reads, a bare use is not, a let named params shadows, an
// undeclared name and a column collision are refused.
func TestParams_ReadAndRefuse(t *testing.T) {
	uses, ok := ReadParams(`x * params.k + params["j"] > params.k`)
	if !ok || !reflect.DeepEqual(uses.Names, []string{"k", "j"}) || uses.Bare {
		t.Fatalf("ReadParams = %+v %v", uses, ok)
	}
	if uses, _ := ReadParams(`params + 1`); len(uses.Names) != 0 || !uses.Bare {
		t.Fatalf("bare: %+v", uses)
	}
	if uses, _ := ReadParams(`let params = 1; params.x`); len(uses.Names) != 0 {
		t.Fatalf("let shadow: %+v", uses)
	}
	if FoldsParams(`params + params.k`) || !FoldsParams(`params.k`) {
		t.Fatal("FoldsParams")
	}
	p := Params{"k": int64(2)}
	if ce := ParamsRefusal(`x * params.k`, p, false); ce != nil {
		t.Fatalf("declared: %v", ce)
	}
	if ce := ParamsRefusal(`x * params.q`, p, false); ce == nil || ce.Code != errors.PULSE_PARAMS_UNDECLARED || ce.Details["param"] != "q" {
		t.Fatalf("undeclared: %v", ce)
	}
	if ce := ParamsRefusal(`x * params.k`, p, true); ce == nil || ce.Code != errors.PULSE_PARAMS_FIELD_COLLISION {
		t.Fatalf("collision: %v", ce)
	}
	if ce := ParamsRefusal(`params > 1`, p, true); ce != nil {
		t.Fatalf("a bare read of the params column is no collision: %v", ce)
	}
}

// TestParams_FoldCompiles: a declared param folds into a typed constant,
// so it type-checks and evaluates against float64 fields, an `in` list
// and an exact large integer — and a different value compiles a
// different program.
func TestParams_FoldCompiles(t *testing.T) {
	p, cerr := NormalizeParams(map[string]any{
		"from_wave": json.Number("3"),
		"k":         json.Number("2.5"),
		"waves":     []any{json.Number("3"), json.Number("5")},
		"big":       json.Number("9007199254740993"),
		"region":    "west",
	})
	if cerr != nil {
		t.Fatal(cerr)
	}
	decl := exprtypes.Map{"wave": exprtypes.TypeOf(float64(0)), "x": exprtypes.TypeOf(float64(0)), "region": exprtypes.TypeOf("")}
	run := func(p Params, src string, env map[string]any) any {
		t.Helper()
		opts := append([]expr.Option{expr.Env(decl)}, Bound{Params: p}.Options()...)
		prog, err := expr.Compile(src, opts...)
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		out, err := expr.Run(prog, env)
		if err != nil {
			t.Fatalf("run %q: %v", src, err)
		}
		return out
	}
	env := map[string]any{"wave": 3.0, "x": 4.0, "region": "west"}
	for src, want := range map[string]any{
		`wave == params.from_wave`:         true,
		`x * params.k`:                     10.0,
		`wave in params.waves`:             true,
		`4.0 in params.waves`:              false,
		`params.big == 9007199254740993`:   true,
		`params.big == 9007199254740992`:   false,
		`region == params.region`:          true,
		`params["from_wave"] + 1`:          4,
		`params.from_wave % 2`:             1,
		`x % params.from_wave`:             1.0,
		`wave >= params.from_wave ? 1 : 0`: 1,
	} {
		if got := run(p, src, env); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", src, got, want)
		}
	}
	// A changed value compiles a changed program: no stale constant.
	p2 := Params{"from_wave": int64(4)}
	if got := run(p2, `wave == params.from_wave`, env); got != false {
		t.Fatalf("changed value: %v", got)
	}
	// Without the fold the read is an unknown name (the patcher is what
	// binds it).
	if _, err := expr.Compile(`wave == params.from_wave`, append([]expr.Option{expr.Env(decl)}, Bound{}.Options()...)...); err == nil {
		t.Fatal("an undeclared params compiled without the fold")
	}
}
