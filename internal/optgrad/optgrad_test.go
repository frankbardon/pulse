package optgrad

import (
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/internal/exprenv"
)

// compile compiles src as an optimize block does (internal/optblock
// compileOptions): the variables declared float64 over the shared env
// with the request-bound declarations.
func compile(t *testing.T, src string, vars []string, bound exprenv.Bound) *vm.Program {
	t.Helper()
	decl := map[string]any{}
	for _, v := range vars {
		decl[v] = float64(0)
	}
	prog, err := expr.Compile(src, append([]expr.Option{expr.Env(decl)}, bound.Options()...)...)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	return prog
}

func run(t *testing.T, prog *vm.Program, vars []string, x []float64) float64 {
	t.Helper()
	env := map[string]any{}
	for j, v := range vars {
		env[v] = x[j]
	}
	out, err := expr.Run(prog, env)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		return math.NaN()
	}
	f, ok := toFloat(out)
	if !ok {
		t.Fatalf("non-number %T", out)
	}
	return f
}

// fd is the central difference of prog in variable j at x.
func fd(t *testing.T, prog *vm.Program, vars []string, x []float64, j int) float64 {
	h := 1e-6 * math.Max(1, math.Abs(x[j]))
	p := slices.Clone(x)
	p[j] = x[j] + h
	fp := run(t, prog, vars, p)
	p[j] = x[j] - h
	fm := run(t, prog, vars, p)
	return (fp - fm) / (2 * h)
}

// TestRules_AgreeWithFiniteDifferences: on every rule family and
// operator the analytic gradient agrees with a central difference
// within 1e-6·max(1, |∂|) (the FD truncation + rounding error at
// h = 1e-6·max(1, |x|) on these smooth, unit-scale expressions is below
// 1e-7), and the dual's value is the VM's value bit for bit.
func TestRules_AgreeWithFiniteDifferences(t *testing.T) {
	t.Parallel()

	vars := []string{"x", "y"}
	points := [][]float64{{0.7, 1.3}, {2.3, 0.4}, {1.1, 3.2}}
	exprs := []string{
		"x + y", "x - 2 * y", "x * y", "x / y", "y / x", "-x + +y", "3 / (1 + x * x)",
		"sqrt(x * y)", "log(x) + log1p(y)", "exp(x / 2) * y", "pow(x, y)", "pow(x, 2.5)", "pow(2, x)",
		"x ** 3 - y ^ 2", "x ** y",
		"sin(x) * cos(y)", "tan(x / 3)", "atan2(y, x)", "pi() * x + e() * y",
		"x % 1.5 + y", "abs(x - y)", "ceil(x) + floor(y) * x + round(x)", "float(x) * y",
		"max(x, y, 1)", "min(x * 2, y)",
		"x > y ? x * x : y * y", "x > 1 && y > 1 ? x : -y", "(x < 1 || y < 1) ? exp(x) : log(y)",
		"!(x > y) ? x : y", "x == y ? 0 : x * y", "x != 1 ? x : y",
		"(x - 3) * (x - 3) + 100 * (y - x * x) * (y - x * x)",
		"x + params.k * y", "params.k ** 2 * x",
	}
	bound := exprenv.Bound{Params: exprenv.Params{"k": 2.5}}
	for _, src := range exprs {
		prog := compile(t, src, vars, bound)
		p, unsupported := Compile(prog.Node(), vars, Options{})
		if p == nil {
			t.Fatalf("%q unsupported: %v", src, unsupported)
		}
		for _, x := range points {
			v, g, err := p.Eval(x)
			if err != nil {
				t.Fatalf("%q at %v: %v", src, x, err)
			}
			if want := run(t, prog, vars, x); math.Float64bits(v) != math.Float64bits(want) {
				t.Errorf("%q at %v: value %v, VM %v", src, x, v, want)
			}
			for j := range vars {
				want := fd(t, prog, vars, x, j)
				if math.Abs(g[j]-want) > 1e-6*math.Max(1, math.Abs(want)) {
					t.Errorf("%q at %v: ∂/∂%s = %v, finite difference %v", src, x, vars[j], g[j], want)
				}
			}
		}
	}
}

// TestCompile_Unsupported: a function without a rule, a collection, a
// member access and a let are named — sorted and unique, rewritten
// operators under the operator a block wrote — and no program is
// returned; an extra rule makes a function supported.
func TestCompile_Unsupported(t *testing.T) {
	t.Parallel()

	vars := []string{"x"}
	coef := func(args ...any) (any, error) { return 2.0, nil }
	bound := exprenv.Bound{Coef: coef, Extensions: []exprenv.NamedFunc{{Name: "rate", Fn: func(args ...any) (any, error) { return 1.5, nil }}}}
	cases := []struct {
		src  string
		want []string
	}{
		{"hill(x, 40, 2) + hill(x, 1, 1)", []string{"hill"}},
		{`coef("m", "t") * x + rate(x)`, []string{"coef", "rate"}},
		{"filter([x, 1], # > 0)[0]", []string{"array", "find", "predicate"}},
		{"let a = x; a * 2", []string{"let"}},
		{"x in [1, 2] ? 1 : x", []string{"array", "operator in"}},
	}
	for _, tc := range cases {
		prog := compile(t, tc.src, vars, bound)
		p, got := Compile(prog.Node(), vars, Options{})
		if p != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: program %v unsupported %v, want nil and %v", tc.src, p != nil, got, tc.want)
		}
		if u := Unsupported(prog.Node(), vars, Options{}); !reflect.DeepEqual(u, tc.want) {
			t.Errorf("%q: Unsupported %v", tc.src, u)
		}
	}

	prog := compile(t, `coef("m", "t") * x`, vars, bound)
	rules := map[string]Rule{"coef": {Impl: coef, Partials: func(a []float64, v float64) []float64 { return []float64{0, 0} }}}
	p, u := Compile(prog.Node(), vars, Options{Rules: rules})
	if p == nil {
		t.Fatalf("extra rule left %v unsupported", u)
	}
	if v, g, err := p.Eval([]float64{3}); err != nil || v != 6 || g[0] != 2 {
		t.Fatalf("coef·x at 3 = %v, %v (%v), want 6, [2]", v, g, err)
	}
}

// TestEval_ConstantNilAndFaults: a constant expression has a zero
// gradient; a nil result reads NaN with a NaN gradient (the VM's nil);
// an operand of the wrong kind is ErrEval, never a made-up number.
func TestEval_ConstantNilAndFaults(t *testing.T) {
	t.Parallel()

	vars := []string{"x", "y"}
	p, _ := Compile(compile(t, "sqrt(4) + 1", vars, exprenv.Bound{}).Node(), vars, Options{})
	if v, g, err := p.Eval([]float64{1, 2}); err != nil || v != 3 || g[0] != 0 || g[1] != 0 {
		t.Fatalf("constant: %v %v %v", v, g, err)
	}
	p, _ = Compile(compile(t, "x > 0 ? nil : y", vars, exprenv.Bound{}).Node(), vars, Options{})
	if v, g, err := p.Eval([]float64{1, 2}); err != nil || !math.IsNaN(v) || !math.IsNaN(g[0]) || !math.IsNaN(g[1]) {
		t.Fatalf("nil: %v %v %v", v, g, err)
	}
	if v, g, err := p.Eval([]float64{-1, 2}); err != nil || v != 2 || g[0] != 0 || g[1] != 1 {
		t.Fatalf("else branch: %v %v %v", v, g, err)
	}
	p, _ = Compile(compile(t, "(x > 0 ? nil : 1) + y", vars, exprenv.Bound{}).Node(), vars, Options{})
	if _, _, err := p.Eval([]float64{1, 2}); err != ErrEval {
		t.Fatalf("nil + y: %v, want ErrEval", err)
	}
}

// TestEval_Deterministic: one point twice gives the same gradient bits
// (no map-order or allocation dependence in the tangent sums).
func TestEval_Deterministic(t *testing.T) {
	t.Parallel()

	vars := []string{"a", "b", "c"}
	src := "exp(a * b) / (1 + c * c) + pow(a, 1.7) * sin(b) - atan2(c, a) * log(b)"
	p, _ := Compile(compile(t, src, vars, exprenv.Bound{}).Node(), vars, Options{})
	x := []float64{0.9, 1.7, -0.4}
	_, g1, _ := p.Eval(x)
	for k := 0; k < 5; k++ {
		_, g2, _ := p.Eval(x)
		for j := range g1 {
			if math.Float64bits(g1[j]) != math.Float64bits(g2[j]) {
				t.Fatalf("run %d: %v vs %v", k, g2, g1)
			}
		}
	}
}

// TestRules_ListsTheTable: Rules reports the function and built-in
// tables (the maths set, the rewritten operators, expr-lang's numeric
// built-ins), and the maths set is every exprenv MathNames entry but
// hill (the fit readers' story).
func TestRules_ListsTheTable(t *testing.T) {
	t.Parallel()

	fns, bis := Rules()
	for _, n := range exprenv.MathNames() {
		if n == exprenv.FuncHill {
			if slices.Contains(fns, n) {
				t.Errorf("hill has a rule; move it with the fit readers")
			}
			continue
		}
		if !slices.Contains(fns, n) {
			t.Errorf("maths built-in %s has no derivative rule", n)
		}
	}
	for _, n := range []string{exprenv.PowFuncName, exprenv.PowCaretFuncName, exprenv.ModFuncName} {
		if !slices.Contains(fns, n) {
			t.Errorf("rewritten operator %s has no rule", n)
		}
	}
	slices.Sort(bis)
	if want := []string{"abs", "ceil", "float", "floor", "max", "min", "round"}; !reflect.DeepEqual(bis, want) {
		t.Errorf("built-ins %v, want %v", bis, want)
	}
}
