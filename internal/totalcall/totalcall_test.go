package totalcall

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestContains: only a call whose callee is the bare identifier total
// counts — a field named total, a member call and an unparsable source
// do not.
func TestContains(t *testing.T) {
	cases := map[string]bool{
		`x / total(x)`: true,
		`x * (1 - 1000 / total(w >= 52 ? x : 0))`: true,
		`x | total()`: true, // pipe desugars to total(x)
		`total + 1`:   false,
		`a.total(x)`:  false,
		`totals(x)`:   false,
		`sum([x])`:    false,
		`total(x`:     false,
		`x * 2`:       false,
	}
	for src, want := range cases {
		if got := Contains(src); got != want {
			t.Errorf("Contains(%q) = %v, want %v", src, got, want)
		}
	}
}

// TestArgs: distinct one-argument calls, keyed by the printed argument,
// in walk order.
func TestArgs(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{`x / total(x)`, []string{"x"}},
		{`total(x) + total(x) * total(y * 2)`, []string{"x", "y * 2"}},
		{`total(w >= 52 ? x : 0)`, []string{"w >= 52 ? x : 0"}},
		{`total()`, nil},
		{`x * 2`, nil},
		{`total(x`, nil},
	}
	for _, tc := range cases {
		if got := Args(tc.src); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Args(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestRefusal: the site rule, then nesting and arity inside
// ATTR_FORMULA.
func TestRefusal(t *testing.T) {
	cases := []struct {
		src, site string
		want      errors.Code // "" = admitted
	}{
		{`x / total(x)`, FormulaSite, ""},
		{`total + 1`, "FILTER_EXPRESSION", ""},
		{`x > 1`, "FILTER_EXPRESSION", ""},
		{`total(x) > 0`, "FILTER_EXPRESSION", errors.PULSE_FORMULA_TOTAL_SITE},
		{`value / total(value)`, "OVERLAY_FORMULA", errors.PULSE_FORMULA_TOTAL_SITE},
		{`total(x * total(y))`, FormulaSite, errors.PULSE_FORMULA_TOTAL_NESTED},
		{`total()`, FormulaSite, errors.PULSE_FORMULA_TOTAL_ARITY},
		{`total(x, y)`, FormulaSite, errors.PULSE_FORMULA_TOTAL_ARITY},
		{`total(x`, FormulaSite, ""},
	}
	for _, tc := range cases {
		cerr := Refusal(tc.src, tc.site)
		switch {
		case tc.want == "" && cerr != nil:
			t.Errorf("Refusal(%q, %s) = %v, want nil", tc.src, tc.site, cerr)
		case tc.want != "" && (cerr == nil || cerr.Code != tc.want):
			t.Errorf("Refusal(%q, %s) = %v, want %s", tc.src, tc.site, cerr, tc.want)
		}
	}
	if cerr := Refusal(`total(x) > 0`, "FILTER_EXPRESSION"); cerr.Details["site"] != "FILTER_EXPRESSION" {
		t.Errorf("site detail: %v", cerr.Details)
	}
}
