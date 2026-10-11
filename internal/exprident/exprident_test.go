package exprident

import (
	"reflect"
	"testing"
)

func TestFree(t *testing.T) {
	cases := []struct {
		src     string
		want    []string
		usesEnv bool
		parsed  bool
	}{
		{`a + b * a`, []string{"a", "b"}, false, true},
		{`vmean(battery) > 3`, []string{"battery"}, false, true},
		{`let x = q1; x + q2`, []string{"q1", "q2"}, false, true},
		{`$env.score + $env["other"]`, []string{"score", "other"}, true, true},
		{`f(g(x))`, []string{"x"}, false, true},
		{`(`, nil, false, false},
	}
	for _, c := range cases {
		got, env, ok := Free(c.src)
		if ok != c.parsed || env != c.usesEnv || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Free(%q) = %v, %v, %v; want %v, %v, %v", c.src, got, env, ok, c.want, c.usesEnv, c.parsed)
		}
	}
}

func TestCallees(t *testing.T) {
	cases := []struct {
		src    string
		want   []string
		parsed bool
	}{
		{`vmean(battery) + sqrt(x) + vmean(y)`, []string{"vmean", "sqrt"}, true},
		{`len(xs) + abs(x)`, nil, true},
		{`has_any(tags, "a") && lookup("t", k) > 0`, []string{"has_any", "lookup"}, true},
		{`)`, nil, false},
	}
	for _, c := range cases {
		got, ok := Callees(c.src)
		if ok != c.parsed || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Callees(%q) = %v, %v; want %v, %v", c.src, got, ok, c.want, c.parsed)
		}
	}
}

func TestFirstCallee(t *testing.T) {
	isV := func(n string) bool { return n == "vmean" || n == "vsd" }
	cases := []struct{ src, want string }{
		{`sqrt(cell) + vsd(x) + vmean(y)`, "vsd"},
		{`sqrt(cell)`, ""},
		{`vmean(`, ""},
	}
	for _, c := range cases {
		if got := FirstCallee(c.src, isV); got != c.want {
			t.Errorf("FirstCallee(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}
