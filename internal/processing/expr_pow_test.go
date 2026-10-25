package processing

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// `**` / `^` on detmath (internal/exprenv pow.go): the operator gives
// pow()'s bits at every engine site and keeps expr-lang's null and
// compile semantics.

func TestExprPow_FormulaBitsAndNull(t *testing.T) {
	s := exprParitySchema()
	null := NewRecordWithWide(s, map[string]float64{"id": 7, "y": 4, "d": 18262}, map[string]bool{"x": true, "cat": true}, nil)
	for i := range 64 {
		x := 0.5 + float64(i)*0.173
		r := NewRecordWithWide(s, map[string]float64{"id": float64(i), "x": x, "y": 3, "cat": 1, "d": 18262}, nil, nil)
		for _, src := range []string{"x ** 1.37", "x ^ 1.37", "(x ?? 0) ** 1.37", "x ** 1.37 + (cat == nil ? 1 : 0)"} {
			got, err := exprModFormula(t, s, src, r)
			if err != nil {
				t.Fatalf("%q: %v", src, err)
			}
			if want := detmath.Pow(x, 1.37); math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("%q at x=%v: %v, want detmath.Pow %v", src, x, got, want)
			}
		}
	}
	// int ** int: float64, exact.
	if got, err := exprModFormula(t, s, "id ** 2 + 2 ** 3", null); err != nil || got != 57 {
		t.Errorf("id ** 2 + 2 ** 3 = %v, %v; want 57", got, err)
	}
	// An unguarded `**` on a null raises expr-lang's own conversion
	// error, as before the rewrite (pow() refuses with its own text).
	_, err := exprModFormula(t, s, "x ** 2", null)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_RUNTIME || ce.Cause == nil ||
		!strings.HasPrefix(ce.Cause.Error(), "invalid operation: float(<nil>) (1:3)") {
		t.Errorf("null ** 2: err %v, want PROCESSING_RUNTIME invalid operation: float(<nil>)", err)
	}
	if got, err := exprModFormula(t, s, "(x ?? 3) ** 2", null); err != nil || got != 9 {
		t.Errorf("(nil ?? 3) ** 2 = %v, %v; want 9", got, err)
	}
}

func TestExprPow_FilterUnknownOnNull(t *testing.T) {
	s := exprParitySchema()
	recs := []*Record{
		NewRecordWithWide(s, map[string]float64{"id": 4, "x": 3, "y": 1, "cat": 1, "d": 18262}, nil, nil),
		NewRecordWithWide(s, map[string]float64{"id": 5, "x": 3, "y": 1, "d": 18262}, map[string]bool{"cat": true}, nil),
		NewRecordWithWide(s, map[string]float64{"id": 6, "y": 1, "d": 18262}, map[string]bool{"x": true, "cat": true}, nil),
	}
	for _, tc := range []struct {
		src  string
		want []bool
	}{
		{"x ** 2 == 9", []bool{true, true, false}},             // null x: unknown, dropped
		{"x ^ 2 > 8 || cat == nil", []bool{true, true, false}}, // row 1 runs the untyped twin
		{"(x ?? 2) ** 2 == 4", []bool{false, false, true}},     //
		{"id ** 2 == 16 || id ** 2 == 36", []bool{true, false, true}},
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
	// A non-bool `**` filter is still refused at build (static type
	// float64 kept by the rewrite).
	_, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x ** 2"}}, s, nil)
	if !errors.HasCode(err, errors.PULSE_FORMULA_COMPILE_ERROR) || !strings.Contains(err.Error(), "expected bool, but got float64") {
		t.Errorf("x ** 2 filter: err %v, want PULSE_FORMULA_COMPILE_ERROR expected bool", err)
	}
}

func TestExprPow_NonNumericStillFailsAtCompile(t *testing.T) {
	s := exprParitySchema()
	for _, src := range []string{"cat ** 2", "amt ** 2", "x ** 'a'", "(amt ?? 0) ^ 2"} {
		_, err := newExprProgram(src, "filter", s, nil, nil, (*ExtensionRegistry)(nil).ExprOptions())
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || !strings.HasPrefix(ce.Message, "compiling filter expression") ||
			!strings.Contains(ce.Message, "mismatched types") {
			t.Errorf("%q: err %v, want a build-time mismatched-types compile error", src, err)
		}
	}
}

func TestExprPow_OverlayFormula(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindFormula}
	proto := map[string]any{"value": 0.0, "total": 0.0}
	for _, exts := range []*ExtensionRegistry{nil, {}} {
		for _, src := range []string{"value ** total", "value ^ total"} {
			prog, err := compileFormulaProgramWithExtensions(spec, src, proto, exts)
			if err != nil {
				t.Fatalf("exts=%v %q: %v", exts != nil, src, err)
			}
			for i := range 64 {
				env := map[string]any{"value": 0.5 + float64(i)*0.173, "total": 1.37}
				want := detmath.Pow(env["value"].(float64), 1.37)
				out, err := expr.Run(prog.program, env)
				if f, ok := out.(float64); err != nil || !ok || math.Float64bits(f) != math.Float64bits(want) {
					t.Fatalf("exts=%v %q at %v: got %v, %v; want %v", exts != nil, src, env["value"], out, err, want)
				}
			}
		}
	}
}

func TestExprPow_PureForPrecompute(t *testing.T) {
	got, ok := exprPureInputs("x ** 2 > 1 && id ^ y > 1", nil)
	if !ok || strings.Join(got, ",") != "x,id,y" {
		t.Fatalf("exprPureInputs = %v, %v; want [x id y], true", got, ok)
	}
	for _, name := range []string{exprenv.PowFuncName, exprenv.PowCaretFuncName} {
		if !exprPureSetBuiltins[name] {
			t.Fatalf("%s must be pure", name)
		}
	}
}
