package coefcall

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// TestLiteralCalls: only all-literal two- and three-argument coef()
// calls are collected (stat defaulting to point), nested ones included;
// a computed argument, another callee or a parse failure yields none.
func TestLiteralCalls(t *testing.T) {
	cases := []struct {
		src  string
		want []Call
	}{
		{`coef("m", "x") * tv / (1 + tv)`, []Call{{"m", "x", model.StatPoint}}},
		{`coef("m", "x", "lower") + coef("n", "(intercept)", "se")`, []Call{{"m", "x", "lower"}, {"n", "(intercept)", "se"}}},
		{`x > coef("m", "x", "upper") ? 1 : 0`, []Call{{"m", "x", "upper"}}},
		{`abs(coef("m", "x"))`, []Call{{"m", "x", model.StatPoint}}},
		{`coef(fit, "x")`, nil},
		{`let s = "se"; coef("m", "x", s)`, nil},
		{`coef("m")`, nil},
		{`coefficient("m", "x")`, nil},
		{`coef("m", "x" +`, nil},
	}
	for _, tc := range cases {
		if got := LiteralCalls(tc.src); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", tc.src, got, tc.want)
		}
	}
}

// TestStaticRefusal: the first failing literal call's CheckCoef
// refusal, nil when all pass.
func TestStaticRefusal(t *testing.T) {
	cat := model.CatalogFromFits([]types.FitSpec{{Name: "m", Request: &types.Request{
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}}}}})
	if err := StaticRefusal(`coef("m", "x", "se") + coef("m", "(intercept)")`, cat); err != nil {
		t.Fatalf("valid calls refused: %v", err)
	}
	cases := []struct {
		src  string
		code errors.Code
	}{
		{`coef("m", "x") + coef("nope", "x")`, errors.PULSE_MODEL_UNKNOWN},
		{`coef("m", "z")`, errors.PULSE_MODEL_TERM_UNKNOWN},
		{`coef("m", "x", "median") + coef("nope", "x")`, errors.PULSE_MODEL_STAT_UNKNOWN},
	}
	for _, tc := range cases {
		if err := StaticRefusal(tc.src, cat); err == nil || err.Code != tc.code {
			t.Errorf("%s: %v, want %s", tc.src, err, tc.code)
		}
	}
	if err := StaticRefusal(`coef("m", "x")`, nil); err == nil || err.Code != errors.PULSE_MODEL_UNKNOWN {
		t.Fatalf("nil catalog: %v", err)
	}
}

// TestNamesFit: a literal first argument of coef() or sweep_axis()
// naming the fit is a read; a computed name, another fit, another
// function or an unparsable source is not.
func TestNamesFit(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{`coef("best", "x") > 0`, true},
		{`1 + sweep_axis("best", "decay")`, true},
		{`abs(coef("best", "x", "se"))`, true},
		{`coef("other", "x")`, false},
		{`coef(params.fit, "x")`, false},
		{`hill(x, "best", 1)`, false},
		{`coef("best"`, false},
		{`x > 0`, false},
	}
	for _, tc := range cases {
		if got := NamesFit(tc.src, "best"); got != tc.want {
			t.Errorf("NamesFit(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}
