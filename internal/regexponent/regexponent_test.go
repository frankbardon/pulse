package regexponent

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheck: exponentiate is admitted on REG_MULTINOMIAL and REG_ORDINAL
// only; on any other type it is PROCESSING_CONFIG {type, slot, requires,
// name}; an unset knob passes on every type.
func TestCheck(t *testing.T) {
	t.Parallel()

	for _, typ := range []types.RegressionType{types.REG_MULTINOMIAL, types.REG_ORDINAL} {
		if !Admits(typ) {
			t.Errorf("Admits(%s) = false", typ)
		}
		if cerr := Check(&types.RegressionSpec{Type: typ, Exponentiate: true}); cerr != nil {
			t.Errorf("%s refused: %v", typ, cerr)
		}
	}
	if Check(nil) != nil {
		t.Error("nil spec refused")
	}
	for _, typ := range []types.RegressionType{types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR, types.REG_MMM_HILL, types.REG_MMM_BAYES, "REG_ACME_X"} {
		if Admits(typ) {
			t.Errorf("Admits(%s) = true", typ)
		}
		if cerr := Check(&types.RegressionSpec{Type: typ}); cerr != nil {
			t.Errorf("%s without exponentiate refused: %v", typ, cerr)
		}
		cerr := Check(&types.RegressionSpec{Type: typ, Name: "fit", Exponentiate: true})
		if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG {
			t.Fatalf("%s: %v, want PROCESSING_CONFIG", typ, cerr)
		}
		want := map[string]any{"type": string(typ), "slot": "exponentiate", "requires": Requires, "name": "fit"}
		if !reflect.DeepEqual(cerr.Details, want) {
			t.Errorf("%s details %v, want %v", typ, cerr.Details, want)
		}
	}
}

// TestApply: the multinomial block exponentiates every key; the ordinal
// block the predictors only (no threshold key); exp(β)·SE and the
// exponentiated bounds ride exactly the keys the fit reports them on,
// a null SE / bound staying null; a spec without the knob, a refused
// type, a failed fit and a nil result are untouched.
func TestApply(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	res := func() *types.RegressionResult {
		return &types.RegressionResult{
			Coefficients: map[string]float64{"b:(intercept)": 0.5, "b:x1": -1.25, "c:x1": 0},
			StdErrors:    map[string]float64{"b:(intercept)": 0.2, "b:x1": nan, "c:x1": 0.1},
			CILower:      map[string]float64{"b:(intercept)": 0.1, "b:x1": nan},
			CIUpper:      map[string]float64{"b:(intercept)": 0.9, "b:x1": nan},
		}
	}
	mn := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Exponentiate: true}
	r := res()
	Apply(mn, r)
	e := r.Exponentiated
	if e == nil {
		t.Fatal("no exponentiated block")
	}
	for k, b := range r.Coefficients {
		if got := e.Estimates[k]; math.Abs(got-math.Exp(b)) > 1e-15*math.Exp(b) {
			t.Errorf("estimate %s = %v, want exp(%v)", k, got, b)
		}
	}
	if got, want := e.StdErrors["b:(intercept)"], math.Exp(0.5)*0.2; math.Abs(got-want) > 1e-15 {
		t.Errorf("se = %v, want %v", got, want)
	}
	if !math.IsNaN(e.StdErrors["b:x1"]) || !math.IsNaN(e.CILower["b:x1"]) || !math.IsNaN(e.CIUpper["b:x1"]) {
		t.Errorf("null SE / bounds not null: %+v", e)
	}
	if got := e.CILower["b:(intercept)"]; math.Abs(got-math.Exp(0.1)) > 1e-15 {
		t.Errorf("ci_lower = %v", got)
	}
	if _, ok := e.CILower["c:x1"]; ok {
		t.Error("ci_lower carries a key the fit's interval does not")
	}
	if len(e.StdErrors) != 3 || len(e.CILower) != 2 || len(e.CIUpper) != 2 {
		t.Errorf("keys %+v", e)
	}
	// Raw figures untouched.
	if !reflect.DeepEqual(r.Coefficients, res().Coefficients) {
		t.Error("coefficients moved")
	}

	ord := &types.RegressionSpec{Type: types.REG_ORDINAL, Predictors: []string{"x1", "x2"}, Exponentiate: true}
	r = &types.RegressionResult{
		Coefficients: map[string]float64{"x1": 0.3, "x2": -0.2, "1|2": -1, "2|3": 1},
		StdErrors:    map[string]float64{"x1": 0.1, "x2": 0.1, "1|2": 0.2, "2|3": 0.2},
	}
	Apply(ord, r)
	if got := r.Exponentiated; got == nil || len(got.Estimates) != 2 || len(got.StdErrors) != 2 || got.CILower != nil {
		t.Fatalf("ordinal block %+v, want the two predictors only", got)
	}
	if _, ok := r.Exponentiated.Estimates["1|2"]; ok {
		t.Error("a threshold was exponentiated")
	}

	finite := func() *types.RegressionResult {
		return &types.RegressionResult{Coefficients: map[string]float64{"b:x1": 0.5}, StdErrors: map[string]float64{"b:x1": 0.2}}
	}
	for name, tc := range map[string]struct {
		spec *types.RegressionSpec
		res  *types.RegressionResult
	}{
		"off":     {&types.RegressionSpec{Type: types.REG_MULTINOMIAL}, finite()},
		"refused": {&types.RegressionSpec{Type: types.REG_OLS, Exponentiate: true}, finite()},
		"failed":  {mn, &types.RegressionResult{Error: &types.SlotError{Code: "X"}}},
		"empty":   {mn, &types.RegressionResult{}},
	} {
		before, err := json.Marshal(tc.res)
		if err != nil {
			t.Fatal(err)
		}
		Apply(tc.spec, tc.res)
		if tc.res.Exponentiated != nil {
			t.Errorf("%s: block added", name)
		}
		if after, _ := json.Marshal(tc.res); string(after) != string(before) {
			t.Errorf("%s: result moved", name)
		}
	}
	Apply(mn, nil)
	Apply(nil, res())
}
