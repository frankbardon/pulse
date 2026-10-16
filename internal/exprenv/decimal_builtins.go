package exprenv

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"

	exprast "github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	exprruntime "github.com/expr-lang/expr/vm/runtime"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// The numeric built-ins over a decimal (decimal.go has the operators).
//
// expr-lang's own abs / ceil / floor / round / int / max / min / mean /
// median refuse a Decimal struct at compile, and its sum / sort / sortBy
// / `in` fail on one at run time (or, for `in`, silently never match).
// The decimal patcher rewrites each call whose argument is — or, once
// the expression holds a decimal, may be — a decimal into the builtin
// below, which answers exactly when a Decimal arrives and otherwise
// calls expr-lang's own built-in, so a non-decimal value answers as it
// always did:
//
//   - abs: exact, at the operand's scale.
//   - ceil / floor / round: an integral decimal at scale 0. round is
//     half-even — every decimal rescale's rule (encoding.Rescale) — so
//     round(2.5) on a decimal is 2 where a float's round gives 3.
//   - int: truncated toward zero; past the int range it is
//     PULSE_DECIMAL_OVERFLOW, never a wrapped integer.
//   - max / min: exact comparison; the winner is a decimal (a number
//     joins as its shortest spelling); ties keep the first.
//   - sum: exact. mean: the exact sum divided by the count, at
//     max(scale, 4) like `/`. median: the middle value, or the two
//     middles' sum divided by 2 at max(scale, 4).
//   - sort / sortBy / `in`: ordered and matched by exact value.
//
// The float-domain built-ins (sqrt, log, exp, pow and the vector
// family, vector.go's exprFloat) read a decimal as its float64: their
// answers are irrational floats, so there is no exact form to keep.
// `%` over a decimal is refused at compile (decimalModValidate).

const decFuncIn = decimalFuncPrefix + "in"

var exprIntType = reflect.TypeOf(0)

// decimalBuiltin is one overridden built-in: its run-time body and the
// Validate typing the patched call.
type decimalBuiltin struct {
	body     func(args ...any) (any, error)
	validate func([]reflect.Type) (reflect.Type, error)
}

// decimalBuiltins maps an expr-lang built-in name to its override,
// registered as decimalFuncPrefix + name.
var decimalBuiltins = map[string]decimalBuiltin{
	"abs":    {decimalUnaryBuiltin("abs", decimalAbs), decimalUnaryBuiltinValidate("abs", nil)},
	"ceil":   {decimalUnaryBuiltin("ceil", decimalCeil), decimalUnaryBuiltinValidate("ceil", nil)},
	"floor":  {decimalUnaryBuiltin("floor", decimalFloor), decimalUnaryBuiltinValidate("floor", nil)},
	"round":  {decimalUnaryBuiltin("round", decimalRound), decimalUnaryBuiltinValidate("round", nil)},
	"int":    {decimalUnaryBuiltin("int", decimalInt), decimalUnaryBuiltinValidate("int", exprIntType)},
	"max":    {decimalAggregate("max", decimalMinMax(1)), decimalAggregateValidate("max")},
	"min":    {decimalAggregate("min", decimalMinMax(-1)), decimalAggregateValidate("min")},
	"mean":   {decimalAggregate("mean", decimalMean), decimalAggregateValidate("mean")},
	"median": {decimalAggregate("median", decimalMedian), decimalAggregateValidate("median")},
	"sum":    {decimalSumBody, decimalCollectionValidate("sum", 1, 1, exprAnyType)},
	"sort":   {decimalSortBody, decimalCollectionValidate("sort", 1, 2, exprAnysType)},
	"sortBy": {decimalSortByBody, decimalCollectionValidate("sortBy", 2, 3, exprAnysType)},
}

var exprAnysType = reflect.TypeOf([]any(nil))

// visitBuiltin patches a built-in call over a (possible) decimal.
func (p *decimalPatcher) visitBuiltin(node *exprast.Node, n *exprast.BuiltinNode) {
	kinds := make([]decimalKind, len(n.Arguments))
	for i, a := range n.Arguments {
		kinds[i] = decimalKindOf(a.Type())
	}
	switch n.Name {
	case "float":
		if len(kinds) == 1 && p.operand(kinds[0]) {
			exprast.Patch(node, p.call(decFuncFloat, exprFloat64Type, n.Arguments[0]))
		}
	case "abs", "ceil", "floor", "round", "int":
		if len(kinds) == 1 && p.operand(kinds[0]) {
			t := decimalResultType(kinds[0])
			if n.Name == "int" {
				t = exprIntType
			}
			exprast.Patch(node, p.call(decimalFuncPrefix+n.Name, t, n.Arguments[0]))
		}
	case "max", "min", "mean", "median":
		for _, k := range kinds {
			if k.decimal() || p.untyped() && (k == decUnknown || k == decArray) {
				exprast.Patch(node, p.call(decimalFuncPrefix+n.Name, decimalResultType(kinds...), n.Arguments...))
				return
			}
		}
	case "sum", "sort", "sortBy":
		if !p.untyped() || len(kinds) == 0 || kinds[0] != decArray && kinds[0] != decUnknown {
			return
		}
		args := n.Arguments
		switch {
		case n.Name == "sum" && len(args) == 2:
			// sum(xs, pred) == sum(map(xs, pred)).
			args = []exprast.Node{&exprast.BuiltinNode{Name: "map", Arguments: args}}
		case n.Name == "sortBy" && len(args) >= 2:
			// The keys ride beside the array: sortBy(xs, keys[, order]).
			keys := &exprast.BuiltinNode{Name: "map", Arguments: []exprast.Node{args[0], args[1]}}
			args = append([]exprast.Node{args[0], keys}, args[2:]...)
		}
		t := exprAnysType
		if n.Name == "sum" {
			t = exprAnyType
		}
		exprast.Patch(node, p.call(decimalFuncPrefix+n.Name, t, args...))
	}
}

// exprBuiltin calls expr-lang's own built-in name — the answer for every
// non-decimal value — turning its panics into evaluation errors.
func exprBuiltin(name string, args ...any) (out any, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			out, err = nil, fmt.Errorf("%v", rec)
		}
	}()
	f := builtin.Builtins[builtin.Index[name]]
	switch {
	case f.Fast != nil && len(args) == 1:
		return f.Fast(args[0]), nil
	case f.Func != nil:
		return f.Func(args...)
	case f.Safe != nil:
		v, _, err := f.Safe(args...)
		return v, err
	}
	return nil, fmt.Errorf("%s: no implementation", name)
}

func decimalUnaryBuiltin(name string, fn func(Decimal) (any, error)) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("invalid number of arguments for %s (expected 1, got %d)", name, len(args))
		}
		if d, ok := args[0].(Decimal); ok {
			return fn(d)
		}
		return exprBuiltin(name, args...)
	}
}

// decimalUnaryBuiltinValidate types abs / ceil / floor / round (result
// nil: the operand's decimal type) and int (result int).
func decimalUnaryBuiltinValidate(name string, result reflect.Type) func([]reflect.Type) (reflect.Type, error) {
	return func(args []reflect.Type) (reflect.Type, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("invalid number of arguments (expected 1, got %d)", len(args))
		}
		k := decimalKindOf(args[0])
		if !k.scalar() {
			return nil, fmt.Errorf("invalid argument for %s (type %v)", name, args[0])
		}
		if result != nil {
			return result, nil
		}
		return decimalResultType(k), nil
	}
}

func decimalAbs(d Decimal) (any, error) {
	return decimalOf(new(big.Int).Abs(d.Value.Mantissa()), d.Scale)
}

// decimalIntegral is d at scale 0, the remainder rounded by adjust (the
// truncated quotient, the remainder and the divisor).
func decimalIntegral(d Decimal, adjust func(q, r, div *big.Int) *big.Int) (any, error) {
	div := decimalPow10[d.Scale]
	q, r := new(big.Int).QuoRem(d.Value.Mantissa(), div, new(big.Int))
	if r.Sign() != 0 {
		q = adjust(q, r, div)
	}
	return decimalOf(q, 0)
}

func decimalCeil(d Decimal) (any, error) {
	return decimalIntegral(d, func(q, r, _ *big.Int) *big.Int {
		if r.Sign() > 0 {
			q.Add(q, big.NewInt(1))
		}
		return q
	})
}

func decimalFloor(d Decimal) (any, error) {
	return decimalIntegral(d, func(q, r, _ *big.Int) *big.Int {
		if r.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		}
		return q
	})
}

// decimalRound is half-even, the rule of every decimal rescale.
func decimalRound(d Decimal) (any, error) {
	return decimalOf(roundHalfEven(d.Value.Mantissa(), decimalPow10[d.Scale]), 0)
}

func decimalInt(d Decimal) (any, error) {
	q := new(big.Int).Quo(d.Value.Mantissa(), decimalPow10[d.Scale])
	if !q.IsInt64() || q.Int64() > math.MaxInt || q.Int64() < math.MinInt {
		return nil, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
			fmt.Sprintf("int(%s): the integer part is out of the int range", d))
	}
	return int(q.Int64()), nil
}

// decimalValues flattens the aggregate built-ins' arguments (a slice is
// read element by element, as expr-lang does) and reports whether any
// value is a Decimal.
func decimalValues(args []any) (vals []any, hasDecimal bool) {
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if rv := reflect.ValueOf(v); v != nil && depth < 64 && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
			for i := 0; i < rv.Len(); i++ {
				walk(rv.Index(i).Interface(), depth+1)
			}
			return
		}
		if _, ok := v.(Decimal); ok {
			hasDecimal = true
		}
		vals = append(vals, v)
	}
	for _, a := range args {
		walk(a, 0)
	}
	return vals, hasDecimal
}

// decimalsOf reads every value as a decimal; a non-number is the
// built-in's invalid-argument error.
func decimalsOf(name string, vals []any) ([]Decimal, error) {
	out := make([]Decimal, len(vals))
	for i, v := range vals {
		d, ok, err := toDecimal(v)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("invalid argument for %s (type %T)", name, v)
		}
		out[i] = d
	}
	return out, nil
}

// decimalAggregate is max / min / mean / median: exact when a Decimal is
// among the (flattened) values, expr-lang's own built-in otherwise.
func decimalAggregate(name string, fn func([]Decimal) (any, error)) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		vals, has := decimalValues(args)
		if !has {
			return exprBuiltin(name, args...)
		}
		ds, err := decimalsOf(name, vals)
		if err != nil {
			return nil, err
		}
		return fn(ds)
	}
}

func decimalAggregateValidate(name string) func([]reflect.Type) (reflect.Type, error) {
	return func(args []reflect.Type) (reflect.Type, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("not enough arguments to call %s", name)
		}
		kinds := make([]decimalKind, len(args))
		for i, a := range args {
			kinds[i] = decimalKindOf(a)
			if !kinds[i].scalar() && kinds[i] != decArray {
				return nil, fmt.Errorf("invalid argument for %s (type %v)", name, a)
			}
		}
		return decimalResultType(kinds...), nil
	}
}

func decimalCompare(a, b Decimal) int {
	x, y, _ := aligned(a, b)
	return x.Cmp(y)
}

// decimalMinMax keeps the value v replacing the best when
// sign(cmp(v, best)) == want — strict, so ties keep the first.
func decimalMinMax(want int) func([]Decimal) (any, error) {
	return func(ds []Decimal) (any, error) {
		best := ds[0]
		for _, d := range ds[1:] {
			if c := decimalCompare(d, best); c == want {
				best = d
			}
		}
		return best, nil
	}
}

func decimalSum(ds []Decimal) (Decimal, error) {
	acc := Decimal{Value: encoding.NewDecimal128FromInt(0)}
	for _, d := range ds {
		s, err := decimalAdd(acc, d)
		if err != nil {
			return Decimal{}, err
		}
		acc = s.(Decimal)
	}
	return acc, nil
}

func decimalMean(ds []Decimal) (any, error) {
	s, err := decimalSum(ds)
	if err != nil {
		return nil, err
	}
	return decimalDiv(s, Decimal{Value: encoding.NewDecimal128FromInt(int64(len(ds)))})
}

func decimalMedian(ds []Decimal) (any, error) {
	sorted := append([]Decimal(nil), ds...)
	sort.SliceStable(sorted, func(i, j int) bool { return decimalCompare(sorted[i], sorted[j]) < 0 })
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2], nil
	}
	s, err := decimalAdd(sorted[n/2-1], sorted[n/2])
	if err != nil {
		return nil, err
	}
	return decimalDiv(s.(Decimal), Decimal{Value: encoding.NewDecimal128FromInt(2)})
}

// decimalElems reads a collection argument's elements.
func decimalElems(name string, v any) ([]any, error) {
	rv := reflect.ValueOf(v)
	if v == nil || rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("builtin %s takes only array (got %T)", name, v)
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, nil
}

func hasDecimalElem(vals []any) bool {
	for _, v := range vals {
		if _, ok := v.(Decimal); ok {
			return true
		}
	}
	return false
}

// decimalSumBody is sum(xs) (sum(xs, pred) arrives as sum(map(xs,
// pred))): exact over a Decimal, else expr-lang's own accumulation —
// an int 0 the elements are added to in order.
func decimalSumBody(args ...any) (out any, err error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("invalid number of arguments for sum (expected 1, got %d)", len(args))
	}
	vals, err := decimalElems("sum", args[0])
	if err != nil {
		return nil, err
	}
	if hasDecimalElem(vals) {
		ds, err := decimalsOf("sum", vals)
		if err != nil {
			return nil, err
		}
		return decimalSum(ds)
	}
	defer func() {
		if rec := recover(); rec != nil {
			out, err = nil, fmt.Errorf("%v", rec)
		}
	}()
	var acc any = 0
	for _, v := range vals {
		acc = exprruntime.Add(v, acc)
	}
	return acc, nil
}

// decimalLess orders two values: exactly when either is a Decimal,
// expr-lang's own `<` otherwise (which panics on a mismatched pair; the
// caller recovers).
func decimalLess(a, b any) bool {
	_, ad := a.(Decimal)
	_, bd := b.(Decimal)
	if ad || bd {
		x, xok, xerr := toDecimal(a)
		y, yok, yerr := toDecimal(b)
		if !xok || !yok {
			if xerr == nil {
				xerr = yerr
			}
			if xerr == nil {
				xerr = fmt.Errorf("invalid operation: %T < %T", a, b)
			}
			panic(xerr)
		}
		return decimalCompare(x, y) < 0
	}
	return exprruntime.Less(a, b)
}

func decimalSortOrder(name string, args []any, at int) (desc bool, err error) {
	if len(args) <= at {
		return false, nil
	}
	switch args[at] {
	case "asc":
		return false, nil
	case "desc":
		return true, nil
	}
	return false, fmt.Errorf("invalid order %v for %s, expected asc or desc", args[at], name)
}

// decimalSorter sorts keys (and the array beside them) as expr-lang's
// runtime.Sort / SortBy do, with decimalLess.
type decimalSorter struct {
	desc         bool
	array, value []any
}

func (s *decimalSorter) Len() int { return len(s.value) }
func (s *decimalSorter) Swap(i, j int) {
	s.value[i], s.value[j] = s.value[j], s.value[i]
	if s.array != nil {
		s.array[i], s.array[j] = s.array[j], s.array[i]
	}
}
func (s *decimalSorter) Less(i, j int) bool {
	if s.desc {
		return decimalLess(s.value[j], s.value[i])
	}
	return decimalLess(s.value[i], s.value[j])
}

func (s *decimalSorter) sort() (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%v", rec)
		}
	}()
	sort.Sort(s)
	return nil
}

func decimalSortBody(args ...any) (any, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("invalid number of arguments (expected 1 or 2, got %d)", len(args))
	}
	vals, err := decimalElems("sort", args[0])
	if err != nil || !hasDecimalElem(vals) {
		return exprBuiltin("sort", args...)
	}
	desc, err := decimalSortOrder("sort", args, 1)
	if err != nil {
		return nil, err
	}
	s := &decimalSorter{desc: desc, value: vals}
	if err := s.sort(); err != nil {
		return nil, err
	}
	return s.value, nil
}

// decimalSortByBody is sortBy(xs, keys[, order]) — the patcher computes
// keys as map(xs, pred). Its sort is expr-lang's own (sort.Sort over the
// key pairs), ordered by decimalLess.
func decimalSortByBody(args ...any) (any, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("invalid number of arguments (expected 2 or 3, got %d)", len(args))
	}
	array, err := decimalElems("sortBy", args[0])
	if err != nil {
		return nil, err
	}
	keys, err := decimalElems("sortBy", args[1])
	if err != nil {
		return nil, err
	}
	desc, err := decimalSortOrder("sortBy", args, 2)
	if err != nil {
		return nil, err
	}
	s := &decimalSorter{desc: desc, array: array, value: keys}
	if err := s.sort(); err != nil {
		return nil, err
	}
	return s.array, nil
}

// decimalCollectionValidate types sum / sort / sortBy: the first
// argument a collection (or `any`).
func decimalCollectionValidate(name string, minArgs, maxArgs int, result reflect.Type) func([]reflect.Type) (reflect.Type, error) {
	return func(args []reflect.Type) (reflect.Type, error) {
		if len(args) < minArgs || len(args) > maxArgs {
			return nil, fmt.Errorf("invalid number of arguments for %s (got %d)", name, len(args))
		}
		if k := decimalKindOf(args[0]); k != decArray && k != decUnknown {
			return nil, fmt.Errorf("builtin %s takes only array (got %v)", name, args[0])
		}
		return result, nil
	}
}

// decimalInBody is `x in xs` matched by exact value when a Decimal is on
// either side, expr-lang's own `in` otherwise.
func decimalInBody(args ...any) (out any, err error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("in: want 2 operands, got %d", len(args))
	}
	if vals, err := decimalElems("in", args[1]); err == nil {
		_, ld := args[0].(Decimal)
		if ld || hasDecimalElem(vals) {
			x, xok, _ := toDecimal(args[0])
			if !xok {
				return false, nil // nil / a non-number never matches
			}
			for _, v := range vals {
				if y, yok, _ := toDecimal(v); yok && decimalCompare(x, y) == 0 {
					return true, nil
				}
			}
			return false, nil
		}
	}
	defer func() {
		if rec := recover(); rec != nil {
			out, err = nil, fmt.Errorf("%v", rec)
		}
	}()
	return exprruntime.In(args[0], args[1]), nil
}

func decimalInValidate(args []reflect.Type) (reflect.Type, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("invalid operation: in (want 2 operands, got %d)", len(args))
	}
	if !decimalKindOf(args[0]).scalar() {
		return nil, fmt.Errorf("invalid operation: in (type %v)", args[0])
	}
	if k := decimalKindOf(args[1]); k != decArray && k != decUnknown {
		return nil, fmt.Errorf("invalid operation: in (type %v)", args[1])
	}
	return exprBoolType, nil
}

// decimalModValidate refuses `%` over a decimal at compile: a decimal
// has no remainder operator; float() is the escape.
func decimalModValidate(args []reflect.Type) (reflect.Type, error) {
	l, r := "?", "?"
	if len(args) == 2 {
		l, r = fmt.Sprint(args[0]), fmt.Sprint(args[1])
	}
	return nil, fmt.Errorf("invalid operation: %% over a decimal (types %s and %s); convert it with float() first, e.g. float(price) %% 2", l, r)
}

func decimalModRefused(args ...any) (any, error) {
	return nil, fmt.Errorf("invalid operation: %% over a decimal; convert it with float() first")
}
