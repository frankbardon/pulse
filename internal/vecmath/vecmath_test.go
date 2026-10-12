package vecmath

import (
	"errors"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var (
	nan  = math.NaN()
	inf  = math.Inf(1)
	ninf = math.Inf(-1)
)

// same reports exact equality with NaN == NaN.
func same(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return a == b
}

// near reports |a-b| <= 1e-12 with NaN == NaN.
func near(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	if a == b {
		return true
	}
	return math.Abs(a-b) <= 1e-12
}

func sameSlice(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !near(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestReductions(t *testing.T) {
	type fn struct {
		name string
		f    func([]float64) float64
	}
	fns := []fn{{"Sum", Sum}, {"Mean", Mean}, {"Min", Min}, {"Max", Max}, {"SD", SD}, {"Norm", Norm}}
	cases := []struct {
		name string
		in   []float64
		want map[string]float64
	}{
		{"nil", nil, map[string]float64{"Sum": nan, "Mean": nan, "Min": nan, "Max": nan, "SD": nan, "Norm": nan}},
		{"empty", []float64{}, map[string]float64{"Sum": nan, "Mean": nan, "Min": nan, "Max": nan, "SD": nan, "Norm": nan}},
		{"all-nan", []float64{nan, nan}, map[string]float64{"Sum": nan, "Mean": nan, "Min": nan, "Max": nan, "SD": nan, "Norm": nan}},
		{"single", []float64{3}, map[string]float64{"Sum": 3, "Mean": 3, "Min": 3, "Max": 3, "SD": nan, "Norm": 3}},
		{"single-valid-among-nan", []float64{nan, -4, nan}, map[string]float64{"Sum": -4, "Mean": -4, "Min": -4, "Max": -4, "SD": nan, "Norm": 4}},
		{"zeros", []float64{0, 0}, map[string]float64{"Sum": 0, "Mean": 0, "Min": 0, "Max": 0, "SD": 0, "Norm": 0}},
		{"plain", []float64{2, 4, 4, 4, 5, 5, 7, 9}, map[string]float64{
			"Sum": 40, "Mean": 5, "Min": 2, "Max": 9, "SD": math.Sqrt(32.0 / 7), "Norm": math.Sqrt(232)}},
		{"nan-skipped", []float64{nan, 1, nan, 3, nan}, map[string]float64{
			"Sum": 4, "Mean": 2, "Min": 1, "Max": 3, "SD": math.Sqrt2, "Norm": math.Sqrt(10)}},
		{"nan-first-for-minmax", []float64{nan, 5, -1}, map[string]float64{
			"Sum": 4, "Mean": 2, "Min": -1, "Max": 5, "SD": math.Sqrt(18), "Norm": math.Sqrt(26)}},
		{"infinities", []float64{ninf, 0, inf}, map[string]float64{
			"Sum": nan, "Mean": nan, "Min": ninf, "Max": inf, "SD": nan, "Norm": inf}},
	}
	for _, c := range cases {
		for _, f := range fns {
			t.Run(c.name+"/"+f.name, func(t *testing.T) {
				got := f.f(c.in)
				if want := c.want[f.name]; !near(got, want) {
					t.Fatalf("%s(%v) = %v, want %v", f.name, c.in, got, want)
				}
			})
		}
	}
}

func TestCount(t *testing.T) {
	cases := []struct {
		in   []float64
		want int
	}{
		{nil, 0},
		{[]float64{nan, nan}, 0},
		{[]float64{1, nan, 0, inf}, 3},
		{[]float64{1, 2, 3}, 3},
	}
	for i, c := range cases {
		if got := Count(c.in); got != c.want {
			t.Errorf("case %d: Count(%v) = %d, want %d", i, c.in, got, c.want)
		}
	}
}

// TestPairwise pins Dot's pairwise-complete rule. dist and cosine
// moved to the linalg/metric registry (TestExprMetrics_* in
// internal/processing pins them through the expression layer).
func TestPairwise(t *testing.T) {
	cases := []struct {
		name string
		a, b []float64
		dot  float64
	}{
		{"empty", []float64{}, []float64{}, nan},
		{"no-complete-pair", []float64{nan, 1}, []float64{2, nan}, nan},
		{"plain", []float64{1, 2, 3}, []float64{4, 5, 6}, 32},
		{"pair-dropped-either-side", []float64{1, nan, 3, 10}, []float64{1, 7, nan, 1}, 11},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, 0},
		{"parallel", []float64{2, 4}, []float64{1, 2}, 10},
		{"zero-norm-left", []float64{0, 0}, []float64{1, 2}, 0},
		{"underflowed-norm-left", []float64{1e-200}, []float64{1}, 1e-200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := Dot(c.a, c.b); err != nil || !near(got, c.dot) {
				t.Errorf("Dot = %v, %v; want %v", got, err, c.dot)
			}
		})
	}
}

func TestPairwiseLengthMismatch(t *testing.T) {
	pairs := []struct{ a, b []float64 }{
		{[]float64{1, 2}, []float64{1}},
		{nil, []float64{nan}},
		{[]float64{nan, nan, nan}, []float64{nan}},
	}
	for _, p := range pairs {
		got, err := Dot(p.a, p.b)
		if !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("Dot(%v,%v) err = %v, want ErrLengthMismatch", p.a, p.b, err)
		}
		var lm *LengthMismatchError
		if !errors.As(err, &lm) || lm.Left != len(p.a) || lm.Right != len(p.b) {
			t.Fatalf("err %#v does not carry lengths %d/%d", err, len(p.a), len(p.b))
		}
		if math.IsNaN(got) {
			t.Fatal("mismatch must not yield NaN")
		}
		if !strings.Contains(err.Error(), strconv.Itoa(len(p.a))) {
			t.Fatalf("message %q lacks the left length", err.Error())
		}
	}
	if errors.Is(&LengthMismatchError{}, ErrInvalidNormalizeMode) {
		t.Fatal("LengthMismatchError must not match ErrInvalidNormalizeMode")
	}
}

func TestArgMaxArgMin(t *testing.T) {
	cases := []struct {
		name     string
		in       []float64
		max, min int
		ok       bool
	}{
		{"nil", nil, -1, -1, false},
		{"all-nan", []float64{nan, nan}, -1, -1, false},
		{"single", []float64{7}, 0, 0, true},
		{"plain", []float64{3, 9, 1, 4}, 1, 2, true},
		{"ties-first-wins", []float64{5, 1, 5, 1}, 0, 1, true},
		{"nan-leading", []float64{nan, 2, 8, nan}, 2, 1, true},
		{"all-equal", []float64{2, 2, 2}, 0, 0, true},
		{"infinities", []float64{0, inf, ninf}, 1, 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if i, ok := ArgMax(c.in); ok != c.ok || (ok && i != c.max) {
				t.Errorf("ArgMax = %d,%v; want %d,%v", i, ok, c.max, c.ok)
			}
			if i, ok := ArgMin(c.in); ok != c.ok || (ok && i != c.min) {
				t.Errorf("ArgMin = %d,%v; want %d,%v", i, ok, c.min, c.ok)
			}
		})
	}
}

func TestCenterZScore(t *testing.T) {
	sd := math.Sqrt(2) // sd of {1, 3}
	cases := []struct {
		name           string
		in             []float64
		center, zscore []float64
	}{
		{"empty", []float64{}, []float64{}, []float64{}},
		{"all-nan", []float64{nan, nan}, []float64{nan, nan}, []float64{nan, nan}},
		{"single-valid", []float64{nan, 4}, []float64{nan, 0}, []float64{nan, nan}},
		{"zero-sd", []float64{3, 3, nan}, []float64{0, 0, nan}, []float64{nan, nan, nan}},
		// d*d underflows so sd == 0 while x - mean != 0: the explicit
		// zero-sd guard must still give all-NaN, not ±Inf.
		{"underflowed-sd", []float64{0, 1e-300}, []float64{-5e-301, 5e-301}, []float64{nan, nan}},
		{"plain", []float64{1, 3}, []float64{-1, 1}, []float64{-1 / sd, 1 / sd}},
		{"nan-kept-in-place", []float64{1, nan, 3}, []float64{-1, nan, 1}, []float64{-1 / sd, nan, 1 / sd}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := append([]float64(nil), c.in...)
			if got := Center(in); !sameSlice(got, c.center) {
				t.Errorf("Center = %v, want %v", got, c.center)
			}
			if got := ZScore(in); !sameSlice(got, c.zscore) {
				t.Errorf("ZScore = %v, want %v", got, c.zscore)
			}
			assertUnmutated(t, in, c.in)
		})
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name   string
		in     []float64
		l1, l2 []float64
	}{
		{"empty", []float64{}, []float64{}, []float64{}},
		{"all-nan", []float64{nan}, []float64{nan}, []float64{nan}},
		{"zero-norm", []float64{0, nan, 0}, []float64{nan, nan, nan}, []float64{nan, nan, nan}},
		// x*x underflows so the L2 norm is 0 while x != 0: the explicit
		// zero-norm guard must still give all-NaN, not +Inf.
		{"underflowed-l2", []float64{1e-200}, []float64{1}, []float64{nan}},
		{"plain", []float64{3, -4}, []float64{3.0 / 7, -4.0 / 7}, []float64{0.6, -0.8}},
		{"nan-kept-in-place", []float64{3, nan, 4}, []float64{3.0 / 7, nan, 4.0 / 7}, []float64{0.6, nan, 0.8}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := append([]float64(nil), c.in...)
			for mode, want := range map[string][]float64{NormL1: c.l1, NormL2: c.l2} {
				got, err := Normalize(in, mode)
				if err != nil || !sameSlice(got, want) {
					t.Errorf("Normalize(%v, %q) = %v, %v; want %v", c.in, mode, got, err, want)
				}
			}
			assertUnmutated(t, in, c.in)
		})
	}
}

func TestNormalizeInvalidMode(t *testing.T) {
	for _, mode := range []string{"", "L2", "max", "l3"} {
		got, err := Normalize([]float64{1, 2}, mode)
		if got != nil || !errors.Is(err, ErrInvalidNormalizeMode) {
			t.Fatalf("Normalize(_, %q) = %v, %v; want nil, ErrInvalidNormalizeMode", mode, got, err)
		}
		var im *InvalidNormalizeModeError
		if !errors.As(err, &im) || im.Mode != mode || !strings.Contains(err.Error(), strconv.Quote(mode)) {
			t.Fatalf("Normalize(_, %q) err %v does not carry the mode", mode, err)
		}
		if errors.Is(err, ErrLengthMismatch) {
			t.Fatal("InvalidNormalizeModeError must not match ErrLengthMismatch")
		}
	}
}

// TestVectorResultsAreFresh pins that vector-returning kernels never
// alias the input: callers bind reused per-worker buffers.
func TestVectorResultsAreFresh(t *testing.T) {
	in := []float64{1, 2, 3}
	l2, _ := Normalize(in, NormL2)
	for name, out := range map[string][]float64{"Center": Center(in), "ZScore": ZScore(in), "Normalize": l2} {
		out[0] = 99
		if in[0] != 1 {
			t.Fatalf("%s result aliases its input", name)
		}
	}
}

func TestScalar(t *testing.T) {
	cases := []struct {
		name      string
		got, want float64
	}{
		{"sqrt", Sqrt(9), 3},
		{"sqrt-neg", Sqrt(-1), nan},
		{"log", Log(math.E), 1},
		{"log-zero", Log(0), ninf},
		{"log-neg", Log(-1), nan},
		{"exp", Exp(0), 1},
		{"exp-overflow", Exp(1000), inf},
		{"pow", Pow(2, 10), 1024},
		{"pow-neg-frac", Pow(-8, 1.0/3), nan},
	}
	for _, c := range cases {
		if !same(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestFMAFreeOrder pins the documented left-to-right summation: a
// reordered or compensated sum would give a different answer here.
func TestFMAFreeOrder(t *testing.T) {
	v := []float64{1e16, 1, -1e16}
	if got := Sum(v); got != 0 {
		t.Fatalf("Sum(%v) = %v, want 0 (left-to-right)", v, got)
	}
	got, err := Dot([]float64{1e16, 1, -1e16}, []float64{1, 1, 1})
	if err != nil || got != 0 {
		t.Fatalf("Dot = %v, %v; want 0 (left-to-right)", got, err)
	}
}

// TestVecmathImportBoundary keeps the package a stdlib-only leaf so
// internal/processing and internal/descriptor can both import it.
func TestVecmathImportBoundary(t *testing.T) {
	allowed := map[string]bool{"errors": true, "fmt": true, "math": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[p] {
				t.Errorf("%s imports %q; vecmath must stay a stdlib-only leaf", f, p)
			}
		}
	}
}

func assertUnmutated(t *testing.T, got, want []float64) {
	t.Helper()
	for i := range want {
		if !same(got[i], want[i]) {
			t.Fatalf("input mutated at %d: %v, want %v", i, got, want)
		}
	}
}
