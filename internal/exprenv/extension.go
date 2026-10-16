package exprenv

import (
	"fmt"
	"reflect"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/errors"
)

// NamedFunc is one embedder expression function (pulse.ExprFunction):
// its registered name and its Go value (any callable).
type NamedFunc struct {
	Name string
	Fn   any
}

// ExtensionFunctions returns one expr.Function per entry with a non-nil
// Fn, in order, each bridged through [Func]. Registered after [Options],
// so an embedder function shadows an unreserved helper of its name
// (the reserved names are refused earlier, at pulse.New).
func ExtensionFunctions(fns []NamedFunc) []expr.Option {
	var out []expr.Option
	for _, fn := range fns {
		if fn.Fn == nil {
			continue
		}
		// expr-lang's expr.Function accepts any callable: typed
		// `func(v float64) float64` is reflected at compile time
		// just like the canonical `func(...any) (any, error)`
		// shape. Embedders pick whichever signature their domain
		// helpers already use.
		out = append(out, expr.Function(fn.Name, Func(fn.Fn)))
	}
	return out
}

// Func bridges an embedder-supplied function to the dynamic shape
// `func(args ...any) (any, error)` that expr-lang's expr.Function
// helper expects. Embedders supplying the canonical shape pay no
// overhead; typed functions go through a one-shot reflective
// trampoline. Any non-function value surfaces as a PROCESSING_RUNTIME
// error at call time.
func Func(fn any) func(args ...any) (any, error) {
	if dynamic, ok := fn.(func(args ...any) (any, error)); ok {
		return dynamic
	}
	if typed, ok := fn.(func(...any) (any, error)); ok {
		return typed
	}
	return reflectiveTrampoline(fn)
}

// reflectiveTrampoline wraps an arbitrary Go function value
// behind the dynamic `func(args ...any) (any, error)` shape required
// by expr-lang's expr.Function helper. Argument types are coerced
// via reflect.Value.Convert; numeric kinds are bridged through
// float64 to match expr-lang's evaluation defaults. Embedders that
// want zero coercion overhead can supply the canonical shape
// directly.
func reflectiveTrampoline(fn any) func(args ...any) (any, error) {
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return func(args ...any) (any, error) {
			_ = args
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("expr function value of type %T is not a callable function", fn))
		}
	}
	rt := rv.Type()
	return func(args ...any) (any, error) {
		expectedArity := rt.NumIn()
		if rt.IsVariadic() {
			expectedArity = rt.NumIn() - 1
		}
		if !rt.IsVariadic() && len(args) != expectedArity {
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("expr function arity mismatch: got %d args, want %d", len(args), expectedArity))
		}
		in := make([]reflect.Value, len(args))
		for i, a := range args {
			var targetType reflect.Type
			if rt.IsVariadic() && i >= expectedArity {
				targetType = rt.In(rt.NumIn() - 1).Elem()
			} else {
				targetType = rt.In(i)
			}
			val := reflect.ValueOf(a)
			if !val.IsValid() {
				in[i] = reflect.Zero(targetType)
				continue
			}
			if val.Type().AssignableTo(targetType) {
				in[i] = val
				continue
			}
			if val.Type().ConvertibleTo(targetType) {
				in[i] = val.Convert(targetType)
				continue
			}
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("expr function arg %d: cannot convert %T to %s", i, a, targetType))
		}
		out := rv.Call(in)
		switch len(out) {
		case 0:
			return nil, nil
		case 1:
			return out[0].Interface(), nil
		case 2:
			var err error
			if e, ok := out[1].Interface().(error); ok {
				err = e
			}
			return out[0].Interface(), err
		default:
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("expr function returns %d values; expected 1 (value) or 2 (value, error)", len(out)))
		}
	}
}
