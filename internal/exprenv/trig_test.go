package exprenv

import (
	"math"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// TestTrigBuiltins_Values: sin / cos / tan / atan2 answer detmath's
// bits, the constants answer math.Pi and math.E exactly, a nil argument
// answers nil, ±Inf and NaN answer NaN, and a decimal argument reads as
// its float64 like every float-domain built-in.
func TestTrigBuiltins_Values(t *testing.T) {
	t.Parallel()

	unary := map[string]func(float64) float64{FuncSin: detmath.Sin, FuncCos: detmath.Cos, FuncTan: detmath.Tan}
	for name, k := range unary {
		for _, x := range []float64{-7.25, -1, 0, 0.5, math.Pi / 3, 1e5} {
			got, err := builtinImpls[name](x)
			if err != nil {
				t.Fatalf("%s(%v): %v", name, x, err)
			}
			if math.Float64bits(got.(float64)) != math.Float64bits(k(x)) {
				t.Errorf("%s(%v) = %v, want detmath's %v", name, x, got, k(x))
			}
		}
		for _, x := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
			if got, err := builtinImpls[name](x); err != nil || !math.IsNaN(got.(float64)) {
				t.Errorf("%s(%v) = %v, %v; want NaN", name, x, got, err)
			}
		}
		if got, err := builtinImpls[name](nil); err != nil || got != nil {
			t.Errorf("%s(nil) = %v, %v; want nil", name, got, err)
		}
		if _, err := builtinImpls[name]("x"); err == nil || topCode(err) != perr.PROCESSING_RUNTIME {
			t.Errorf("%s(\"x\"): %v, want PROCESSING_RUNTIME", name, err)
		}
	}
	got, err := builtinImpls[FuncAtan2](int64(1), float32(-1))
	if err != nil || math.Float64bits(got.(float64)) != math.Float64bits(detmath.Atan2(1, -1)) {
		t.Errorf("atan2(1, -1) = %v, %v; want %v", got, err, detmath.Atan2(1, -1))
	}
	for _, args := range [][]any{{nil, 1.0}, {1.0, nil}} {
		if got, err := builtinImpls[FuncAtan2](args...); err != nil || got != nil {
			t.Errorf("atan2(%v) = %v, %v; want nil", args, got, err)
		}
	}
	for _, args := range [][]any{{1.0}, {1.0, 2.0, 3.0}} {
		if _, err := builtinImpls[FuncAtan2](args...); err == nil || topCode(err) != perr.PROCESSING_RUNTIME {
			t.Errorf("atan2 with %d arguments: %v, want PROCESSING_RUNTIME", len(args), err)
		}
	}
	if _, err := builtinImpls[FuncAtan2]("y", 1.0); err == nil {
		t.Error("atan2(\"y\", 1): want an error")
	}
	if got, err := builtinImpls[FuncPi](); err != nil || got != math.Pi {
		t.Errorf("pi() = %v, %v; want math.Pi", got, err)
	}
	if got, err := builtinImpls[FuncE](); err != nil || got != math.E {
		t.Errorf("e() = %v, %v; want math.E", got, err)
	}
	if got, err := builtinImpls[FuncSin](Decimal{Value: encoding.NewDecimal128FromInt(150), Scale: 2}); err != nil || got != detmath.Sin(1.5) {
		t.Errorf("sin(1.50 decimal) = %v, %v; want %v", got, err, detmath.Sin(1.5))
	}
}

// TestTrigBuiltins_CompileThroughOptions: the new names are registered
// in the shared environment — the Fourier seasonality term compiles and
// evaluates — and a bare pi / e is not the constant (it is the call).
func TestTrigBuiltins_CompileThroughOptions(t *testing.T) {
	t.Parallel()

	env := map[string]any{"week": 13.0}
	prog, err := expr.Compile(`sin(2 * pi() * week / 52) + cos(2 * pi() * week / 52) + atan2(1, 1) * 4 - e()`, append(Options(), expr.Env(env))...)
	if err != nil {
		t.Fatal(err)
	}
	out, err := expr.Run(prog, env)
	if err != nil {
		t.Fatal(err)
	}
	x := float64(2*math.Pi*13) / 52
	want := detmath.Sin(x) + detmath.Cos(x) + detmath.Atan2(1, 1)*4 - math.E
	if math.Float64bits(out.(float64)) != math.Float64bits(want) {
		t.Errorf("Fourier term = %v, want %v", out, want)
	}
	if _, err := expr.Compile(`2 * pi`, append(Options(), expr.Env(env))...); err == nil {
		t.Error("a bare `pi` compiled as a number; the constant is the call pi()")
	}
}

// TestExprTranscendentals_AreDetmath: every transcendental built-in —
// exp, log, log1p, pow, hill's power and the trig set — answers
// linalg/detmath's bits over a dense grid, so a formula gives the
// same value on every architecture and CPU. The grid is wide enough
// that a kernel reverted to math.* differs somewhere on arm64 (fused
// multiply-adds) and on amd64 (the Exp / Log assembly).
func TestExprTranscendentals_AreDetmath(t *testing.T) {
	t.Parallel()

	type check struct {
		call func(x float64) (any, error)
		want func(x float64) float64
	}
	checks := map[string]check{
		"exp":   {func(x float64) (any, error) { return builtinImpls["exp"](x) }, detmath.Exp},
		"log":   {func(x float64) (any, error) { return builtinImpls["log"](x) }, detmath.Log},
		"log1p": {func(x float64) (any, error) { return builtinImpls["log1p"](x) }, detmath.Log1p},
		"pow":   {func(x float64) (any, error) { return builtinImpls["pow"](x, 1.7) }, func(x float64) float64 { return detmath.Pow(x, 1.7) }},
		"hill": {func(x float64) (any, error) { return builtinImpls["hill"](x, 0.8, 1.7) },
			func(x float64) float64 { return 1 / (1 + detmath.Pow(0.8/x, 1.7)) }},
		"sin": {func(x float64) (any, error) { return builtinImpls["sin"](x) }, detmath.Sin},
		"cos": {func(x float64) (any, error) { return builtinImpls["cos"](x) }, detmath.Cos},
		"tan": {func(x float64) (any, error) { return builtinImpls["tan"](x) }, detmath.Tan},
		"atan2": {func(x float64) (any, error) { return builtinImpls["atan2"](x, 0.37) },
			func(x float64) float64 { return detmath.Atan2(x, 0.37) }},
	}
	for name, c := range checks {
		for i := 1; i <= 5000; i++ {
			x := float64(i) * 0.000731
			got, err := c.call(x)
			if err != nil {
				t.Fatalf("%s(%v): %v", name, x, err)
			}
			if math.Float64bits(got.(float64)) != math.Float64bits(c.want(x)) {
				t.Errorf("%s(%v) = %v, want detmath's %v", name, x, got, c.want(x))
				break
			}
		}
	}
}
