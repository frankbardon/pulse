package exprenv

import (
	"errors"
	"strings"
	"testing"
)

// Direct unit coverage for reflectiveTrampoline + Func: the embedder
// paths flow through the runtime processor in internal/processing, but
// the trampoline has branches (variadic, multi-return, error, non-func,
// type conversion, nil arg) that are easier to exercise here.

func TestReflectiveTrampoline_NonFunction(t *testing.T) {
	t.Parallel()

	fn := reflectiveTrampoline("not-a-function")
	_, err := fn()
	if err == nil {
		t.Fatal("expected non-function value to surface PROCESSING_RUNTIME error")
	}
	if !strings.Contains(err.Error(), "callable function") {
		t.Errorf("err = %v; expected callable-function message", err)
	}
}

func TestReflectiveTrampoline_TypedFunction(t *testing.T) {
	t.Parallel()

	fn := reflectiveTrampoline(func(v float64) float64 { return v * 3 })
	got, err := fn(5.0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 15.0 {
		t.Errorf("got %v, want 15.0", got)
	}
}

func TestReflectiveTrampoline_ArityMismatch(t *testing.T) {
	t.Parallel()

	fn := reflectiveTrampoline(func(a, b int) int { return a + b })
	if _, err := fn(1); err == nil {
		t.Error("expected arity mismatch error for 1 arg to 2-arity fn")
	}
}

func TestReflectiveTrampoline_VariadicAcceptsMore(t *testing.T) {
	t.Parallel()

	sum := func(vs ...float64) float64 {
		var s float64
		for _, v := range vs {
			s += v
		}
		return s
	}
	fn := reflectiveTrampoline(sum)
	got, err := fn(1.0, 2.0, 3.0, 4.0)
	if err != nil {
		t.Fatalf("variadic call: %v", err)
	}
	if got != 10.0 {
		t.Errorf("variadic sum = %v, want 10", got)
	}
}

func TestReflectiveTrampoline_VariadicAcceptsZero(t *testing.T) {
	t.Parallel()

	count := func(vs ...float64) int { return len(vs) }
	fn := reflectiveTrampoline(count)
	got, err := fn()
	if err != nil {
		t.Fatalf("variadic 0-args: %v", err)
	}
	if got != 0 {
		t.Errorf("variadic count = %v, want 0", got)
	}
}

func TestReflectiveTrampoline_TypeConversionFailure(t *testing.T) {
	t.Parallel()

	fn := reflectiveTrampoline(func(v float64) float64 { return v })
	// Pass a struct value that cannot convert to float64.
	if _, err := fn(struct{}{}); err == nil {
		t.Error("expected conversion failure error")
	}
}

func TestReflectiveTrampoline_NilArgZeroesTarget(t *testing.T) {
	t.Parallel()

	fn := reflectiveTrampoline(func(s string) int { return len(s) })
	got, err := fn(nil)
	if err != nil {
		t.Fatalf("nil arg: %v", err)
	}
	if got != 0 {
		t.Errorf("nil-arg result = %v, want 0", got)
	}
}

func TestReflectiveTrampoline_NoReturn(t *testing.T) {
	t.Parallel()

	var called bool
	fn := reflectiveTrampoline(func() { called = true })
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
	t.Parallel()

	sentinel := errors.New("boom")
	fn := reflectiveTrampoline(func() (float64, error) { return 0, sentinel })
	_, err := fn()
	if err == nil {
		t.Fatal("expected error to propagate from 2-return fn")
	}
	if !errors.Is(err, sentinel) && err.Error() != sentinel.Error() {
		t.Errorf("err = %v; want sentinel", err)
	}
}

func TestReflectiveTrampoline_TwoReturnsNoError(t *testing.T) {
	t.Parallel()

	fn := reflectiveTrampoline(func() (float64, error) { return 7, nil })
	got, err := fn()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 7.0 {
		t.Errorf("got %v, want 7.0", got)
	}
}

func TestReflectiveTrampoline_TooManyReturns(t *testing.T) {
	t.Parallel()

	fn := reflectiveTrampoline(func() (int, int, int) { return 1, 2, 3 })
	if _, err := fn(); err == nil {
		t.Error("expected 3-return signature to be rejected")
	}
}

func TestAsExprFunc_CanonicalDynamicShapePassThrough(t *testing.T) {
	t.Parallel()

	dynamic := func(args ...any) (any, error) {
		if len(args) == 0 {
			return nil, nil
		}
		return args[0], nil
	}
	got := Func(dynamic)
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
	t.Parallel()

	got := Func(func(s string) string { return s + "!" })
	v, err := got("hi")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if v != "hi!" {
		t.Errorf("got %v, want hi!", v)
	}
}
