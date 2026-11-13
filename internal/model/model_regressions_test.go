package model

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestModelRegression_ShapeAndTerms: REG_MMM_HILL is a built-in type but
// not a coefficient-shaped one (BuiltinRegression false, ModelRegression
// true), so a bound fit names its own coefficients; its term catalog is
// k:<channel> then s:<channel> from params.channels, unknown for params
// that do not decode or name no channel, and absent for every other
// type.
func TestModelRegression_ShapeAndTerms(t *testing.T) {
	t.Parallel()

	for _, rt := range types.AllRegressionTypes() {
		if BuiltinRegression(rt) == ModelRegression(rt) {
			t.Errorf("%s: BuiltinRegression %v, ModelRegression %v — exactly one holds for a built-in type", rt, BuiltinRegression(rt), ModelRegression(rt))
		}
	}
	if !ModelRegression(types.REG_MMM_HILL) || BuiltinRegression(types.REG_MMM_HILL) {
		t.Fatal("REG_MMM_HILL is a model regression")
	}
	spec := func(rt types.RegressionType, params string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: rt, Params: json.RawMessage(params)}
	}
	got, ok := ModelRegressionTerms(spec(types.REG_MMM_HILL, `{"channels":["tv","search"],"nonnegative":false}`))
	if want := []string{"k:tv", "k:search", "s:tv", "s:search"}; !ok || !slices.Equal(got, want) {
		t.Fatalf("terms %v (%v), want %v", got, ok, want)
	}
	for _, params := range []string{``, `{}`, `{"channels":[]}`, `{"channels":"tv"}`, `not json`} {
		if got, ok := ModelRegressionTerms(spec(types.REG_MMM_HILL, params)); ok {
			t.Errorf("params %q: terms %v known, want unknown", params, got)
		}
	}
	if _, ok := ModelRegressionTerms(spec(types.REG_OLS, `{"channels":["tv"]}`)); ok {
		t.Error("REG_OLS has a term catalog")
	}
	if _, ok := ModelRegressionTerms(nil); ok {
		t.Error("a nil spec has a term catalog")
	}
}

// TestCatalogFromFits_ModelRegressionTerms: a `fits` entry of a model
// regression carries its catalogued coefficient names (sorted, known),
// so CheckCoef refuses an uncatalogued curve term and accepts every
// catalogued one; an entry whose catalog is unknown accepts any term
// (the runtime judges it).
func TestCatalogFromFits_ModelRegressionTerms(t *testing.T) {
	t.Parallel()

	fit := func(params string) types.FitSpec {
		return types.FitSpec{Name: "mmm", Request: &types.Request{Regressions: []*types.RegressionSpec{{
			Type: types.REG_MMM_HILL, Target: "y", Predictors: []string{"tv", "price"}, Params: json.RawMessage(params)}}}}
	}
	cat := CatalogFromFits([]types.FitSpec{fit(`{"channels":["tv"]}`)})
	sig, _ := cat.Signature("mmm")
	if !sig.CoefficientsKnown || !slices.Equal(sig.Coefficients, []string{"k:tv", "s:tv"}) {
		t.Fatalf("signature coefficients %v known %v", sig.Coefficients, sig.CoefficientsKnown)
	}
	for _, term := range []string{InterceptKey, "tv", "price", "k:tv", "s:tv"} {
		if cerr := CheckCoef(cat, "mmm", term, StatPoint); cerr != nil {
			t.Errorf("%s: %v", term, cerr)
		}
	}
	if cerr := CheckCoef(cat, "mmm", "k:price", StatPoint); cerr == nil || cerr.Code != "PULSE_MODEL_TERM_UNKNOWN" {
		t.Errorf("k:price: %v, want PULSE_MODEL_TERM_UNKNOWN", cerr)
	}
	open := CatalogFromFits([]types.FitSpec{fit(`{}`)})
	if cerr := CheckCoef(open, "mmm", "k:anything", StatPoint); cerr != nil {
		t.Errorf("unknown catalog: %v, want any term accepted", cerr)
	}
}

// TestModelRegression_BayesTermsAndInputs: REG_MMM_BAYES is a model
// regression whose term catalog is decay:<channel>, k:<channel>,
// s:<channel> (each over params.channels, in order) then sigma, and
// whose fields read beyond its target and predictors are params.time
// (and params.geo on a geo panel, whose term names are then unknown)
// (ModelRegressionFieldInputs, the projection's and predict's one
// source); every other type reads none.
func TestModelRegression_BayesTermsAndInputs(t *testing.T) {
	t.Parallel()

	if !ModelRegression(types.REG_MMM_BAYES) || BuiltinRegression(types.REG_MMM_BAYES) {
		t.Fatal("REG_MMM_BAYES is a model regression")
	}
	spec := func(rt types.RegressionType, params string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: rt, Params: json.RawMessage(params)}
	}
	got, ok := ModelRegressionTerms(spec(types.REG_MMM_BAYES, `{"channels":["tv","search"],"time":"week"}`))
	if want := []string{"decay:tv", "decay:search", "k:tv", "k:search", "s:tv", "s:search", "sigma"}; !ok || !slices.Equal(got, want) {
		t.Fatalf("terms %v (%v), want %v", got, ok, want)
	}
	for _, tc := range []struct{ kernel, want string }{
		{"geometric", "decay:tv decay:search k:tv k:search s:tv s:search sigma"},
		{"delayed", "decay:tv decay:search peak:tv peak:search k:tv k:search s:tv s:search sigma"},
		{"weibull_cdf", "shape:tv shape:search scale:tv scale:search k:tv k:search s:tv s:search sigma"},
		{"weibull_pdf", "shape:tv shape:search scale:tv scale:search k:tv k:search s:tv s:search sigma"},
	} {
		got, ok := ModelRegressionTerms(spec(types.REG_MMM_BAYES, `{"channels":["tv","search"],"time":"week","adstock":"`+tc.kernel+`"}`))
		if want := strings.Fields(tc.want); !ok || !slices.Equal(got, want) {
			t.Errorf("adstock %s: terms %v (%v), want %v", tc.kernel, got, ok, want)
		}
	}
	for _, params := range []string{``, `{}`, `{"channels":[]}`, `not json`, `{"channels":["tv"],"adstock":"hyperbolic"}`} {
		if got, ok := ModelRegressionTerms(spec(types.REG_MMM_BAYES, params)); ok {
			t.Errorf("params %q: terms %v known, want unknown", params, got)
		}
	}
	if got := ModelRegressionFieldInputs(spec(types.REG_MMM_BAYES, `{"channels":["tv"],"time":"week"}`)); !slices.Equal(got, []string{"week"}) {
		t.Errorf("Bayes field inputs %v, want [week]", got)
	}
	// A geo panel reads its geo field too, and its per-geo term names
	// are the data's: unknown statically.
	geo := spec(types.REG_MMM_BAYES, `{"channels":["tv"],"time":"week","geo":"market"}`)
	if got := ModelRegressionFieldInputs(geo); !slices.Equal(got, []string{"week", "market"}) {
		t.Errorf("Bayes geo field inputs %v, want [week market]", got)
	}
	if got, ok := ModelRegressionTerms(geo); ok {
		t.Errorf("geo panel terms %v known, want unknown", got)
	}
	for _, s := range []*types.RegressionSpec{
		spec(types.REG_MMM_BAYES, `{"channels":["tv"]}`),
		spec(types.REG_MMM_BAYES, `not json`),
		spec(types.REG_MMM_HILL, `{"channels":["tv"],"time":"week"}`),
		spec(types.REG_OLS, `{"time":"week"}`),
		nil,
	} {
		if got := ModelRegressionFieldInputs(s); got != nil {
			t.Errorf("%+v: field inputs %v, want none", s, got)
		}
	}
}

// TestModelRegression_OrdinalThresholdTerms: REG_ORDINAL's term catalog
// is its threshold names "<level>|<next level>" from params.levels, in
// order; without the list (the data order the levels) or with params
// that do not decode, the names are unknown.
func TestModelRegression_OrdinalThresholdTerms(t *testing.T) {
	t.Parallel()

	spec := func(params string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_ORDINAL, Params: json.RawMessage(params)}
	}
	got, ok := ModelRegressionTerms(spec(`{"levels":["low","mid","high"],"link":"probit"}`))
	if want := []string{"low|mid", "mid|high"}; !ok || !slices.Equal(got, want) {
		t.Fatalf("terms %v (%v), want %v", got, ok, want)
	}
	for _, params := range []string{``, `{}`, `{"levels":["only"]}`, `{"levels":"low"}`, `not json`} {
		if got, ok := ModelRegressionTerms(spec(params)); ok {
			t.Errorf("params %q: terms %v known, want unknown", params, got)
		}
	}
}
