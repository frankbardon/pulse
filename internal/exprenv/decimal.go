package exprenv

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/checker/nature"
	"github.com/expr-lang/expr/conf"
	exprtypes "github.com/expr-lang/expr/types"
	exprruntime "github.com/expr-lang/expr/vm/runtime"

	"github.com/frankbardon/pulse/encoding"
)

// Exact decimal arithmetic in expressions.
//
// encoding.Decimal128 carries no scale (the scale is the schema
// field's), so a decimal128 field binds in the env as a [Decimal] — the
// mantissa plus its field's scale ([Bind], declared by [ValueType]) —
// and a patcher rewrites every operator with a decimal operand into an
// exact decimal builtin:
//
//   - `+ - * /` and `< > <= >= == !=` over decimal×decimal or
//     decimal×number (an int / float literal or a numeric field). A
//     number joins as the decimal its shortest spelling reads (1.2 is
//     exactly 1.2); a non-finite one has no decimal value and is an
//     evaluation error in arithmetic (a comparison falls back to
//     float64, so `price < x` stays IEEE for x = +Inf).
//   - Result scale: `+ -` the larger operand scale; `*` the sum (held
//     to 38 by banker's rounding); `/` max(s1, s2, 4)
//     (encoding.MinDecimalScale), banker's rounding. A zero divisor is
//     PULSE_DECIMAL_DIVIDE_BY_ZERO, a result past 38 digits
//     PULSE_DECIMAL_OVERFLOW.
//   - Unary `-` negates; `d ?? n` lifts a numeric default to a decimal
//     so `(price ?? 0) * 2` stays exact; `float(d)` is the explicit
//     escape to float64.
//   - `x == nil` / `x != nil` are never rewritten: nil keeps expr-lang's
//     own rules. Anything else (a string, a bool, `%`) is left alone and
//     fails at COMPILE, exactly as it did.
//
// The null-row twin declares a decimal name through [Untyped] — a
// method-bearing interface, not bare `any` — so the patcher still sees
// it as decimal and patches the same builtins, which dispatch on the
// run-time values: one implementation serves the typed program and the
// twin, so both give the same figure. An expression with no decimal
// operand is never touched (TestDecimal_NonDecimalUntouched), so its
// program — and its cost — is what it always was.
//
// Registered in [Options], so FILTER_EXPRESSION, ATTR_FORMULA (typed and
// twin), total() arguments, OVERLAY_FORMULA and predict's compile all
// see it; overlay cells are float64, so there it never fires.

// Decimal is a decimal128 value as the formula env binds it: the
// mantissa and its scale. It is also the result of every decimal
// operator; ATTR_FORMULA downcasts a Decimal result to its float64
// column ([Decimal.Float64]).
type Decimal struct {
	Value encoding.Decimal128
	Scale uint8
}

// String renders the decimal at its scale (trailing zeros kept).
func (d Decimal) String() string { return d.Value.String(d.Scale) }

// Float64 is the float64 nearest the decimal — correctly rounded from
// its exact spelling, so it never depends on the platform.
func (d Decimal) Float64() float64 {
	f, err := strconv.ParseFloat(d.String(), 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

func (Decimal) exprDecimal() {}

// decimalOperand is the twin's declaration of a decimal name: any
// run-time value (a Decimal, or nil on a null row), still recognisable
// as decimal to the patcher.
type decimalOperand interface{ exprDecimal() }

var (
	exprDecimalType    = reflect.TypeOf(Decimal{})
	exprDecimalAnyType = reflect.TypeOf((*decimalOperand)(nil)).Elem()
	exprBoolType       = reflect.TypeOf(true)
)

// Bind is the env value of field f's decoded value v: a decimal128
// field's encoding.Decimal128 becomes a [Decimal] at the field's scale;
// every other value (and a nil f) passes through unchanged.
func Bind(f *encoding.Field, v any) any {
	if f == nil || f.Type != encoding.FieldTypeDecimal128 {
		return v
	}
	if d, ok := v.(encoding.Decimal128); ok {
		return Decimal{Value: d, Scale: f.Scale}
	}
	return v
}

// reflectDecl declares an env name by reflect.Type (exprtypes.TypeOf
// cannot spell an interface type).
type reflectDecl struct{ t reflect.Type }

func (d reflectDecl) Nature() nature.Nature { return nature.FromType(d.t) }
func (d reflectDecl) Equal(o exprtypes.Type) bool {
	if o == exprtypes.Any {
		return true
	}
	od, ok := o.(reflectDecl)
	return ok && od.t == d.t
}
func (d reflectDecl) String() string { return d.t.String() }

var untypedDecimal exprtypes.Type = reflectDecl{exprDecimalAnyType}

// Untyped is the null-row twin's declaration of a name declared t: a
// decimal stays recognisably decimal (so its operators are patched the
// same way), everything else is `any`.
func Untyped(t exprtypes.Type) exprtypes.Type {
	if t != nil {
		if n := t.Nature(); n.Type == exprDecimalType {
			return untypedDecimal
		}
	}
	return exprtypes.Any
}

// decimalFuncPrefix prefixes every decimal builtin the patcher calls —
// deliberately unspellable-looking, like [ModFuncName].
const decimalFuncPrefix = "__pulse_dec_"

const (
	decFuncFloat = decimalFuncPrefix + "float"
	decFuncLift  = decimalFuncPrefix + "lift"
	decFuncNeg   = decimalFuncPrefix + "neg"
)

// decimalBinary maps each overloaded operator to its builtin and body.
var decimalBinary = map[string]struct {
	name    string
	compare bool
	fn      func(a, b Decimal) (any, error)
	other   func(a, b any) any // the operator over non-decimal values
}{
	"+":  {name: decimalFuncPrefix + "add", fn: decimalAdd, other: exprruntime.Add},
	"-":  {name: decimalFuncPrefix + "sub", fn: decimalSub, other: exprruntime.Subtract},
	"*":  {name: decimalFuncPrefix + "mul", fn: decimalMul, other: exprruntime.Multiply},
	"/":  {name: decimalFuncPrefix + "div", fn: decimalDiv, other: func(a, b any) any { return exprruntime.Divide(a, b) }},
	"==": {name: decimalFuncPrefix + "eq", compare: true, fn: decimalCmp(func(c int) bool { return c == 0 }), other: func(a, b any) any { return exprruntime.Equal(a, b) }},
	"!=": {name: decimalFuncPrefix + "ne", compare: true, fn: decimalCmp(func(c int) bool { return c != 0 }), other: func(a, b any) any { return !exprruntime.Equal(a, b) }},
	"<":  {name: decimalFuncPrefix + "lt", compare: true, fn: decimalCmp(func(c int) bool { return c < 0 }), other: func(a, b any) any { return exprruntime.Less(a, b) }},
	">":  {name: decimalFuncPrefix + "gt", compare: true, fn: decimalCmp(func(c int) bool { return c > 0 }), other: func(a, b any) any { return exprruntime.More(a, b) }},
	"<=": {name: decimalFuncPrefix + "le", compare: true, fn: decimalCmp(func(c int) bool { return c <= 0 }), other: func(a, b any) any { return exprruntime.LessOrEqual(a, b) }},
	">=": {name: decimalFuncPrefix + "ge", compare: true, fn: decimalCmp(func(c int) bool { return c >= 0 }), other: func(a, b any) any { return exprruntime.MoreOrEqual(a, b) }},
}

// decimalOptions registers the decimal builtins and the patcher.
func decimalOptions() []expr.Option {
	opts := make([]expr.Option, 0, len(decimalBinary)+4)
	for op, b := range decimalBinary {
		opts = append(opts, decimalFunction(b.name, decimalBinaryBody(op), decimalBinaryValidate(op, b.compare)))
	}
	opts = append(opts,
		decimalFunction(decFuncFloat, decimalFloatBody, decimalUnaryValidate(func(...decimalKind) reflect.Type { return exprFloat64Type })),
		decimalFunction(decFuncLift, decimalLiftBody, decimalUnaryValidate(decimalResultType)),
		decimalFunction(decFuncNeg, decimalNegBody, decimalUnaryValidate(decimalResultType)),
		// A fresh patcher per compile: its pass state is never shared
		// by two compiles running at once.
		func(c *conf.Config) { c.Visitors = append(c.Visitors, &decimalPatcher{}) },
	)
	return opts
}

// decimalFunction registers fn under name with a Validate (expr.Function
// cannot carry one): the patcher already typed every call it writes, and
// Validate refuses any other shape at compile.
func decimalFunction(name string, fn func(args ...any) (any, error), validate func([]reflect.Type) (reflect.Type, error)) expr.Option {
	return func(c *conf.Config) {
		c.Functions[name] = &builtin.Function{Name: name, Func: fn, Validate: validate}
	}
}

// decimalKind classifies an operand's static type.
type decimalKind int

const (
	decInvalid decimalKind = iota // not numeric, not decimal
	decNumber                     // an int / uint / float kind
	decStatic                     // a Decimal
	decDynamic                    // the twin's decimal declaration
	decUnknown                    // `any`: known only at run time
)

func (k decimalKind) decimal() bool { return k == decStatic || k == decDynamic }

func decimalKindOf(t reflect.Type) decimalKind {
	switch {
	case t == nil:
		return decUnknown
	case t == exprDecimalType:
		return decStatic
	case t == exprDecimalAnyType:
		return decDynamic
	}
	switch t.Kind() {
	case reflect.Interface:
		if t.NumMethod() == 0 {
			return decUnknown
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return decNumber
	}
	return decInvalid
}

// decimalResultType is a decimal operation's static type: a Decimal when
// every operand is known, else the twin's decimal declaration.
func decimalResultType(kinds ...decimalKind) reflect.Type {
	for _, k := range kinds {
		if k == decDynamic || k == decUnknown {
			return exprDecimalAnyType
		}
	}
	return exprDecimalType
}

// decimalBinaryApplies reports whether a binary operator over l and r is
// a decimal operation.
func decimalBinaryApplies(l, r decimalKind) bool {
	return (l.decimal() || r.decimal()) && l != decInvalid && r != decInvalid
}

func decimalBinaryValidate(op string, compare bool) func([]reflect.Type) (reflect.Type, error) {
	return func(args []reflect.Type) (reflect.Type, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("invalid operation: %s (want 2 operands, got %d)", op, len(args))
		}
		l, r := decimalKindOf(args[0]), decimalKindOf(args[1])
		if !decimalBinaryApplies(l, r) {
			return nil, fmt.Errorf("invalid operation: %s (mismatched types %v and %v)", op, args[0], args[1])
		}
		if compare {
			return exprBoolType, nil
		}
		return decimalResultType(l, r), nil
	}
}

func decimalUnaryValidate(result func(...decimalKind) reflect.Type) func([]reflect.Type) (reflect.Type, error) {
	return func(args []reflect.Type) (reflect.Type, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("invalid number of arguments (expected 1, got %d)", len(args))
		}
		k := decimalKindOf(args[0])
		if k == decInvalid {
			return nil, fmt.Errorf("invalid argument (type %v)", args[0])
		}
		return result(k), nil
	}
}

// decimalPatcher rewrites decimal operations into the builtins. It is
// repeatable: a rewrite can type a node the previous pass could not (a
// `let` bound to a decimal result, a `??` whose default it lifted), so
// expr-lang re-checks and walks again until a pass changes nothing.
type decimalPatcher struct{ applied bool }

func (p *decimalPatcher) Reset()             { p.applied = false }
func (p *decimalPatcher) ShouldRepeat() bool { return p.applied }

func (p *decimalPatcher) Visit(node *exprast.Node) {
	switch n := (*node).(type) {
	case *exprast.BinaryNode:
		if n.Operator == "??" {
			l, r := decimalKindOf(n.Left.Type()), decimalKindOf(n.Right.Type())
			if l.decimal() && r == decNumber {
				n.Right = p.call(decFuncLift, decimalResultType(r), n.Right)
			}
			return
		}
		b, ok := decimalBinary[n.Operator]
		if !ok || isNilNode(n.Left) || isNilNode(n.Right) {
			return
		}
		l, r := decimalKindOf(n.Left.Type()), decimalKindOf(n.Right.Type())
		if !decimalBinaryApplies(l, r) {
			return
		}
		t := exprBoolType
		if !b.compare {
			t = decimalResultType(l, r)
		}
		exprast.Patch(node, p.call(b.name, t, n.Left, n.Right))
	case *exprast.UnaryNode:
		if k := decimalKindOf(n.Node.Type()); n.Operator == "-" && k.decimal() {
			exprast.Patch(node, p.call(decFuncNeg, decimalResultType(k), n.Node))
		}
	case *exprast.BuiltinNode:
		if n.Name == "float" && len(n.Arguments) == 1 && decimalKindOf(n.Arguments[0].Type()).decimal() {
			exprast.Patch(node, p.call(decFuncFloat, exprFloat64Type, n.Arguments[0]))
		}
	}
}

func (p *decimalPatcher) call(name string, t reflect.Type, args ...exprast.Node) exprast.Node {
	p.applied = true
	call := &exprast.CallNode{Callee: &exprast.IdentifierNode{Value: name}, Arguments: args}
	call.SetType(t)
	return call
}

func isNilNode(n exprast.Node) bool {
	_, ok := n.(*exprast.NilNode)
	return ok
}

// toDecimal reads a run-time operand as a Decimal: a Decimal as is, a
// finite number as the decimal its shortest spelling reads. ok=false for
// anything else (nil, a string, a non-finite float — err set for the
// last).
func toDecimal(v any) (d Decimal, ok bool, err error) {
	switch x := v.(type) {
	case Decimal:
		return x, true, nil
	case float64:
		return floatDecimal(x, 64)
	case float32:
		return floatDecimal(float64(x), 32)
	case nil:
		return Decimal{}, false, nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Decimal{Value: encoding.NewDecimal128FromInt(rv.Int())}, true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		m, err := encoding.NewDecimal128FromBigInt(new(big.Int).SetUint64(rv.Uint()))
		return Decimal{Value: m}, err == nil, err
	}
	return Decimal{}, false, nil
}

func floatDecimal(f float64, bits int) (Decimal, bool, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Decimal{}, false, fmt.Errorf("a non-finite number (%v) has no decimal value", f)
	}
	m, s, err := encoding.ParseDecimal128(strconv.FormatFloat(f, 'f', -1, bits))
	if err != nil {
		return Decimal{}, false, err
	}
	return Decimal{Value: m, Scale: s}, true, nil
}

// decimalBinaryBody is the run-time body of the op builtin: two operands
// at least one of which is a decimal take the exact path; anything else
// (a nil on a null row, two plain numbers through a dynamic operand) is
// expr-lang's own operator, so the twin answers as the built-in would.
func decimalBinaryBody(op string) func(args ...any) (any, error) {
	b := decimalBinary[op]
	return func(args ...any) (out any, err error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("%s: want 2 operands, got %d", op, len(args))
		}
		_, ad := args[0].(Decimal)
		_, bd := args[1].(Decimal)
		if ad || bd {
			x, xok, xerr := toDecimal(args[0])
			y, yok, yerr := toDecimal(args[1])
			if xok && yok {
				return b.fn(x, y)
			}
			if b.compare && xerr != nil || b.compare && yerr != nil {
				// A comparison with a non-finite number stays IEEE.
				return b.other(decimalFloatOf(args[0]), decimalFloatOf(args[1])), nil
			}
			if xerr != nil {
				return nil, xerr
			}
			if yerr != nil {
				return nil, yerr
			}
			if op == "==" || op == "!=" {
				return b.other(args[0], args[1]), nil // nil / a non-number: never equal
			}
			return nil, fmt.Errorf("invalid operation: %T %s %T", args[0], op, args[1])
		}
		defer func() {
			if rec := recover(); rec != nil {
				out, err = nil, fmt.Errorf("%v", rec)
			}
		}()
		return b.other(args[0], args[1]), nil
	}
}

func decimalFloatOf(v any) any {
	if d, ok := v.(Decimal); ok {
		return d.Float64()
	}
	return v
}

func decimalFloatBody(args ...any) (out any, err error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("float: want 1 argument, got %d", len(args))
	}
	if d, ok := args[0].(Decimal); ok {
		return d.Float64(), nil
	}
	defer func() {
		if rec := recover(); rec != nil {
			out, err = nil, fmt.Errorf("%v", rec)
		}
	}()
	return builtin.Float(args[0]), nil
}

func decimalLiftBody(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("decimal lift: want 1 argument, got %d", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	d, ok, err := toDecimal(args[0])
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("invalid operation: %T has no decimal value", args[0])
	}
	return d, nil
}

func decimalNegBody(args ...any) (out any, err error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("-: want 1 operand, got %d", len(args))
	}
	if d, ok := args[0].(Decimal); ok {
		m, err := encoding.NewDecimal128FromBigInt(new(big.Int).Neg(d.Value.Mantissa()))
		if err != nil {
			return nil, err
		}
		return Decimal{Value: m, Scale: d.Scale}, nil
	}
	defer func() {
		if rec := recover(); rec != nil {
			out, err = nil, fmt.Errorf("%v", rec)
		}
	}()
	return exprruntime.Negate(args[0]), nil
}

// decimalPow10 caches 10^n for every scale shift two decimals can need.
var decimalPow10 = func() [2*encoding.MaxDecimalPrecision + 1]*big.Int {
	var t [2*encoding.MaxDecimalPrecision + 1]*big.Int
	for i := range t {
		t[i] = new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(i)), nil)
	}
	return t
}()

// aligned returns both mantissas at the larger scale, exact (no range
// check: a comparison or a sum checks its own result).
func aligned(a, b Decimal) (x, y *big.Int, scale uint8) {
	x, y = a.Value.Mantissa(), b.Value.Mantissa()
	scale = max(a.Scale, b.Scale)
	if a.Scale < scale {
		x.Mul(x, decimalPow10[scale-a.Scale])
	}
	if b.Scale < scale {
		y.Mul(y, decimalPow10[scale-b.Scale])
	}
	return x, y, scale
}

func decimalOf(m *big.Int, scale uint8) (any, error) {
	d, err := encoding.NewDecimal128FromBigInt(m)
	if err != nil {
		return nil, err
	}
	return Decimal{Value: d, Scale: scale}, nil
}

func decimalAdd(a, b Decimal) (any, error) {
	x, y, s := aligned(a, b)
	return decimalOf(x.Add(x, y), s)
}

func decimalSub(a, b Decimal) (any, error) {
	x, y, s := aligned(a, b)
	return decimalOf(x.Sub(x, y), s)
}

func decimalMul(a, b Decimal) (any, error) {
	m := new(big.Int).Mul(a.Value.Mantissa(), b.Value.Mantissa())
	s := int(a.Scale) + int(b.Scale)
	if s > encoding.MaxDecimalPrecision {
		m = roundHalfEven(m, decimalPow10[s-encoding.MaxDecimalPrecision])
		s = encoding.MaxDecimalPrecision
	}
	return decimalOf(m, uint8(s))
}

func decimalDiv(a, b Decimal) (any, error) {
	rs := max(a.Scale, b.Scale, uint8(encoding.MinDecimalScale))
	q, err := a.Value.Div(b.Value, a.Scale, b.Scale, rs)
	if err != nil {
		return nil, err
	}
	return Decimal{Value: q, Scale: rs}, nil
}

func decimalCmp(verdict func(int) bool) func(a, b Decimal) (any, error) {
	return func(a, b Decimal) (any, error) {
		x, y, _ := aligned(a, b)
		return verdict(x.Cmp(y)), nil
	}
}

// roundHalfEven divides m by d (a power of ten), banker's rounding.
func roundHalfEven(m, d *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(m, d, new(big.Int))
	if r.Sign() == 0 {
		return q
	}
	c := new(big.Int).Lsh(new(big.Int).Abs(r), 1).Cmp(d)
	if c > 0 || c == 0 && q.Bit(0) == 1 {
		if r.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	return q
}
