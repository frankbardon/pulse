package processing

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// `%` over float64-bound Pulse fields (expr_mod.go). Every numeric field
// binds as float64 and expr-lang's `%` is integer-only, so without the
// patcher `x % 2` never compiles on a field.

func exprModFormula(t *testing.T, s *encoding.Schema, src string, r *Record) (float64, error) {
	t.Helper()
	comp, err := newFormulaAttribute(&types.Attribute{Type: types.ATTR_FORMULA, Expression: src, Label: "v"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := bindAttribute(comp, nil); err != nil {
		return 0, err
	}
	return comp.(*formulaAttribute).Row(r, "")
}

func TestExprMod_FormulaSemantics(t *testing.T) {
	s := exprParitySchema()
	full := NewRecordWithWide(s, map[string]float64{"id": 7, "x": 6.5, "y": 4, "cat": 1, "d": 18262}, nil, nil)
	null := NewRecordWithWide(s, map[string]float64{"id": 7, "y": 4, "d": 18262}, map[string]bool{"x": true, "cat": true}, nil)
	for _, tc := range []struct {
		src  string
		r    *Record
		want float64
	}{
		{"x % 4", full, 2.5},        // float field, int literal
		{"x % 2.5", full, 1.5},      // float field, float literal
		{"-x % 4", full, -2.5},      // sign of the dividend (math.Mod)
		{"x % -4", full, 2.5},       //
		{"id % 3", full, 1},         // u32 field (float64-bound)
		{"id % y", full, 3},         // field % field
		{"(x % 4) % 1", full, 0.5},  // nested rewrite
		{"7 % 2", full, 1},          // int % int stays expr-lang's
		{"(x ?? 0) % 4", full, 2.5}, // any-typed operand, full row
		{"(x ?? 9) % 4", null, 1},   // coalesced null, untyped twin
		{"x == nil ? -1 : x % 4", null, -1},
		{"id % 3 + (cat == nil ? 10 : 0)", null, 11}, // untyped twin, present field
	} {
		got, err := exprModFormula(t, s, tc.src, tc.r)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %v, %v; want %v", tc.src, got, err, tc.want)
		}
	}

	// Zero divisor on a field: NaN, no error — as `/` yields ±Inf.
	if got, err := exprModFormula(t, s, "x % 0", full); err != nil || !math.IsNaN(got) {
		t.Errorf("x %% 0: got %v, %v; want NaN, nil", got, err)
	}
	if got, err := exprModFormula(t, s, "x / 0", full); err != nil || !math.IsInf(got, 1) {
		t.Errorf("x / 0: got %v, %v; want +Inf, nil (the rule %% by zero mirrors)", got, err)
	}
	// Integer modulo by zero keeps expr-lang's evaluation error.
	if _, err := exprModFormula(t, s, "7 % (len('') )", full); !errors.HasCode(err, errors.PROCESSING_RUNTIME) {
		t.Errorf("int %% 0: err %v, want PROCESSING_RUNTIME", err)
	}
	// Unguarded `%` on a null raises, like every other arithmetic.
	if _, err := exprModFormula(t, s, "x % 2", null); !errors.HasCode(err, errors.PROCESSING_RUNTIME) {
		t.Errorf("null %% 2: err %v, want PROCESSING_RUNTIME", err)
	}
}

func TestExprMod_NonNumericStillFailsAtCompile(t *testing.T) {
	s := exprParitySchema()
	for _, src := range []string{"cat % 2", "amt % 2", "x % 'a'"} {
		_, err := newExprProgram(src, "filter", s, (*ExtensionRegistry)(nil).ExprOptions())
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || !strings.HasPrefix(ce.Message, "compiling filter expression") {
			t.Errorf("%q: err %v, want a build-time compile error", src, err)
		}
	}
}

func TestExprMod_FilterTypedAndUntyped(t *testing.T) {
	s := exprParitySchema()
	recs := []*Record{
		NewRecordWithWide(s, map[string]float64{"id": 4, "x": 6.5, "y": 1, "cat": 1, "d": 18262}, nil, nil),
		NewRecordWithWide(s, map[string]float64{"id": 5, "x": 6.5, "y": 1, "d": 18262}, map[string]bool{"cat": true}, nil),
		NewRecordWithWide(s, map[string]float64{"id": 6, "y": 1, "d": 18262}, map[string]bool{"x": true, "cat": true}, nil),
	}
	for _, tc := range []struct {
		src  string
		want []bool
	}{
		{"id % 2 == 0", []bool{true, false, true}},
		{"x % 4 == 2.5", []bool{true, true, false}},               // null x: unknown, dropped
		{"x % 4 == 2.5 || cat == nil", []bool{true, true, false}}, // row 1 runs the untyped twin
		{"(x ?? 1) % 2 == 1", []bool{false, false, true}},         //
		{"x % 0 == x % 0", []bool{false, false, false}},           // NaN never equals
	} {
		fns, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: tc.src}}, s, nil)
		if err != nil {
			t.Fatalf("%q: %v", tc.src, err)
		}
		for i, want := range tc.want {
			got, err := fns[0](recs[i])
			if err != nil || got != want {
				t.Errorf("%q row %d: got %v, %v; want %v", tc.src, i, got, err, want)
			}
		}
	}
}

func TestExprMod_OverlayFormula(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindFormula}
	env := map[string]any{"value": 7.5, "total": 2.0}
	for _, exts := range []*ExtensionRegistry{nil, {}} {
		prog, err := compileFormulaProgramWithExtensions(spec, "value % total", env, exts)
		if err != nil {
			t.Fatalf("exts=%v: %v", exts != nil, err)
		}
		out, err := expr.Run(prog.program, env)
		if err != nil || out != 1.5 {
			t.Errorf("exts=%v: got %v, %v; want 1.5", exts != nil, out, err)
		}
	}
}

func TestExprMod_PureForPrecompute(t *testing.T) {
	got, ok := exprPureInputs("x % 2 == 0 && id % y > 1", nil)
	if !ok || strings.Join(got, ",") != "x,id,y" {
		t.Fatalf("exprPureInputs = %v, %v; want [x id y], true", got, ok)
	}
	if !exprPureSetBuiltins[exprModFuncName] {
		t.Fatalf("%s must be pure", exprModFuncName)
	}
}
