package exprenv

import (
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/checker/nature"
	exprtypes "github.com/expr-lang/expr/types"
	exprruntime "github.com/expr-lang/expr/vm/runtime"
)

// Exact u64 fields.
//
// A u64 field used to bind as its float64 echo, so every expression
// over it — `id == params.k`, `id == 9007199254740993`, `id < other_id`
// — was exact only up to 2^53. A u64 field now binds as a [U64], the
// stored word, and three patchers rewrite the program around it:
//
//   - u64WrapPatcher wraps every node typed U64 (a field identifier,
//     `$env.id`, a let name bound to one, abs / max / min over one) in a
//     call that yields its float64 echo, so arithmetic, `%`, `**`, unary
//     minus, the maths built-ins (exp, log, …), `??`, conditionals and
//     the result type of the whole expression see a float64 exactly as
//     before — same bits, same result types, same compile errors.
//   - u64ComparePatcher rewrites a comparison (`==`, `!=`, `<`, `<=`,
//     `>`, `>=`) or `in` the checker accepted over a wrapped operand into
//     an EXACT comparison: the word against an integer constant or param
//     (int64 or uint64), another u64 field, or a float, over the full
//     0..2^64−1 range (NaN unordered). An operand that is no number at
//     run time (nil, a string, a decimal) takes the historical operator
//     over the echo — same result, same evaluation error at the same
//     location. A comparison the checker refused (`id == "a"`) or whose
//     other operand is a decimal is left alone (the echo keeps its
//     compile error / the decimal patcher's exact compare).
//   - u64Retype, the last repeatable patcher, keeps the final check
//     honest about the calls the other two write.
//
// A value that is not a U64 at run time (the record holds no decoder
// word for it — a feature rewrote the column — or the env binds the
// echo, as an `$env` read does) flows through as the float64 it always
// was: the wrapper passes it on and the comparison falls back to
// expr-lang's own operator, so the result is the historical one.
//
// The untyped null-row twin declares a u64 name as u64Operand (like a
// decimal's decimalOperand), so the patchers still recognise it; the
// wrapper is unknown-typed there and yields nil for a null value, so nil
// follows expr-lang's nil rules with the historical error text.
//
// The wrap and compare patchers are one-shot and registered in
// [Options] before the `%` patcher, so `%` / `**` / the decimal patcher
// judge the wrapped float64 exactly as they judged the old float64
// field; predict's compile declares a u64 field through the same
// [ValueType], so both sides type-check identically. The patchers parse
// nothing a purity or identifier walk reads: those walk the raw source.

// U64 is a u64 field's stored word as an expression env binds it.
type U64 uint64

func (U64) exprU64() {}

// u64Operand is the twin's declaration of a u64 name: a U64, or nil on
// a null row.
type u64Operand interface{ exprU64() }

var (
	exprU64Type                   = reflect.TypeOf(U64(0))
	exprU64AnyType                = reflect.TypeOf((*u64Operand)(nil)).Elem()
	untypedU64     exprtypes.Type = reflectDecl{exprU64AnyType}
)

// The builtins the patchers rewrite into — deliberately
// unspellable-looking, like [ModFuncName].
const (
	U64FloatFuncName = "__pulse_u64"
	u64FuncPrefix    = "__pulse_u64_"
)

// u64Compare maps each rewritten operator to its builtin name and its
// verdict on an exact three-way comparison.
var u64Compare = map[string]struct {
	name    string
	verdict func(c int) bool
}{
	"==": {u64FuncPrefix + "eq", func(c int) bool { return c == 0 }},
	"!=": {u64FuncPrefix + "ne", func(c int) bool { return c != 0 }},
	"<":  {u64FuncPrefix + "lt", func(c int) bool { return c < 0 }},
	"<=": {u64FuncPrefix + "le", func(c int) bool { return c <= 0 }},
	">":  {u64FuncPrefix + "gt", func(c int) bool { return c > 0 }},
	">=": {u64FuncPrefix + "ge", func(c int) bool { return c >= 0 }},
}

const u64InFuncName = u64FuncPrefix + "in"

// U64FuncNames returns every builtin the u64 patchers rewrite into
// (internal/processing's purity table lists them as pure).
func U64FuncNames() []string {
	out := []string{U64FloatFuncName, u64InFuncName}
	for _, op := range []string{"==", "!=", "<", "<=", ">", ">="} {
		out = append(out, u64Compare[op].name)
	}
	return out
}

// exprU64Options returns the u64 builtins and the wrap and compare patchers.
func exprU64Options() []expr.Option {
	opts := []expr.Option{
		decimalFunction(U64FloatFuncName, u64FloatBody, u64FloatValidate),
		decimalFunction(u64InFuncName, u64InBody, u64BoolValidate),
	}
	for op, c := range u64Compare {
		opts = append(opts, decimalFunction(c.name, u64CompareBody(op, c.verdict), u64BoolValidate))
	}
	// Two one-shot passes: the wrap, then — after expr-lang re-checks
	// the tree with every u64 read as its float64 echo — the comparisons.
	return append(opts, expr.Patch(u64WrapPatcher{}), expr.Patch(u64ComparePatcher{}))
}

// exprU64Retype returns the visitor [options] registers LAST: see
// u64Retype.
func exprU64Retype() expr.Option { return expr.Patch(&u64Retype{}) }

func u64FloatValidate(args []reflect.Type) (reflect.Type, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("invalid number of arguments (expected 1, got %d)", len(args))
	}
	if t := args[0]; t != nil && t.Kind() != reflect.Interface {
		return exprFloat64Type, nil // a U64 (or a let name already bound to its echo)
	}
	// The twin's u64 operand (a U64 or nil): unknown, exactly as the
	// twin's `any` declaration always checked.
	return nil, nil
}

func u64BoolValidate(args []reflect.Type) (reflect.Type, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("invalid number of arguments (expected 2, got %d)", len(args))
	}
	return exprBoolType, nil
}

// u64WrapPatcher wraps every node typed U64 (a field, `$env.id`, a let
// name, abs / max / min over one) in the float64-echo call. It is
// one-shot and runs before every other one-shot patcher of [Options],
// so they — and the re-check before each — see the float64 the field
// always bound as. (expr-lang's checker does not count a named integer
// type as a number: before the wrap, `id + 1` and `id == 5` do not even
// type-check.)
type u64WrapPatcher struct{}

func (u64WrapPatcher) Visit(node *exprast.Node) {
	if t := (*node).Type(); t == exprU64Type || t == exprU64AnyType {
		// Post-order: every U64 below is already wrapped.
		exprast.Patch(node, &exprast.CallNode{
			Callee:    &exprast.IdentifierNode{Value: U64FloatFuncName},
			Arguments: []exprast.Node{*node},
		})
	}
}

// u64ComparePatcher rewrites a comparison (`==` `!=` `<` `<=` `>` `>=`)
// or `in` over a wrapped u64 into its exact builtin — only one the
// checker ACCEPTED over the float64 echo (typed bool, like pow.go's
// rule), and only against a number or a value known only at run time
// (`in`: a list or such a value). A decimal operand stays with the
// decimal patcher; everything else keeps the float64 comparison and so
// compiles, or fails, exactly as before.
type u64ComparePatcher struct{}

func (u64ComparePatcher) Visit(node *exprast.Node) {
	b, ok := (*node).(*exprast.BinaryNode)
	if !ok || b.Type() != exprBoolType {
		return
	}
	var name string
	if b.Operator == "in" {
		name = u64InFuncName
	} else if c, ok := u64Compare[b.Operator]; ok {
		name = c.name
	} else {
		return
	}
	l, lu := u64Unwrap(b.Left)
	r, ru := u64Unwrap(b.Right)
	switch {
	case b.Operator == "in":
		if !lu || !u64Collection(b.Right.Type()) {
			return
		}
		r = b.Right
	case lu && ru:
	case lu:
		if !u64Comparable(b.Right.Type()) {
			return
		}
		r = b.Right
	case ru:
		if !u64Comparable(b.Left.Type()) {
			return
		}
		l = b.Left
	default:
		return
	}
	exprast.Patch(node, &exprast.CallNode{
		Callee:    &exprast.IdentifierNode{Value: name},
		Arguments: []exprast.Node{l, r},
	})
}

// u64Retype clears the type of every call the u64 patchers wrote. The
// checker never re-checks a call that carries a type — neither its
// Validate nor its arguments — and each check between patchers types
// them, so an operand that does not type-check (`id == x + "a"`) would
// hide beneath one. It is repeatable and registered after every other
// repeatable patcher, so it ends each repeatable pass and the final
// check judges the calls, and their arguments, afresh: the compile
// refuses exactly what the float64 field's did, with the same message.
type u64Retype struct{}

func (*u64Retype) Reset() {}

func (*u64Retype) ShouldRepeat() bool { return false }

func (*u64Retype) Visit(node *exprast.Node) {
	c, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	if id, ok := c.Callee.(*exprast.IdentifierNode); ok && strings.HasPrefix(id.Value, U64FloatFuncName) {
		c.SetNature(nature.Nature{})
	}
}

// u64Unwrap returns the raw U64 operand under a wrapper call.
func u64Unwrap(n exprast.Node) (exprast.Node, bool) {
	c, ok := n.(*exprast.CallNode)
	if !ok || len(c.Arguments) != 1 {
		return n, false
	}
	if id, ok := c.Callee.(*exprast.IdentifierNode); !ok || id.Value != U64FloatFuncName {
		return n, false
	}
	return c.Arguments[0], true
}

// u64Comparable reports whether t, the other operand of a comparison
// with a u64, is one the exact builtin takes: an integer or float kind,
// or an interface that is not the twin's decimal.
func u64Comparable(t reflect.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	case reflect.Interface:
		return t != exprDecimalAnyType
	}
	return false
}

// u64Collection reports whether t, the right operand of `u64 in t`, is
// a list or a value known only at run time.
func u64Collection(t reflect.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return true
	case reflect.Interface:
		return t != exprDecimalAnyType
	}
	return false
}

// u64Echo is v with a U64 replaced by its float64 echo — the value the
// field bound as before.
func u64Echo(v any) any {
	if u, ok := v.(U64); ok {
		return float64(u)
	}
	return v
}

func u64FloatBody(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("invalid number of arguments (expected 1, got %d)", len(args))
	}
	return u64Echo(args[0]), nil
}

// u64CompareBody is a rewritten comparison: exact when one operand is a
// U64 and the other a number, the historical operator over the echo
// otherwise (it panics exactly as the operator did; the VM recovers it
// into the same evaluation error).
func u64CompareBody(op string, verdict func(int) bool) func(args ...any) (any, error) {
	fallback := decimalBinary[op].other
	decimal := decimalBinaryBody(op)
	return func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("invalid number of arguments (expected 2, got %d)", len(args))
		}
		a, b := args[0], args[1]
		c, ordered, exact := 0, false, false
		if u, ok := a.(U64); ok {
			c, ordered, exact = cmpU64(uint64(u), b)
		} else if u, ok := b.(U64); ok {
			c, ordered, exact = cmpU64(uint64(u), a)
			c = -c
		}
		if exact {
			if !ordered {
				return op == "!=", nil // NaN: every comparison false, != true
			}
			return verdict(c), nil
		}
		a, b = u64Echo(a), u64Echo(b)
		_, ad := a.(Decimal)
		_, bd := b.(Decimal)
		if ad || bd {
			return decimal(a, b)
		}
		return fallback(a, b), nil
	}
}

// u64InBody is a rewritten `in`: element by element exact equality when
// the needle is a U64 and the collection a list, expr-lang's `in` over
// the echo otherwise.
func u64InBody(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("invalid number of arguments (expected 2, got %d)", len(args))
	}
	needle, coll := args[0], args[1]
	if u, ok := needle.(U64); ok && coll != nil {
		if v := reflect.ValueOf(coll); v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			for i := 0; i < v.Len(); i++ {
				e := v.Index(i)
				if !e.IsValid() {
					continue
				}
				ev := e.Interface()
				if c, ordered, exact := cmpU64(uint64(u), ev); exact {
					if ordered && c == 0 {
						return true, nil
					}
					continue
				}
				if exprruntime.Equal(ev, float64(u)) {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return exprruntime.In(u64Echo(needle), coll), nil
}

// cmpU64 compares u exactly with v, a number of any integer or float
// kind; exact is false when v is no number (nil, a string, a decimal),
// ordered false when v is NaN.
func cmpU64(u uint64, v any) (c int, ordered, exact bool) {
	switch x := v.(type) {
	case U64:
		return cmpOrdered(u, uint64(x)), true, true
	case uint64:
		return cmpOrdered(u, x), true, true
	case int:
		return cmpUintInt(u, int64(x)), true, true
	case int64:
		return cmpUintInt(u, x), true, true
	case float64:
		return cmpUintFloatOrdered(u, x)
	case nil:
		return 0, false, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmpUintInt(u, rv.Int()), true, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmpOrdered(u, rv.Uint()), true, true
	case reflect.Float32, reflect.Float64:
		return cmpUintFloatOrdered(u, rv.Float())
	}
	return 0, false, false
}

func cmpUintInt(u uint64, i int64) int {
	if i < 0 {
		return 1
	}
	return cmpOrdered(u, uint64(i))
}

func cmpUintFloatOrdered(u uint64, f float64) (int, bool, bool) {
	if math.IsNaN(f) {
		return 0, false, true
	}
	return cmpUintFloat(u, f), true, true
}

func cmpOrdered(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpUintFloat compares u with a non-NaN f exactly.
func cmpUintFloat(u uint64, f float64) int {
	if f < 0 {
		return 1
	}
	if f >= 1<<64 {
		return -1
	}
	t := math.Trunc(f) // 0 <= t < 2^64: uint64(t) is exact
	if c := cmpOrdered(u, uint64(t)); c != 0 {
		return c
	}
	if f > t {
		return -1
	}
	return 0
}
