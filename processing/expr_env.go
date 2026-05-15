package processing

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/errors"
)

// reflectiveExprTrampoline wraps an arbitrary Go function value
// behind the dynamic `func(args ...any) (any, error)` shape required
// by expr-lang's expr.Function helper. Argument types are coerced
// via reflect.Value.Convert; numeric kinds are bridged through
// float64 to match expr-lang's evaluation defaults. Embedders that
// want zero coercion overhead can supply the canonical shape
// directly.
func reflectiveExprTrampoline(fn any) func(args ...any) (any, error) {
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

// ExprOptions returns the expr-lang Option slice an ExtensionRegistry
// contributes to ATTR_FORMULA / FILTER_EXPRESSION compilation. A nil
// receiver and a registry with no expression-side entries both return
// nil — the caller's base options stay untouched.
//
// Two contributions:
//   - One expr.Function per ExprFunctions entry, registered under its
//     declared Name.
//   - The built-in `lookup(table, keys...)` function when at least one
//     LookupTable is registered. The function resolves keys against
//     the named table and returns a float64 on hit; PULSE_LOOKUP_MISS
//     on miss; PULSE_LOOKUP_TABLE_UNKNOWN when the table is not
//     registered.
//
// Expressions in cohorts with no lookup tables registered still
// reference `lookup` legally — the function returns
// PULSE_LOOKUP_TABLE_UNKNOWN at evaluation time. The compile-time
// typecheck (E8) catches static misuse.
func (r *ExtensionRegistry) ExprOptions() []expr.Option {
	if r == nil {
		return nil
	}
	if len(r.ExprFunctions) == 0 && len(r.LookupTables) == 0 {
		return nil
	}
	out := make([]expr.Option, 0, len(r.ExprFunctions)+1)
	for _, fn := range r.ExprFunctions {
		if fn.Fn == nil {
			continue
		}
		// expr-lang's expr.Function accepts any callable: typed
		// `func(v float64) float64` is reflected at compile time
		// just like the canonical `func(...any) (any, error)`
		// shape. Embedders pick whichever signature their domain
		// helpers already use.
		out = append(out, expr.Function(fn.Name, asExprFunc(fn.Fn)))
	}
	if len(r.LookupTables) > 0 {
		out = append(out, expr.Function("lookup", r.lookupBuiltin))
	}
	return out
}

// asExprFunc bridges an embedder-supplied function to the dynamic
// shape `func(args ...any) (any, error)` that expr-lang's
// expr.Function helper expects. Embedders supplying the canonical
// shape pay no overhead; typed functions go through a one-shot
// reflective trampoline. Any non-function value surfaces as a
// PROCESSING_RUNTIME error at call time.
func asExprFunc(fn any) func(args ...any) (any, error) {
	if dynamic, ok := fn.(func(args ...any) (any, error)); ok {
		return dynamic
	}
	if typed, ok := fn.(func(...any) (any, error)); ok {
		return typed
	}
	return reflectiveExprTrampoline(fn)
}

// lookupBuiltin is the closure registered as the `lookup` function in
// the expr environment whenever any LookupTables are configured. It
// expects (table_name, key1, key2, ...) all as strings or values that
// can be stringified.
func (r *ExtensionRegistry) lookupBuiltin(args ...any) (any, error) {
	if len(args) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			"lookup() requires at least 1 argument (table name)")
	}
	tableName, ok := args[0].(string)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("lookup() table argument must be string, got %T", args[0]))
	}
	table, ok := r.LookupTables[tableName]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_LOOKUP_TABLE_UNKNOWN,
			fmt.Sprintf("lookup table %q is not registered", tableName),
			map[string]any{"table": tableName},
		)
	}
	keys := make([]string, 0, len(args)-1)
	for _, a := range args[1:] {
		keys = append(keys, fmt.Sprintf("%v", a))
	}
	value, found, err := resolveLookup(table, keys)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_LOOKUP_MISS,
			fmt.Sprintf("lookup(%q, %s) failed", tableName, strings.Join(keys, ", ")))
	}
	if !found {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_LOOKUP_MISS,
			fmt.Sprintf("lookup(%q, %s) returned no result", tableName, strings.Join(keys, ", ")),
			map[string]any{"table": tableName, "keys": keys},
		)
	}
	return value, nil
}

// resolveLookup dispatches to whichever accessor the table provided.
// Rows-backed tables join the keys with "|" before indexing; embedders
// can override the join policy by switching to the Lookup func.
func resolveLookup(table LookupTable, keys []string) (float64, bool, error) {
	if table.Lookup != nil {
		return table.Lookup(keys...)
	}
	joined := strings.Join(keys, "|")
	v, ok := table.Rows[joined]
	return v, ok, nil
}
