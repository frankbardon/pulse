package model

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// scoreCatalog binds "ols" (x1, x2), "glm" (x1) and "ext" (an
// unscorable regression type).
func scoreCatalog() FitCatalog {
	return CatalogFromFits([]types.FitSpec{
		{Name: "ols", Request: olsReq(&types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}})},
		{Name: "glm", Request: olsReq(&types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x1"}})},
		{Name: "ext", Request: olsReq(&types.RegressionSpec{Type: "REG_X_CUSTOM", Target: "y", Predictors: []string{"x1"}})},
	})
}

func scoreAttr(params string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_REG_PREDICT, Params: json.RawMessage(params)}
}

// TestCheckScore: every refusal the scoring rule raises, in its order,
// with its code and reason, and the valid shapes it accepts.
func TestCheckScore(t *testing.T) {
	cases := []struct {
		name   string
		attr   *types.Attribute
		code   errors.Code
		reason string
	}{
		{"whole model", scoreAttr(`{"fit":"ols"}`), "", ""},
		{"terms with intercept", scoreAttr(`{"fit":"ols","terms":["x2","(intercept)"]}`), "", ""},
		{"glm link", scoreAttr(`{"fit":"glm","scale":"link"}`), "", ""},
		{"glm response", scoreAttr(`{"fit":"glm","scale":"response"}`), "", ""},
		{"params not an object", scoreAttr(`["ols"]`), errors.PULSE_MODEL_SCORE_INVALID, ReasonParamsInvalid},
		{"unknown params key", scoreAttr(`{"fit":"ols","bound":"lower"}`), errors.PULSE_MODEL_SCORE_INVALID, ReasonParamsInvalid},
		{"no params", &types.Attribute{Type: types.ATTR_REG_PREDICT}, errors.PULSE_MODEL_SCORE_INVALID, ReasonFitMissing},
		{"field on the slot", &types.Attribute{Type: types.ATTR_REG_PREDICT, Field: "x1", Params: json.RawMessage(`{"fit":"ols"}`)}, errors.PULSE_MODEL_SCORE_INVALID, ReasonSlotFields},
		{"predictors on the slot", &types.Attribute{Type: types.ATTR_REG_PREDICT, Predictors: []string{"x1"}, Params: json.RawMessage(`{"fit":"ols"}`)}, errors.PULSE_MODEL_SCORE_INVALID, ReasonSlotFields},
		{"unknown fit", scoreAttr(`{"fit":"nope"}`), errors.PULSE_MODEL_UNKNOWN, ""},
		{"unscorable type", scoreAttr(`{"fit":"ext"}`), errors.PULSE_MODEL_SCORE_INVALID, ReasonTypeUnsupported},
		{"empty terms", scoreAttr(`{"fit":"ols","terms":[]}`), errors.PULSE_MODEL_SCORE_INVALID, ReasonTermsEmpty},
		{"duplicate term", scoreAttr(`{"fit":"ols","terms":["x1","x1"]}`), errors.PULSE_MODEL_SCORE_INVALID, ReasonTermDuplicate},
		{"term not a predictor", scoreAttr(`{"fit":"ols","terms":["x3"]}`), errors.PULSE_MODEL_TERM_UNKNOWN, ""},
		{"unknown scale", scoreAttr(`{"fit":"glm","scale":"probability"}`), errors.PULSE_MODEL_SCORE_INVALID, ReasonScaleUnknown},
		{"scale on OLS", scoreAttr(`{"fit":"ols","scale":"response"}`), errors.PULSE_MODEL_SCORE_INVALID, ReasonScaleNotGLM},
	}
	cat := scoreCatalog()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := CheckScore(tc.attr, cat)
			if tc.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || err.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if tc.reason != "" && err.Details["reason"] != tc.reason {
				t.Fatalf("reason = %v, want %s", err.Details["reason"], tc.reason)
			}
		})
	}
	// The unknown-model refusal is UnknownModel's, listing the catalog.
	_, _, err := CheckScore(scoreAttr(`{"fit":"nope"}`), cat)
	if want := UnknownModel("nope", []string{"ext", "glm", "ols"}); !reflect.DeepEqual(err, want) {
		t.Fatalf("unknown fit = %#v, want %#v", err, want)
	}
	// A nil catalog binds nothing.
	_, _, err = CheckScore(scoreAttr(`{"fit":"ols"}`), nil)
	if want := UnknownModel("ols", nil); !reflect.DeepEqual(err, want) {
		t.Fatalf("nil catalog = %#v, want %#v", err, want)
	}
}

// TestSignature_RuntimeMatchesPredict: the runtime's Set and predict's
// FitCatalog give one fit the same Signature — under Selection too,
// where the bound predictors are a subset but the terms are the
// candidates the spec wrote — so both sides judge a reference alike.
func TestSignature_RuntimeMatchesPredict(t *testing.T) {
	for _, sel := range []string{"", "forward"} {
		spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b", "c"}, Selection: sel, Criterion: "aic"}
		res := &types.RegressionResult{Type: types.REG_OLS, SelectedFeatures: []string{"b"},
			Coefficients: map[string]float64{InterceptKey: 1, "a": 2, "b": 3, "c": 4}}
		b, err := Bind("m", spec, res)
		if err != nil {
			t.Fatal(err)
		}
		set := &Set{}
		if err := set.Add(b); err != nil {
			t.Fatal(err)
		}
		runtime, ok := set.Signature("m")
		if !ok {
			t.Fatal("bound name has no signature")
		}
		predict, _ := CatalogFromFits([]types.FitSpec{{Name: "m", Request: olsReq(spec)}}).Signature("m")
		if !reflect.DeepEqual(runtime, predict) {
			t.Fatalf("selection %q: runtime %+v, predict %+v", sel, runtime, predict)
		}
		if !reflect.DeepEqual(set.Names(), CatalogFromFits([]types.FitSpec{{Name: "m", Request: olsReq(spec)}}).Names()) {
			t.Fatal("names differ")
		}
	}
	var none *Set
	if _, ok := none.Signature("m"); ok {
		t.Fatal("nil set resolved a name")
	}
}

// TestDefaultScoreLabel: "ATTR_REG_PREDICT_<fit>".
func TestDefaultScoreLabel(t *testing.T) {
	if got := DefaultScoreLabel(scoreAttr(`{"fit":"mmm"}`)); got != "ATTR_REG_PREDICT_mmm" {
		t.Fatalf("label = %q", got)
	}
}
