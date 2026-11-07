package exprident

import (
	"reflect"
	"testing"
)

// TestNullableResult: a nullable read or a literal nil reaching the
// result position is reported; `??` with a non-nil fallback and a
// `== nil` / `!= nil` ternary guard suppress it; an operator over a
// null (which the runtime refuses, never nulls) and a non-nullable
// read never report.
func TestNullableResult(t *testing.T) {
	t.Parallel()

	nullable := func(name string) bool { return name == "x" || name == "y" }
	cases := []struct {
		src        string
		fields     []string
		literalNil bool
		parsed     bool
	}{
		// Fires.
		{`x`, []string{"x"}, false, true},
		{`z > 0 ? w : nil`, nil, true, true},
		{`z > 0 ? x : 0`, []string{"x"}, false, true},
		{`x ?? y`, []string{"x", "y"}, false, true},
		{`x ?? nil`, []string{"x"}, true, true},
		{`$env.x`, []string{"x"}, false, true},
		{`$env["y"]`, []string{"y"}, false, true},
		{`let v = x; v`, []string{"x"}, false, true},
		{`x == nil ? y : x`, []string{"y"}, false, true},
		{`x != nil ? y : 0`, []string{"y"}, false, true},
		{`x == nil ? nil : x`, nil, true, true},
		{`x != nil && z > 0 ? y : 0`, []string{"y"}, false, true},
		{`1; x`, []string{"x"}, false, true},
		// Silent.
		{`x ?? 0`, nil, false, true},
		{`x == nil ? 0 : x`, nil, false, true},
		{`nil == x ? 0 : x`, nil, false, true},
		{`x != nil ? x : 0`, nil, false, true},
		{`!(x == nil) ? x : 0`, nil, false, true},
		{`x != nil && y != nil ? x + y : 0`, nil, false, true},
		{`x != nil and y != nil ? (z > 0 ? x : y) : 0`, nil, false, true},
		{`x == nil || y == nil ? 0 : y`, nil, false, true},
		{`x * 2`, nil, false, true},
		{`z`, nil, false, true},
		{`vmean(x)`, nil, false, true},
		{`let x = 1; x`, nil, false, true},
		{`(`, nil, false, false},
	}
	for _, c := range cases {
		fields, lit, ok := NullableResult(c.src, nullable)
		if ok != c.parsed || lit != c.literalNil || !reflect.DeepEqual(fields, c.fields) {
			t.Errorf("NullableResult(%q) = %v, %v, %v; want %v, %v, %v", c.src, fields, lit, ok, c.fields, c.literalNil, c.parsed)
		}
	}
}
