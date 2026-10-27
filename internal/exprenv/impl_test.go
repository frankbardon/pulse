package exprenv

import (
	stderrors "errors"
	"math"
	"sort"
	"testing"

	"github.com/expr-lang/expr"

	perr "github.com/frankbardon/pulse/errors"
)

// TestExprBuiltins_EveryNameImplemented pins the shared name list to the
// implementation table in both directions.
func TestExprBuiltins_EveryNameImplemented(t *testing.T) {
	t.Parallel()

	names := Names()
	for _, name := range names {
		if builtinImpls[name] == nil {
			t.Errorf("reserved built-in %q has no implementation", name)
		}
	}
	if len(builtinImpls) != len(names) {
		var impl []string
		for k := range builtinImpls {
			impl = append(impl, k)
		}
		sort.Strings(impl)
		t.Errorf("implementation table %v has names outside the reserved list %v", impl, names)
	}
	if got := len(builtinOptions()); got != len(names) {
		t.Errorf("builtinOptions registers %d functions, want %d", got, len(names))
	}
}

// TestVectorBuiltins_Edges pins the expr-layer adaptations: argmax of an
// all-NaN vector is NaN, a []float64 argument is accepted as is and not
// mutated, and the vector transforms return a new slice.
func TestVectorBuiltins_Edges(t *testing.T) {
	t.Parallel()

	got, err := builtinImpls["argmax"]([]any{nil, nil})
	if err != nil || !math.IsNaN(got.(float64)) {
		t.Errorf("argmax([nil, nil]) = %v, %v; want NaN", got, err)
	}
	got, err = builtinImpls["argmin"]([]float64{3, 1, 1})
	if err != nil || got != 1 {
		t.Errorf("argmin = %v, %v; want int 1", got, err)
	}
	in := []float64{1, 2, 3}
	out, err := builtinImpls["vcenter"](in)
	if err != nil {
		t.Fatal(err)
	}
	o := out.([]float64)
	o[0] = 99
	if in[0] != 1 {
		t.Error("vcenter returned or mutated its bound argument")
	}
	got, err = builtinImpls["vsum"]([]any{nil})
	if err != nil || !math.IsNaN(got.(float64)) {
		t.Errorf("vsum([nil]) = %v, %v; want NaN", got, err)
	}
	got, err = builtinImpls["sqrt"](-1)
	if err != nil || !math.IsNaN(got.(float64)) {
		t.Errorf("sqrt(-1) = %v, %v; want NaN (IEEE)", got, err)
	}
	got, err = builtinImpls["pow"](uint8(2), float32(0.5))
	if err != nil || !closeTo(got.(float64), math.Sqrt2) {
		t.Errorf("pow(2, 0.5) = %v, %v", got, err)
	}
	for name, fn := range builtinImpls {
		if name == FuncPi || name == FuncE {
			// The zero-argument constants: one argument is the misuse.
			if _, err := fn(1.0); err == nil || topCode(err) != perr.PROCESSING_RUNTIME {
				t.Errorf("%s(1): %v, want PROCESSING_RUNTIME arity error", name, err)
			}
			continue
		}
		if _, err := fn(); err == nil || topCode(err) != perr.PROCESSING_RUNTIME {
			t.Errorf("%s() with no arguments: %v, want PROCESSING_RUNTIME arity error", name, err)
		}
	}
	for _, name := range []string{"pow", "dot", "dist", "cosine", "vnormalize"} {
		if _, err := builtinImpls[name]([]any{1}, "x"); err == nil {
			t.Errorf("%s with a bad second argument: want an error", name)
		}
	}
}

// TestSetHelpers_EveryNameImplemented: every SetHelperNames
// entry (the list the overlay predict gate admits) has an
// implementation Options registers, and no implementation hides
// outside the list.
func TestSetHelpers_EveryNameImplemented(t *testing.T) {
	t.Parallel()

	names := SetHelperNames()
	for _, name := range names {
		if setHelperImpls[name] == nil {
			t.Errorf("set helper %q has no implementation", name)
		}
		if _, err := expr.Compile(name+`(["a"], ["a"])`, Options()...); err != nil {
			t.Errorf("set helper %q does not compile: %v", name, err)
		}
	}
	if len(setHelperImpls) != len(names) {
		t.Errorf("setHelperImpls has %d entries, SetHelperNames lists %d", len(setHelperImpls), len(names))
	}
}

func closeTo(got, want float64) bool { return math.Abs(got-want) <= 1e-12 }

// topCode is err's outermost coded error's code ("" when uncoded).
func topCode(err error) perr.Code {
	var ce *perr.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}
