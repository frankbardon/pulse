package processing

import (
	"fmt"
	"math"
	"reflect"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	exprruntime "github.com/expr-lang/expr/vm/runtime"
)

// `%` over Pulse fields.
//
// expr-lang's `%` is integer-only, and every numeric Pulse field (u*,
// f*, date, packed_bool) binds as float64 — so `x % 2` on a field could
// never compile ("mismatched types float64 and int"), and the guarded
// `(x ?? 0) % 2` compiled but failed on every row at run time.
//
// exprModPatcher rewrites a `%` whose operands are numeric but NOT both
// integers into a call to the float modulo builtin. Semantics:
//
//   - int % int is untouched — expr-lang's own truncated integer modulo,
//     and `n % 0` stays its "integer divide by zero" evaluation error.
//   - Any other numeric pair is math.Mod(a, b): truncated, the result
//     takes the sign of the DIVIDEND (-7 % 3 == -1, 7.5 % 2 == 1.5),
//     the same sign rule as Go's integer `%`.
//   - A zero divisor yields NaN, never an error — IEEE, exactly as `/`
//     already yields ±Inf / NaN for a zero divisor in these expressions.
//   - An operand typed `any` (the untyped null-row twin, a `??`
//     coalesce) is patched too and dispatched at run time: two integers
//     take the integer path, two numbers the float path, anything else
//     (nil, a string, a decimal) raises the evaluation error the
//     built-in `%` would have.
//   - A statically non-numeric operand (a categorical label, a
//     decimal128) is left alone, so it keeps failing at COMPILE time.
//
// The patcher is registered in setExprOptions, so FILTER_EXPRESSION,
// ATTR_FORMULA (typed program and untyped twin alike) and — through
// compileFormulaProgramWithExtensions — OVERLAY_FORMULA all see it. It
// is a compile-time AST rewrite: the purity walk in filter_precompute
// parses the raw source, where `%` is still a pure BinaryNode.

// exprModFuncName is the builtin `%` is rewritten into. Deliberately
// unspellable-looking so no embedder ExprFunction collides with it.
const exprModFuncName = "__pulse_mod"

// exprModOptions returns the `%` patcher and its builtin.
func exprModOptions() []expr.Option {
	return []expr.Option{
		expr.Function(exprModFuncName, exprModulo),
		expr.Patch(exprModPatcher{}),
	}
}

var exprAnyType = reflect.TypeOf((*any)(nil)).Elem()

type exprModPatcher struct{}

func (exprModPatcher) Visit(node *exprast.Node) {
	b, ok := (*node).(*exprast.BinaryNode)
	if !ok || b.Operator != "%" {
		return
	}
	l, r := b.Left.Type(), b.Right.Type()
	lk, rk := exprModOperandKind(l), exprModOperandKind(r)
	if lk == exprModInvalid || rk == exprModInvalid {
		return // non-numeric: keep the built-in compile error
	}
	if lk == exprModInt && rk == exprModInt {
		return // expr-lang's integer modulo
	}
	call := &exprast.CallNode{
		Callee:    &exprast.IdentifierNode{Value: exprModFuncName},
		Arguments: []exprast.Node{b.Left, b.Right},
	}
	if lk == exprModDynamic || rk == exprModDynamic {
		call.SetType(exprAnyType)
	} else {
		call.SetType(exprFloat64Type)
	}
	exprast.Patch(node, call)
}

type exprModKind int

const (
	exprModInvalid exprModKind = iota
	exprModInt
	exprModFloat
	exprModDynamic
)

func exprModOperandKind(t reflect.Type) exprModKind {
	if t == nil {
		return exprModDynamic
	}
	switch t.Kind() {
	case reflect.Interface:
		return exprModDynamic
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return exprModInt
	case reflect.Float32, reflect.Float64:
		return exprModFloat
	}
	return exprModInvalid
}

// exprModulo is the run-time body of a rewritten `%`.
func exprModulo(params ...any) (out any, err error) {
	if len(params) != 2 {
		return nil, fmt.Errorf("%%: want 2 operands, got %d", len(params))
	}
	a, b := params[0], params[1]
	ka, kb := exprModValueKind(a), exprModValueKind(b)
	switch {
	case ka == exprModInt && kb == exprModInt:
		defer func() {
			if rec := recover(); rec != nil {
				out, err = nil, fmt.Errorf("%v", rec)
			}
		}()
		return exprruntime.Modulo(a, b), nil
	case ka != exprModInvalid && kb != exprModInvalid:
		return math.Mod(exprModFloat64(a), exprModFloat64(b)), nil
	}
	return nil, fmt.Errorf("invalid operation: %T %% %T", a, b)
}

func exprModValueKind(v any) exprModKind {
	if v == nil {
		return exprModInvalid
	}
	k := exprModOperandKind(reflect.TypeOf(v))
	if k == exprModDynamic {
		return exprModInvalid
	}
	return k
}

func exprModFloat64(v any) float64 {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint())
	}
	return float64(rv.Int())
}
