package exprenv

import (
	"math"
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// powProbe is a base where math.Pow(powProbe, 1.7) and detmath.Pow
// disagree in the last bit (on arm64 at least), so a `**` left on
// expr-lang's own math.Pow is visible.
const powProbe = 2.9552898640566303

// mathCompile compiles src against a row-shaped env (x, y floats, c a
// string) and only MathOptions.
func mathCompile(src string) (func() (any, error), error) {
	env := map[string]any{"x": 0.7, "y": 2.5, "z": powProbe, "c": "a"}
	prog, err := expr.Compile(src, append([]expr.Option{expr.Env(env)}, MathOptions()...)...)
	if err != nil {
		return nil, err
	}
	return func() (any, error) { return expr.Run(prog, env) }, nil
}

func TestMathNames_AreReserved(t *testing.T) {
	t.Parallel()

	names := MathNames()
	if len(names) != 12 {
		t.Fatalf("MathNames = %v, want the 12 scalar-maths built-ins", names)
	}
	for _, n := range names {
		if !IsReserved(n) {
			t.Errorf("%s: in MathNames but not reserved", n)
		}
		if builtinImpls[n] == nil {
			t.Errorf("%s: no implementation", n)
		}
	}
	names[0] = "mutated"
	if MathNames()[0] == "mutated" {
		t.Error("MathNames returned the backing slice")
	}
}

// TestMathOptions_SameBitsAsQueryImpl: every built-in answers exactly the
// query registration's value, i.e. detmath's (the shared-constructor
// promise).
func TestMathOptions_SameBitsAsQueryImpl(t *testing.T) {
	t.Parallel()

	cases := []struct {
		src  string
		want float64
	}{
		{"sqrt(y)", math.Sqrt(2.5)},
		{"exp(x)", detmath.Exp(0.7)},
		{"log(y)", detmath.Log(2.5)},
		{"log1p(x)", detmath.Log1p(0.7)},
		{"pow(y, x)", detmath.Pow(2.5, 0.7)},
		{"sin(x)", detmath.Sin(0.7)},
		{"cos(x)", detmath.Cos(0.7)},
		{"tan(x)", detmath.Tan(0.7)},
		{"atan2(x, y)", detmath.Atan2(0.7, 2.5)},
		{"pi()", math.Pi},
		{"e()", math.E},
		{"y ** x", detmath.Pow(2.5, 0.7)},
		{"y ^ x", detmath.Pow(2.5, 0.7)},
		{"z ** 1.7", detmath.Pow(powProbe, 1.7)},
		{"z ^ 1.7", detmath.Pow(powProbe, 1.7)},
		{"y % 1", math.Mod(2.5, 1)},
		{"-y % 2", math.Mod(-2.5, 2)},
		{"sin(1)", detmath.Sin(1)}, // an int literal converts
	}
	for _, c := range cases {
		run, err := mathCompile(c.src)
		if err != nil {
			t.Errorf("%s: compile: %v", c.src, err)
			continue
		}
		got, err := run()
		if err != nil {
			t.Errorf("%s: run: %v", c.src, err)
			continue
		}
		f, ok := got.(float64)
		if !ok || math.Float64bits(f) != math.Float64bits(c.want) {
			t.Errorf("%s = %v (%T), want %v", c.src, got, got, c.want)
		}
	}
	// hill against the query implementation directly.
	run, err := mathCompile("hill(y, 1.5, 2)")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := run()
	want, _ := Impl("hill")(2.5, 1.5, 2)
	if got != want {
		t.Errorf("hill = %v, want %v", got, want)
	}
	// int % int stays expr-lang's integer modulo.
	run, err = mathCompile("7 % 3")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := run(); got != 1 {
		t.Errorf("7 %% 3 = %v (%T), want int 1", got, got)
	}
}

// TestMathOptions_StaticRefusals: arity and argument type are compile
// errors, and nothing query-only comes along.
func TestMathOptions_StaticRefusals(t *testing.T) {
	t.Parallel()

	for _, src := range []string{
		"sin(x, y)", "pow(x)", "pi(1)", "hill(x, y)", "atan2(x)",
		"sqrt(c)", "exp(c) > 1",
		"vsum(x)", "weekday(x)", "has_any(c, c)", "total(x)", "jaccard(c, c)", "ref(c, x)",
	} {
		if _, err := mathCompile(src); err == nil {
			t.Errorf("%s: compiled, want a compile error", src)
		}
	}
	// The result is typed float64, so a string concatenation is refused.
	if _, err := mathCompile(`sin(x) + "a"`); err == nil || !strings.Contains(err.Error(), "float64") {
		t.Errorf(`sin(x) + "a": err = %v, want a float64 type error`, err)
	}
}
