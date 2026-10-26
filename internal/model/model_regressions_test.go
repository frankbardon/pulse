package model

import (
	"encoding/json"
	"slices"
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
