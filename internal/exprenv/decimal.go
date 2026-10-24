package exprenv

import (
	stderrors "errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/checker"
	"github.com/expr-lang/expr/checker/nature"
	"github.com/expr-lang/expr/conf"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser"
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
//   - Unary `-` negates; `d ?? n` and a ternary `c ? d : n` lift the
//     number (or untyped) side to a decimal, so `(price ?? 0) * 2` and
//     `(c ? price : 0) * 2` stay exact; `float(d)` is the explicit
//     escape to float64.
//   - The numeric built-ins (decimal_builtins.go): abs, max, min, sum,
//     mean, median, sort, sortBy and `in` are exact; ceil / floor /
//     round answer an integral decimal (round half-even, as every
//     decimal rescale); int truncates; the float-domain built-ins (sqrt,
//     log, exp, pow, the vector family) read the decimal's float64.
//   - Once an expression holds a decimal, an operand typed `any` (a
//     decimal laundered through a map / mixed array literal, an untyped
//     `??`) is routed through the same builtins, which fall back to
//     expr-lang's own operator for non-decimal values — so nothing the
//     compile accepts fails at run time for a decimal type reason.
//   - `x == nil` / `x != nil` are never rewritten: nil keeps expr-lang's
//     own rules. A string or bool operand is left alone and fails at
//     COMPILE, exactly as it did; `%` over a decimal is refused at
//     COMPILE with a reason naming float().
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

// MarshalJSON writes the decimal as a JSON number at its scale (toJSON
// in an expression reads 3.60, not the struct).
func (d Decimal) MarshalJSON() ([]byte, error) { return []byte(d.String()), nil }

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
// a dictionary code (float64) on a dictionary-bearing categorical — a
// derived column a dictionary-inheritance view declares ([DerivedFields]),
// whose value rides the record as its code — becomes its label, as
// Record.AllValues resolves a schema categorical; every other value
// (and a nil f) passes through unchanged.
func Bind(f *encoding.Field, v any) any {
	if f == nil {
		return v
	}
	switch {
	case f.Type == encoding.FieldTypeDecimal128:
		if d, ok := v.(encoding.Decimal128); ok {
			return Decimal{Value: d, Scale: f.Scale}
		}
	case f.Type.IsCategorical() && f.Dictionary != nil:
		if code, ok := v.(float64); ok {
			return f.Dictionary.Resolve(uint32(code))
		}
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
// same way), a u64 recognisably u64 (u64.go), everything else is `any`.
func Untyped(t exprtypes.Type) exprtypes.Type {
	if t != nil {
		switch t.Nature().Type {
		case exprDecimalType:
			return untypedDecimal
		case exprU64Type:
			return untypedU64
		}
	}
	return exprtypes.Any
}

// decimalFuncPrefix prefixes every decimal builtin the patcher calls —
// deliberately unspellable-looking, like [ModFuncName].
const decimalFuncPrefix = "__pulse_dec_"

const (
	decFuncFloat   = decimalFuncPrefix + "float"
	decFuncLift    = decimalFuncPrefix + "lift"
	decFuncLiftDyn = decimalFuncPrefix + "lift_dyn"
	decFuncNeg     = decimalFuncPrefix + "neg"
	decFuncMod     = decimalFuncPrefix + "mod"
	decFuncFail    = decimalFuncPrefix + "fail"
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
	opts := make([]expr.Option, 0, len(decimalBinary)+len(decimalBuiltins)+7)
	for op, b := range decimalBinary {
		opts = append(opts, decimalFunction(b.name, decimalBinaryBody(op), decimalBinaryValidate(op, b.compare)))
	}
	for name, b := range decimalBuiltins {
		opts = append(opts, decimalFunction(decimalFuncPrefix+name, b.body, b.validate))
	}
	opts = append(opts,
		decimalFunction(decFuncFloat, decimalFloatBody, decimalUnaryValidate(func(...decimalKind) reflect.Type { return exprFloat64Type })),
		decimalFunction(decFuncLift, decimalLiftBody, decimalUnaryValidate(decimalResultType)),
		decimalFunction(decFuncLiftDyn, decimalLiftBody, decimalUnaryValidate(func(k ...decimalKind) reflect.Type {
			if k[0] == decUnknown {
				return exprAnyType
			}
			return exprDecimalAnyType
		})),
		decimalFunction(decFuncNeg, decimalNegBody, decimalUnaryValidate(decimalResultType)),
		decimalFunction(decFuncMod, decimalModRefused, decimalModValidate),
		decimalFunction(decFuncIn, decimalInBody, decimalInValidate),
		// A fresh patcher per compile: its pass state is never shared
		// by two compiles running at once. Its failure builtin reads that
		// patcher's state, so it is per compile too.
		func(c *conf.Config) {
			p := &decimalPatcher{conf: c}
			c.Visitors = append(c.Visitors, p)
			c.Functions[decFuncFail] = &builtin.Function{
				Name: decFuncFail,
				Func: func(args ...any) (any, error) { return args[0], nil },
				Validate: func([]reflect.Type) (reflect.Type, error) {
					if p.failed != nil {
						return nil, p.failed
					}
					return exprAnyType, nil
				},
			}
		},
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
	decArray                      // a slice / array (the aggregate built-ins' argument)
)

func (k decimalKind) decimal() bool { return k == decStatic || k == decDynamic }

// scalar reports whether k can be an operand of a decimal operator: a
// number, a decimal, or a value known only at run time.
func (k decimalKind) scalar() bool { return k == decNumber || k.decimal() || k == decUnknown }

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
	case reflect.Slice, reflect.Array:
		return decArray
	}
	return decInvalid
}

// decimalResultType is a decimal operation's static type: a Decimal when
// every operand is statically known; the twin's decimal declaration when
// one is the twin's decimal; `any` when one is known only at run time
// (it may be no decimal at all — the builtin then answers as expr-lang
// would).
func decimalResultType(kinds ...decimalKind) reflect.Type {
	t := exprDecimalType
	for _, k := range kinds {
		switch k {
		case decUnknown, decArray:
			return exprAnyType
		case decDynamic:
			t = exprDecimalAnyType
		}
	}
	return t
}

// decimalBinaryApplies reports whether a binary operator over l and r is
// a decimal operation: both operands scalar and one a decimal — or, once
// the expression holds a decimal (sawDecimal), one known only at run
// time (it may carry a decimal there).
func decimalBinaryApplies(l, r decimalKind, sawDecimal bool) bool {
	if !l.scalar() || !r.scalar() {
		return false
	}
	return l.decimal() || r.decimal() || sawDecimal && (l == decUnknown || r == decUnknown)
}

func decimalBinaryValidate(op string, compare bool) func([]reflect.Type) (reflect.Type, error) {
	return func(args []reflect.Type) (reflect.Type, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("invalid operation: %s (want 2 operands, got %d)", op, len(args))
		}
		l, r := decimalKindOf(args[0]), decimalKindOf(args[1])
		if !l.scalar() || !r.scalar() {
			return nil, fmt.Errorf("invalid operation: %s (mismatched types %v and %v)", op, args[0], args[1])
		}
		if compare {
			return exprBoolType, nil
		}
		if !l.decimal() && !r.decimal() {
			return exprAnyType, nil // two numbers: expr-lang's own operator
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
		if !k.scalar() {
			return nil, fmt.Errorf("invalid argument (type %v)", args[0])
		}
		return result(k), nil
	}
}

// decimalPatcher rewrites decimal operations into the builtins. It is
// repeatable: a rewrite can type a node the previous pass could not (a
// `let` bound to a decimal result, a `??` whose default it lifted), so
// expr-lang re-checks and walks again until a pass changes nothing.
//
// sawDecimal (kept across passes, never Reset) records that the
// expression holds a decimal-typed node. From then on an operand typed
// `any` is patched too — it may carry a decimal at run time (a map or
// mixed-array literal launders one) — and its builtin falls back to
// expr-lang's own operator for every non-decimal value. An expression
// with no decimal never sets it, so its program is untouched.
//
// expr-lang's checker never re-checks a call that carries a type —
// neither its Validate nor its arguments — so a call the patcher writes
// would hide an ill-typed operand beneath it (`price / "x" > 1`) or its
// own Validate refusal (`price % 2 > 1`) from every later check. Each
// pass's end clears the calls' types, so the next pass judges by fresh
// ones; the last pass then type-checks the whole patched tree itself
// and, on an error, wraps the root in the failure builtin, whose
// Validate raises that error at its location in the final check — the
// compile refuses exactly what an unpatched check would. An operand
// typed `any` may be stale within a pass (checked before a call beneath
// it was patched), so the `any` rules wait for a pass with no patch so
// far (untyped).
type decimalPatcher struct {
	conf                *conf.Config
	applied, sawDecimal bool
	calls               []*exprast.CallNode
	root                *exprast.Node // the last node a walk visited
	failed              error
}

func (p *decimalPatcher) Reset() { p.applied = false }

func (p *decimalPatcher) ShouldRepeat() bool {
	for _, c := range p.calls {
		c.SetNature(nature.Nature{})
	}
	if !p.applied && len(p.calls) > 0 && p.failed == nil && p.root != nil {
		p.verify()
	}
	return p.applied
}

// verify type-checks the patched tree; an error is raised again, at its
// own location, by the final check (decFuncFail wrapping the root).
func (p *decimalPatcher) verify() {
	_, err := checker.Check(&parser.Tree{Node: *p.root}, p.conf)
	var fe *file.Error
	if err == nil || !stderrors.As(err, &fe) {
		return
	}
	p.failed = stderrors.New(fe.Message)
	wrap := &exprast.CallNode{Callee: &exprast.IdentifierNode{Value: decFuncFail}, Arguments: []exprast.Node{*p.root}}
	wrap.SetLocation(fe.Location)
	*p.root = wrap
}

// operand reports whether an operand of kind k is patched: a decimal,
// or — once the expression holds one — a value known only at run time.
func (p *decimalPatcher) operand(k decimalKind) bool {
	return k.decimal() || p.untyped() && k == decUnknown
}

// untyped reports whether the `any` rules apply now: the expression
// holds a decimal and no patch in this pass has left a type stale.
func (p *decimalPatcher) untyped() bool { return p.sawDecimal && !p.applied }

func (p *decimalPatcher) Visit(node *exprast.Node) {
	p.root = node
	if !p.sawDecimal && decimalKindOf((*node).Type()).decimal() {
		// Walk again: a node visited earlier in this pass is judged
		// with sawDecimal set.
		p.sawDecimal, p.applied = true, true
	}
	switch n := (*node).(type) {
	case *exprast.BinaryNode:
		p.visitBinary(node, n)
	case *exprast.ConditionalNode:
		// `c ? d : n`: lift the other branch, so the result is a
		// decimal on every row.
		k1, k2 := decimalKindOf(n.Exp1.Type()), decimalKindOf(n.Exp2.Type())
		if k1.decimal() && p.liftable(n.Exp2, k2) {
			n.Exp2 = p.lift(k1, k2, n.Exp2)
		} else if k2.decimal() && p.liftable(n.Exp1, k1) {
			n.Exp1 = p.lift(k2, k1, n.Exp1)
		}
	case *exprast.UnaryNode:
		if k := decimalKindOf(n.Node.Type()); n.Operator == "-" && p.operand(k) {
			exprast.Patch(node, p.call(decFuncNeg, decimalResultType(k), n.Node))
		}
	case *exprast.CallNode:
		// The `%` patcher (mod.go) runs first and may have rewritten a
		// `%` whose operand was still untyped; refuse it once a
		// decimal operand is known.
		if id, ok := n.Callee.(*exprast.IdentifierNode); ok && id.Value == ModFuncName && len(n.Arguments) == 2 &&
			(decimalKindOf(n.Arguments[0].Type()).decimal() || decimalKindOf(n.Arguments[1].Type()).decimal()) {
			exprast.Patch(node, p.call(decFuncMod, exprAnyType, n.Arguments...))
		}
	case *exprast.BuiltinNode:
		p.visitBuiltin(node, n)
	}
}

func (p *decimalPatcher) visitBinary(node *exprast.Node, n *exprast.BinaryNode) {
	l, r := decimalKindOf(n.Left.Type()), decimalKindOf(n.Right.Type())
	switch n.Operator {
	case "??":
		if l.decimal() && p.liftable(n.Right, r) {
			n.Right = p.lift(l, r, n.Right)
		}
		return
	case "%":
		if l.decimal() || r.decimal() {
			exprast.Patch(node, p.call(decFuncMod, exprAnyType, n.Left, n.Right))
		}
		return
	case "in":
		if p.untyped() && l.scalar() && (r == decArray || r == decUnknown) {
			exprast.Patch(node, p.call(decFuncIn, exprBoolType, n.Left, n.Right))
		}
		return
	}
	b, ok := decimalBinary[n.Operator]
	if !ok || isNilNode(n.Left) || isNilNode(n.Right) || !decimalBinaryApplies(l, r, p.untyped()) {
		return
	}
	t := exprBoolType
	if !b.compare {
		t = decimalResultType(l, r)
	}
	exprast.Patch(node, p.call(b.name, t, n.Left, n.Right))
}

// liftable reports whether the other side of a decimal `??` or ternary
// is lifted: a number, or a value known only at run time (never nil).
func (p *decimalPatcher) liftable(n exprast.Node, k decimalKind) bool {
	if c, ok := n.(*exprast.CallNode); ok {
		if id, ok := c.Callee.(*exprast.IdentifierNode); ok && (id.Value == decFuncLift || id.Value == decFuncLiftDyn) {
			return false // lifted already (an untyped lift stays `any`)
		}
	}
	return !isNilNode(n) && (k == decNumber || k == decUnknown)
}

// lift wraps n (kind k) so it joins the decimal side (kind d) as a
// decimal: a static Decimal beside a static decimal, the twin's decimal
// declaration beside the twin's.
func (p *decimalPatcher) lift(d, k decimalKind, n exprast.Node) exprast.Node {
	name, t := decFuncLift, decimalResultType(k)
	if d == decDynamic {
		name, t = decFuncLiftDyn, exprDecimalAnyType
	}
	c := p.call(name, t, n)
	c.SetLocation(n.Location())
	return c
}

func (p *decimalPatcher) call(name string, t reflect.Type, args ...exprast.Node) exprast.Node {
	p.applied = true
	call := &exprast.CallNode{Callee: &exprast.IdentifierNode{Value: name}, Arguments: args}
	call.SetType(t)
	p.calls = append(p.calls, call)
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
		// Only a value typed `any` reaches here (a number is always a
		// decimal): a non-number keeps its own value, as the unlifted
		// `??` / ternary would have answered.
		return args[0], nil
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
