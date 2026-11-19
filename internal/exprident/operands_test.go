package exprident

import (
	"reflect"
	"testing"
)

// TestNullableOperands_Guards: an unguarded nullable read reaching a
// nil-rejecting operand reports with its kind; a ternary nil check, a
// `&&` / `||` short-circuit, `??` and a let over a guarded name
// suppress it; the nil-safe operators, total()'s argument, a function
// that takes nil and a non-nullable read never report.
func TestNullableOperands_Guards(t *testing.T) {
	t.Parallel()

	nullable := func(name string) bool { return name == "x" || name == "y" || name == "c" || name == "s" }
	r := func(pairs ...string) []OperandRead {
		var out []OperandRead
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, OperandRead{Field: pairs[i], Kind: pairs[i+1]})
		}
		return out
	}
	cases := []struct {
		src    string
		reads  []OperandRead
		parsed bool
	}{
		// Fires.
		{`x * 2`, r("x", OperandArithmetic), true},
		{`2 + x`, r("x", OperandArithmetic), true},
		{`x % 3`, r("x", OperandArithmetic), true},
		{`x ** 2`, r("x", OperandArithmetic), true},
		{`x > 1`, r("x", OperandCompare), true},
		{`1 <= x`, r("x", OperandCompare), true},
		{`-x`, r("x", OperandUnaryMinus), true},
		{`abs(x)`, r("x", OperandFunction), true},
		{`max(x, 1)`, r("x", OperandFunction), true},
		{`round(x) + y`, r("x", OperandFunction, "y", OperandArithmetic), true},
		{`len(c)`, r("c", OperandFunction), true},
		{`c + "!"`, r("c", OperandArithmetic), true},
		{`s[0]`, r("s", OperandIndex), true},
		{`s[1:]`, r("s", OperandIndex), true},
		{`has_any(["a"], s)`, r("s", OperandFunction), true},
		{`lookup("t", c)`, r("c", OperandFunction), true},
		{`$env.x + 1`, r("x", OperandArithmetic), true},
		{`$env["y"] > 0`, r("y", OperandCompare), true},
		{`let v = x; v * 2`, r("x", OperandArithmetic), true},
		{`(x ?? y) + 1`, r("x", OperandArithmetic, "y", OperandArithmetic), true},
		{`x != nil ? y * 2 : 0`, r("y", OperandArithmetic), true},
		{`x == nil ? x * 2 : 0`, r("x", OperandArithmetic), true},
		{`x > 1 && x != nil`, r("x", OperandCompare), true},
		{`x != nil || y > 0`, r("y", OperandCompare), true},
		{`x == nil && x > 0`, r("x", OperandCompare), true},
		{`(x * 2) ?? 0`, r("x", OperandArithmetic), true},
		{`(x * 2) == nil ? 0 : 1`, r("x", OperandArithmetic), true},
		{`z > 1 ? x + 1 : 0`, r("x", OperandArithmetic), true},
		{`x * y * x`, r("x", OperandArithmetic, "y", OperandArithmetic), true},
		{`map([1, 2], # + x)`, r("x", OperandArithmetic), true},
		{`sum([1, 2], x)`, r("x", OperandFunction), true},
		{`1; x > 0`, r("x", OperandCompare), true},
		// Silent: guards.
		{`x != nil ? x * 2 : 0`, nil, true},
		{`x == nil ? 0 : x * 2`, nil, true},
		{`nil != x ? -x : 0`, nil, true},
		{`!(x == nil) ? x + 1 : 0`, nil, true},
		{`x != nil && y != nil ? x + y : 0`, nil, true},
		{`x == nil || y == nil ? 0 : x * y`, nil, true},
		{`x != nil && x > 1`, nil, true},
		{`x == nil || x > 1`, nil, true},
		{`x != nil and y != nil and x < y`, nil, true},
		{`(x ?? 0) * 2`, nil, true},
		{`-(x ?? 1)`, nil, true},
		{`(x != nil ? x : 0) + 1`, nil, true},
		{`let v = x; v != nil ? v * 2 : 0`, nil, true},
		{`let v = x; x != nil ? v * 2 : 0`, nil, true},
		// Silent: nil-safe operators and functions.
		{`x`, nil, true},
		{`x == 1`, nil, true},
		{`x != 1`, nil, true},
		{`x in [1, 2]`, nil, true},
		{`"a" in s`, nil, true},
		{`c matches "^n"`, nil, true},
		{`c contains "o"`, nil, true},
		{`c startsWith "n"`, nil, true},
		{`+x`, nil, true},
		{`s?.[0]`, nil, true},
		{`x ?? 0`, nil, true},
		{`string(x)`, nil, true},
		{`sin(x)`, nil, true},
		{`max(x)`, nil, true},
		{`has_any(s, ["a"])`, nil, true},
		{`first(s)`, nil, true},
		{`total(x * 2)`, nil, true},
		{`embedder_fn(x)`, nil, true},
		// Silent: non-nullable reads, shadowing.
		{`z * 2`, nil, true},
		{`let x = 1; x * 2`, nil, true},
		{`(`, nil, false},
	}
	for _, c := range cases {
		reads, ok := NullableOperands(c.src, nullable)
		if ok != c.parsed || !reflect.DeepEqual(reads, c.reads) {
			t.Errorf("NullableOperands(%q) = %v, %v; want %v, %v", c.src, reads, ok, c.reads, c.parsed)
		}
	}
}

// TestNilRejectingArgument: the table's position and arity rules.
func TestNilRejectingArgument(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		arity, pos int
		want       bool
	}{
		{"abs", 1, 0, true},
		{"max", 1, 0, false},
		{"max", 2, 1, true},
		{"has_any", 2, 0, false},
		{"has_any", 2, 1, true},
		{"has_all", 3, 2, true},
		{"map", 2, 0, true},
		{"map", 2, 1, false},
		{"sortBy", 2, 1, true},
		{"sortBy", 3, 2, false},
		{"lookup", 2, 0, false},
		{"lookup", 3, 2, true},
		{"sin", 1, 0, false},
		{"embedder_fn", 1, 0, false},
	} {
		if got := NilRejectingArgument(c.name, c.arity, c.pos); got != c.want {
			t.Errorf("NilRejectingArgument(%s, %d, %d) = %v, want %v", c.name, c.arity, c.pos, got, c.want)
		}
	}
}
