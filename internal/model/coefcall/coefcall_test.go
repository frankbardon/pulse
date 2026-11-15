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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// TestStaticRefusal_SweepAxis: all-literal sweep_axis() calls are
// judged beside coef() in walk order; SweepAxisRefusal judges them
// alone; a computed or wrong-arity call is left to evaluation.
func TestStaticRefusal_SweepAxis(t *testing.T) {
	t.Parallel()

	cat := model.FitCatalog{
		"best":  {Name: "best", Type: types.REG_OLS, Terms: []string{"x"}, SweepBound: true, SweepAxes: []string{"tv"}, SweepAxesKnown: true},
		"plain": {Name: "plain", Type: types.REG_OLS, Terms: []string{"x"}},
	}
	if got := LiteralSweepAxisCalls(`sweep_axis("best", "tv") + coef("best", "x") + sweep_axis(f, "tv") + sweep_axis("a")`); !reflect.DeepEqual(got, []SweepAxisCall{{"best", "tv"}}) {
		t.Fatalf("calls %v", got)
	}
	if cerr := StaticRefusal(`coef("best", "x") * sweep_axis("best", "tv")`, cat); cerr != nil {
		t.Fatalf("valid: %v", cerr)
	}
	cerr := StaticRefusal(`sweep_axis("plain", "tv") + coef("best", "nope")`, cat)
	if cerr == nil || cerr.Details["reason"] != model.ReasonNotSweepBound {
		t.Fatalf("walk order: %v", cerr)
	}
	if cerr := SweepAxisRefusal(`coef("best", "nope")`, cat); cerr != nil {
		t.Fatalf("SweepAxisRefusal judged coef(): %v", cerr)
	}
	if cerr := SweepAxisRefusal(`sweep_axis("best", "hi")`, cat); cerr == nil || cerr.Code != errors.PULSE_MODEL_TERM_UNKNOWN {
		t.Fatalf("SweepAxisRefusal: %v", cerr)
	}
	if got := LiteralFits(`coef("a", "x") + sweep_axis("b", "tv")`, false); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("LiteralFits(no coef) %v", got)
	}
	if got := LiteralFits(`coef("a", "x") + sweep_axis("b", "tv")`, true); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("LiteralFits %v", got)
	}
}

// TestStaticRefusal_Response: a response() call with a literal fit and
// channel is judged by model.CheckResponse beside coef() in walk order —
// whatever its spend — and by SweepAxisRefusal too (response is
// reserved); a computed fit or channel, or a wrong arity, is left to
// evaluation. Its fit is a read (NamesFit, LiteralFits).
func TestStaticRefusal_Response(t *testing.T) {
	t.Parallel()

	cat := model.FitCatalog{
		"mmm": {Name: "mmm", Type: types.REG_MMM_HILL, Terms: []string{"tv"}, Adstocks: []string{"tv"}, AdstocksKnown: true},
		"ols": {Name: "ols", Type: types.REG_OLS, Terms: []string{"x"}, Adstocks: []string{}, AdstocksKnown: true},
	}
	src := `response("mmm", "tv", spend * 2) + response(f, "tv", 1) + response("mmm", c, 1) + response("mmm", "tv")`
	if got := LiteralResponseCalls(src); !reflect.DeepEqual(got, []ResponseCall{{"mmm", "tv"}}) {
		t.Fatalf("calls %v", got)
	}
	if cerr := StaticRefusal(`response("mmm", "tv", x) + coef("mmm", "tv")`, cat); cerr != nil {
		t.Fatalf("valid: %v", cerr)
	}
	cerr := StaticRefusal(`response("ols", "x", 1) + coef("mmm", "nope")`, cat)
	if cerr == nil || cerr.Details["reason"] != model.ReasonAdstockUnknown || cerr.Details["channel"] != "x" {
		t.Fatalf("walk order: %v", cerr)
	}
	if cerr := SweepAxisRefusal(`response("nope", "tv", 1)`, cat); cerr == nil || cerr.Code != errors.PULSE_MODEL_UNKNOWN {
		t.Fatalf("SweepAxisRefusal: %v", cerr)
	}
	if !NamesFit(`1 + response("mmm", "tv", 3)`, "mmm") || NamesFit(`response(f, "tv", 3)`, "mmm") {
		t.Fatal("NamesFit")
	}
	if got := LiteralFits(`coef("a", "x") + response("b", "tv", 1)`, false); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("LiteralFits %v", got)
	}
}

// TestStaticRefusal_ResponseGeo: a response() call's literal geo is
// judged by model.CheckResponseGeo (a fit whose geos the catalog knows
// refuses another); a computed geo is the evaluation's, the call judged
// as a three-argument one.
func TestStaticRefusal_ResponseGeo(t *testing.T) {
	t.Parallel()

	cat := model.FitCatalog{
		"panel": {Name: "panel", Type: types.REG_MMM_BAYES, Adstocks: []string{"tv"}, AdstocksKnown: true, Geos: []string{"NE"}, GeosKnown: true},
		"open":  {Name: "open", Type: types.REG_MMM_BAYES, Adstocks: []string{"tv"}, AdstocksKnown: true},
	}
	if cerr := StaticRefusal(`response("panel", "tv", 1, "NE") + response("open", "tv", 1, "XX") + response("panel", "tv", 1, g)`, cat); cerr != nil {
		t.Fatalf("valid: %v", cerr)
	}
	cerr := StaticRefusal(`response("panel", "tv", 1, "SW")`, cat)
	if cerr == nil || cerr.Details["reason"] != model.ReasonGeoUnknown || cerr.Details["geo"] != "SW" {
		t.Fatalf("unknown geo: %v", cerr)
	}
	if cerr := SweepAxisRefusal(`response("panel", "tv", 1, "SW")`, cat); cerr == nil || cerr.Details["reason"] != model.ReasonGeoUnknown {
		t.Fatalf("SweepAxisRefusal: %v", cerr)
	}
	if cerr := StaticRefusal(`response("panel", "radio", 1, g)`, cat); cerr == nil || cerr.Details["reason"] != model.ReasonAdstockUnknown {
		t.Fatalf("computed geo, unknown channel: %v", cerr)
	}
}
