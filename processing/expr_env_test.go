package processing

import (
	"errors"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// Direct unit coverage for reflectiveExprTrampoline + asExprFunc +
// lookupBuiltin. The exp_env paths flow through the runtime processor
// in extensions_expr_test.go, but the trampoline + builtin have
// branches (variadic, multi-return, error, non-func, type conversion,
// nil arg) that are easier to exercise here.

// ---- reflectiveExprTrampoline -----------------------------------------

func TestReflectiveTrampoline_NonFunction(t *testing.T) {
	fn := reflectiveExprTrampoline("not-a-function")
	_, err := fn()
	if err == nil {
		t.Fatal("expected non-function value to surface PROCESSING_RUNTIME error")
	}
	if !strings.Contains(err.Error(), "callable function") {
		t.Errorf("err = %v; expected callable-function message", err)
	}
}

func TestReflectiveTrampoline_TypedFunction(t *testing.T) {
	fn := reflectiveExprTrampoline(func(v float64) float64 { return v * 3 })
	got, err := fn(5.0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 15.0 {
		t.Errorf("got %v, want 15.0", got)
	}
}

func TestReflectiveTrampoline_ArityMismatch(t *testing.T) {
	fn := reflectiveExprTrampoline(func(a, b int) int { return a + b })
	if _, err := fn(1); err == nil {
		t.Error("expected arity mismatch error for 1 arg to 2-arity fn")
	}
}

func TestReflectiveTrampoline_VariadicAcceptsMore(t *testing.T) {
	sum := func(vs ...float64) float64 {
		var s float64
		for _, v := range vs {
			s += v
		}
		return s
	}
	fn := reflectiveExprTrampoline(sum)
	got, err := fn(1.0, 2.0, 3.0, 4.0)
	if err != nil {
		t.Fatalf("variadic call: %v", err)
	}
	if got != 10.0 {
		t.Errorf("variadic sum = %v, want 10", got)
	}
}

func TestReflectiveTrampoline_VariadicAcceptsZero(t *testing.T) {
	count := func(vs ...float64) int { return len(vs) }
	fn := reflectiveExprTrampoline(count)
	got, err := fn()
	if err != nil {
		t.Fatalf("variadic 0-args: %v", err)
	}
	if got != 0 {
		t.Errorf("variadic count = %v, want 0", got)
	}
}

func TestReflectiveTrampoline_TypeConversionFailure(t *testing.T) {
	fn := reflectiveExprTrampoline(func(v float64) float64 { return v })
	// Pass a struct value that cannot convert to float64.
	if _, err := fn(struct{}{}); err == nil {
		t.Error("expected conversion failure error")
	}
}

func TestReflectiveTrampoline_NilArgZeroesTarget(t *testing.T) {
	fn := reflectiveExprTrampoline(func(s string) int { return len(s) })
	got, err := fn(nil)
	if err != nil {
		t.Fatalf("nil arg: %v", err)
	}
	if got != 0 {
		t.Errorf("nil-arg result = %v, want 0", got)
	}
}

func TestReflectiveTrampoline_NoReturn(t *testing.T) {
	var called bool
	fn := reflectiveExprTrampoline(func() { called = true })
	got, err := fn()
	if err != nil {
		t.Fatalf("no-return: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if !called {
		t.Error("function was not invoked")
	}
}

func TestReflectiveTrampoline_TwoReturnsErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	fn := reflectiveExprTrampoline(func() (float64, error) { return 0, sentinel })
	_, err := fn()
	if err == nil {
		t.Fatal("expected error to propagate from 2-return fn")
	}
	if !errors.Is(err, sentinel) && err.Error() != sentinel.Error() {
		t.Errorf("err = %v; want sentinel", err)
	}
}

func TestReflectiveTrampoline_TwoReturnsNoError(t *testing.T) {
	fn := reflectiveExprTrampoline(func() (float64, error) { return 7, nil })
	got, err := fn()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 7.0 {
		t.Errorf("got %v, want 7.0", got)
	}
}

func TestReflectiveTrampoline_TooManyReturns(t *testing.T) {
	fn := reflectiveExprTrampoline(func() (int, int, int) { return 1, 2, 3 })
	if _, err := fn(); err == nil {
		t.Error("expected 3-return signature to be rejected")
	}
}

// ---- asExprFunc -------------------------------------------------------

func TestAsExprFunc_CanonicalDynamicShapePassThrough(t *testing.T) {
	dynamic := func(args ...any) (any, error) {
		if len(args) == 0 {
			return nil, nil
		}
		return args[0], nil
	}
	got := asExprFunc(dynamic)
	// Calling through must not trip the trampoline (typed-coerce path).
	v, err := got("hello")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v != "hello" {
		t.Errorf("got %v want hello", v)
	}
}

func TestAsExprFunc_TypedFunctionViaTrampoline(t *testing.T) {
	got := asExprFunc(func(s string) string { return s + "!" })
	v, err := got("hi")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v != "hi!" {
		t.Errorf("got %v, want hi!", v)
	}
}

// ---- lookupBuiltin ----------------------------------------------------

func TestLookupBuiltin_NoArgsError(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin()
	if err == nil {
		t.Fatal("expected error on 0-arg lookup call")
	}
	if !strings.Contains(err.Error(), "table name") {
		t.Errorf("err = %v; expected 'table name' in message", err)
	}
}

func TestLookupBuiltin_NonStringTableError(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin(42, "k")
	if err == nil {
		t.Fatal("expected error on non-string table arg")
	}
}

func TestLookupBuiltin_UnknownTable(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin("notthere", "k")
	if !perr.HasCode(err, perr.PULSE_LOOKUP_TABLE_UNKNOWN) {
		t.Errorf("err = %v; expected PULSE_LOOKUP_TABLE_UNKNOWN", err)
	}
}

func TestLookupBuiltin_Miss(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	_, err := r.lookupBuiltin("t", "y")
	if !perr.HasCode(err, perr.PULSE_LOOKUP_MISS) {
		t.Errorf("err = %v; expected PULSE_LOOKUP_MISS", err)
	}
}

func TestLookupBuiltin_Hit(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 9}}},
	}
	v, err := r.lookupBuiltin("t", "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v != 9.0 {
		t.Errorf("got %v, want 9.0", v)
	}
}

func TestLookupBuiltin_FuncBackedError(t *testing.T) {
	sentinel := errors.New("table broke")
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{
			"f": {Lookup: func(keys ...string) (float64, bool, error) {
				return 0, false, sentinel
			}},
		},
	}
	_, err := r.lookupBuiltin("f", "x")
	if !perr.HasCode(err, perr.PULSE_LOOKUP_MISS) {
		t.Errorf("err = %v; expected PULSE_LOOKUP_MISS wrap", err)
	}
}

// ---- ExprOptions ------------------------------------------------------

func TestExprOptions_NilRegistry(t *testing.T) {
	var r *ExtensionRegistry
	if opts := r.ExprOptions(); opts != nil {
		t.Errorf("nil registry must yield nil ExprOptions, got %v", opts)
	}
}

func TestExprOptions_NoEntries(t *testing.T) {
	r := &ExtensionRegistry{}
	if opts := r.ExprOptions(); opts != nil {
		t.Errorf("empty registry must yield nil ExprOptions, got %v", opts)
	}
}

func TestExprOptions_SkipsNilFn(t *testing.T) {
	r := &ExtensionRegistry{
		ExprFunctions: []ExprFunction{
			{Name: "good", Fn: func() float64 { return 1 }},
			{Name: "bad", Fn: nil},
		},
	}
	opts := r.ExprOptions()
	if len(opts) != 1 {
		t.Errorf("expected 1 option (nil Fn skipped), got %d", len(opts))
	}
}

func TestExprOptions_LookupBuiltinAddedWhenTablesPresent(t *testing.T) {
	r := &ExtensionRegistry{
		LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"x": 1}}},
	}
	opts := r.ExprOptions()
	if len(opts) == 0 {
		t.Fatal("expected lookup() option when LookupTables registered")
	}
}
