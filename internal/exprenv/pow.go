package exprenv

import (
	"fmt"
	"reflect"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	exprruntime "github.com/expr-lang/expr/vm/runtime"

	"github.com/frankbardon/pulse/internal/vecmath"
)

// `**` / `^` on detmath.
//
// expr-lang's own power operator evaluates through runtime.Exponent,
// i.e. math.Pow, whose bits depend on the machine (amd64 math.Exp picks
// an FMA path per CPU, arm64 contracts the pure-Go multiply-adds). The
// named pow() runs on internal/detmath through vecmath, so `x ** 2` and
// pow(x, 2) gave different bits on one machine and `x ** 2` different
// bits across machines.
//
// exprPowPatcher rewrites every `**` / `^` the type check ACCEPTED into
// a call to the power builtin, backed by vecmath.Pow (detmath.Pow), so
// the operator and pow() give identical bits everywhere. Semantics are
// otherwise expr-lang's, exactly:
//
//   - The result is always float64 (int ** int too), as
//     runtime.Exponent's is; the call is declared float64, so a typed
//     program checks identically (`x ** 2 + "a"` stays a compile error).
//   - Operands convert with runtime.ToFloat64, the conversion
//     runtime.Exponent uses, so a nil (the untyped null-row twin), a
//     string or a decimal operand raises the same "invalid operation:
//     float(<nil>)" evaluation error at the same location, and
//     FILTER_EXPRESSION's UNKNOWN rule sees what it saw before (pow()
//     refuses a nil with its own coded argument error instead).
//   - A `**` the checker refused (a categorical label, a decimal128, a
//     vector) has no type and is left alone, so it keeps failing at
//     COMPILE time with expr-lang's own message.
//
// The patcher runs in the repeatable pass, after the decimal patcher
// (decimal.go), so it judges operands by their final types. It is
// registered in [Options], so FILTER_EXPRESSION, ATTR_FORMULA (typed
// program and untyped twin), total() arguments, holdout, OVERLAY_FORMULA
// and predict's compile all see it. The purity walk in
// internal/processing's filter_precompute parses the raw source, where
// `**` is still a pure BinaryNode.

// PowFuncName is the builtin `**` is rewritten into, and PowCaretFuncName
// the one `^` is (the spelling only shapes a compile error's text).
// Deliberately unspellable-looking, like [ModFuncName].
const (
	PowFuncName      = "__pulse_pow"
	PowCaretFuncName = "__pulse_pow_caret"
)

// exprPowOptions returns the `**` / `^` patcher and its builtins.
func exprPowOptions() []expr.Option {
	return []expr.Option{
		decimalFunction(PowFuncName, exprPower, exprPowValidate("**")),
		decimalFunction(PowCaretFuncName, exprPower, exprPowValidate("^")),
		expr.Patch(&exprPowPatcher{}),
	}
}

// exprPowPatcher is repeatable (Reset / ShouldRepeat) so expr-lang runs
// it in the second pass, after every one-shot patcher and beside the
// decimal patcher registered before it. It never asks for another pass.
type exprPowPatcher struct{}

func (*exprPowPatcher) Reset() {}

func (*exprPowPatcher) ShouldRepeat() bool { return false }

func (*exprPowPatcher) Visit(node *exprast.Node) {
	b, ok := (*node).(*exprast.BinaryNode)
	if !ok {
		return
	}
	name := PowFuncName
	switch b.Operator {
	case "**":
	case "^":
		name = PowCaretFuncName
	default:
		return
	}
	if b.Type() != exprFloat64Type {
		return // the check refused it: keep expr-lang's compile error
	}
	call := &exprast.CallNode{
		Callee:    &exprast.IdentifierNode{Value: name},
		Arguments: []exprast.Node{b.Left, b.Right},
	}
	call.SetType(exprFloat64Type)
	exprast.Patch(node, call)
}

// exprPowValidate declares the call float64 and, should an operand's
// type ever move after the rewrite, refuses it with the checker's own
// text for op.
func exprPowValidate(op string) func([]reflect.Type) (reflect.Type, error) {
	return func(args []reflect.Type) (reflect.Type, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("%s: want 2 operands, got %d", op, len(args))
		}
		if !exprPowOperand(args[0]) || !exprPowOperand(args[1]) {
			return nil, fmt.Errorf("invalid operation: %s (mismatched types %s and %s)", op, args[0], args[1])
		}
		return exprFloat64Type, nil
	}
}

func exprPowOperand(t reflect.Type) bool {
	if t == nil || t.Kind() == reflect.Interface {
		return true
	}
	return exprModOperandKind(t) != exprModInvalid
}

// exprPower is the run-time body of a rewritten `**` / `^`:
// runtime.Exponent with detmath's Pow.
func exprPower(params ...any) (any, error) {
	if len(params) != 2 {
		return nil, fmt.Errorf("**: want 2 operands, got %d", len(params))
	}
	// ToFloat64 panics on a non-number exactly as runtime.Exponent does;
	// the VM recovers it into the same evaluation error.
	return vecmath.Pow(exprruntime.ToFloat64(params[0]), exprruntime.ToFloat64(params[1])), nil
}
