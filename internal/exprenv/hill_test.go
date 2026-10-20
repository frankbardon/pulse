package exprenv

import (
	"math"
	"testing"

	"github.com/expr-lang/expr"
)

// TestHill_Values: hill(x, k, s) = x^s / (x^s + k^s) at known points,
// a null argument answers nil, an out-of-domain one NaN (null on the
// wire).
func TestHill_Values(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		name    string
		args    []any
		want    any // float64, nil, or NaN
		wantErr bool
	}{
		{"half saturation", []any{3.0, 3.0, 2.0}, 0.5, false},
		{"closed form", []any{2.0, 4.0, 3.0}, 8.0 / (8.0 + 64.0), false},
		{"shape one", []any{1.0, 3.0, 1.0}, 0.25, false},
		{"int arguments", []any{6, 2, 1}, 0.75, false},
		{"zero spend", []any{0.0, 5.0, 1.5}, 0.0, false},
		{"infinite spend", []any{math.Inf(1), 5.0, 1.5}, 1.0, false},
		{"huge ratio no overflow", []any{1e300, 1.0, 4.0}, 1.0, false},
		{"tiny ratio underflows to 0", []any{1e-300, 1.0, 4.0}, 0.0, false},
		{"null x", []any{nil, 3.0, 2.0}, nil, false},
		{"null half", []any{1.0, nil, 2.0}, nil, false},
		{"null shape", []any{1.0, 3.0, nil}, nil, false},
		{"negative x", []any{-1.0, 3.0, 2.0}, nan, false},
		{"zero half", []any{1.0, 0.0, 2.0}, nan, false},
		{"negative half", []any{1.0, -3.0, 2.0}, nan, false},
		{"zero shape", []any{1.0, 3.0, 0.0}, nan, false},
		{"negative shape", []any{1.0, 3.0, -2.0}, nan, false},
		{"NaN x", []any{nan, 3.0, 2.0}, nan, false},
		{"string argument", []any{"a", 3.0, 2.0}, nil, true},
		{"arity", []any{1.0, 3.0}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Impl(FuncHill)(c.args...)
			if c.wantErr {
				if err == nil {
					t.Fatalf("hill(%v) = %v, want an error", c.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("hill(%v): %v", c.args, err)
			}
			switch w := c.want.(type) {
			case nil:
				if got != nil {
					t.Errorf("hill(%v) = %v, want nil", c.args, got)
				}
			case float64:
				g, ok := got.(float64)
				if !ok {
					t.Fatalf("hill(%v) = %T, want float64", c.args, got)
				}
				if math.IsNaN(w) {
					if !math.IsNaN(g) {
						t.Errorf("hill(%v) = %v, want NaN", c.args, g)
					}
					return
				}
				if math.Abs(g-w) > 1e-15 {
					t.Errorf("hill(%v) = %.17g, want %.17g", c.args, g, w)
				}
			}
		})
	}
}

// TestHill_InExpression: the reserved built-in is registered in the
// shared environment, so a formula calls it on fields.
func TestHill_InExpression(t *testing.T) {
	opts := append([]expr.Option{expr.Env(map[string]any{"spend": 0.0, "k": 0.0})}, Bound{}.Options()...)
	prog, err := expr.Compile("hill(spend, k, 2) * 10", opts...)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := expr.Run(prog, map[string]any{"spend": 4.0, "k": 4.0})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != 5.0 {
		t.Errorf("hill(4, 4, 2) * 10 = %v, want 5", got)
	}
	if !IsReserved(FuncHill) {
		t.Error("hill is not reserved")
	}
}
