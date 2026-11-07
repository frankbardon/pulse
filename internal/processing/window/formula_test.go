package window

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

func formulaWindow(label, expr string) *types.Window {
	return &types.Window{Type: types.WIN_FORMULA, Label: label, Params: json.RawMessage(`{"expression":` + jsonQuote(expr) + `}`)}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestFormula_RowLocalOverEarlierWindows: WIN_FORMULA reads the row's
// own cells — integers, floats and a decimal as its float value — and
// an earlier window's output; a null, absent or NaN input reads null,
// and a nil or non-finite result reads null.
func TestFormula_RowLocalOverEarlierWindows(t *testing.T) {
	t.Parallel()

	dec := ScaledDecimal{Value: encoding.NewDecimal128FromInt(250), Scale: 2} // 2.50
	rows := []map[string]any{
		{"n": int64(4), "x": 10.0, "d": dec},
		{"n": int64(5), "x": 30.0, "d": dec},
		{"n": int64(2), "x": nil, "d": dec},
		{"n": int64(1), "x": math.NaN(), "d": dec},
		{"n": int64(0), "x": 12.0, "d": dec},
		{"x": 8.0, "d": dec},
	}
	err := Apply(context.Background(), rows, []*types.Window{
		{Type: types.WIN_SHARE, Field: "x", Label: "s"},
		formulaWindow("per", "x / n"),
		formulaWindow("sd", "s * d"),
		formulaWindow("guard", "x ?? -1"),
		formulaWindow("none", "n > 1 ? x : nil"),
	})
	if err != nil {
		t.Fatal(err)
	}
	total := 10.0 + 30 + 12 + 8
	wantCells(t, rows, "per", []any{10.0 / 4, 30.0 / 5, nil, nil, nil, nil})
	wantCells(t, rows, "sd", []any{(10.0 / total) * 2.5, (30.0 / total) * 2.5, nil, nil, (12.0 / total) * 2.5, (8.0 / total) * 2.5})
	wantCells(t, rows, "guard", []any{10.0, 30.0, nil, nil, 12.0, 8.0})
	wantCells(t, rows, "none", []any{10.0, 30.0, nil, nil, nil, nil})
}

// TestFormula_ParamsAndFactoryRefusals: params fold through
// WindowOptions.ExprBound; without them the read is
// PULSE_PARAMS_UNDECLARED; a non-number result type, ref() and bad
// syntax are PULSE_FORMULA_COMPILE_ERROR at the factory — the compile
// predict's walk runs.
func TestFormula_ParamsAndFactoryRefusals(t *testing.T) {
	t.Parallel()

	params, ce := exprenv.NormalizeParams(map[string]any{"k": json.Number("3")})
	if ce != nil {
		t.Fatal(ce)
	}
	rows := []map[string]any{{"x": 2.0}}
	err := ApplyWith(context.Background(), rows, []*types.Window{formulaWindow("f", "x * params.k")}, nil, nil, nil,
		WindowOptions{ExprBound: exprenv.Bound{Params: params}})
	if err != nil {
		t.Fatal(err)
	}
	wantCells(t, rows, "f", []any{6.0})

	factory, _ := Lookup(types.WIN_FORMULA)
	for src, code := range map[string]errors.Code{
		"x * params.k":    errors.PULSE_PARAMS_UNDECLARED,
		"x > 1":           errors.PULSE_FORMULA_COMPILE_ERROR,
		`"label"`:         errors.PULSE_FORMULA_COMPILE_ERROR,
		`ref("t", "k")`:   errors.PULSE_FORMULA_COMPILE_ERROR,
		"x +":             errors.PULSE_FORMULA_COMPILE_ERROR,
		`coef("m", "x")`:  errors.PULSE_FORMULA_COMPILE_ERROR,
		"total(x)":        errors.PULSE_FORMULA_COMPILE_ERROR,
		"[x, x]":          errors.PULSE_FORMULA_COMPILE_ERROR,
		"{\"a\": x}":      errors.PULSE_FORMULA_COMPILE_ERROR,
		"x / 2 > 1 ? 1:0": "",
	} {
		_, err := factory(formulaWindow("f", src), WindowOptions{})
		if code == "" {
			if err != nil {
				t.Errorf("%q: refused %v", src, err)
			}
			continue
		}
		var got *errors.CodedError
		if !stderrors.As(err, &got) || got.Code != code {
			t.Errorf("%q: err = %v, want %s", src, err, code)
		}
	}
}

// TestFormula_RowResultNotANumber: a result the compile cannot type (a
// bare column) that is no number on a row fails the run
// PROCESSING_RUNTIME, naming the type.
func TestFormula_RowResultNotANumber(t *testing.T) {
	t.Parallel()

	rows := []map[string]any{{"g": "north"}}
	err := Apply(context.Background(), rows, []*types.Window{formulaWindow("f", "g")})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_RUNTIME || ce.Details["returned_type"] != "string" {
		t.Fatalf("err = %v, want PROCESSING_RUNTIME returned_type string", err)
	}
	rows = []map[string]any{{"g": "north"}}
	if err := Apply(context.Background(), rows, []*types.Window{formulaWindow("f", `g == "north" ? 1 : 0`)}); err != nil {
		t.Fatalf("a label compared, not computed with, failed: %v", err)
	}
	wantCells(t, rows, "f", []any{1.0})
}
