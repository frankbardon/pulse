package regbounds

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func bounded(mut func(*types.RegressionSpec)) *types.RegressionSpec {
	s := &types.RegressionSpec{
		Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b"},
		Bounds: &types.RegressionBounds{Nonnegative: []string{"a"}},
	}
	if mut != nil {
		mut(s)
	}
	return s
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name  string
		spec  *types.RegressionSpec
		code  errors.Code
		field string
	}{
		{"nil", nil, "", ""},
		{"absent", bounded(func(s *types.RegressionSpec) { s.Bounds = nil }), "", ""},
		{"empty list", bounded(func(s *types.RegressionSpec) { s.Bounds.Nonnegative = nil }), "", ""},
		{"ols", bounded(nil), "", ""},
		{"ols every predictor", bounded(func(s *types.RegressionSpec) { s.Bounds.Nonnegative = []string{"b", "a"} }), "", ""},
		{"ols weighted resample", bounded(func(s *types.RegressionSpec) { s.Resample = "bootstrap" }), "", ""},
		{"unknown name", bounded(func(s *types.RegressionSpec) { s.Bounds.Nonnegative = []string{"zz"} }), errors.PROCESSING_CONFIG, "zz"},
		{"target is not a predictor", bounded(func(s *types.RegressionSpec) { s.Bounds.Nonnegative = []string{"y"} }), errors.PROCESSING_CONFIG, "y"},
		{"duplicate", bounded(func(s *types.RegressionSpec) { s.Bounds.Nonnegative = []string{"a", "a"} }), errors.PROCESSING_CONFIG, "a"},
		{"ridge", bounded(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }), errors.PROCESSING_CONFIG, ""},
		{"lasso", bounded(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.1 }), errors.PROCESSING_CONFIG, ""},
		{"selection", bounded(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }), errors.PROCESSING_CONFIG, ""},
		{"glm", bounded(func(s *types.RegressionSpec) { s.Type, s.Family = types.REG_GLM, "binomial" }), errors.PROCESSING_CONFIG, ""},
		{"bayes", bounded(func(s *types.RegressionSpec) { s.Type = types.REG_BAYES_LINEAR }), errors.PROCESSING_CONFIG, ""},
		// An extension model's shared-slot table refuses the slot.
		{"extension", bounded(func(s *types.RegressionSpec) { s.Type = "REG_ACME_FIT" }), "", ""},
	}
	for _, c := range cases {
		got := Check(c.spec)
		if c.code == "" {
			if got != nil {
				t.Errorf("%s: refused %v", c.name, got)
			}
			continue
		}
		if got == nil || got.Code != c.code {
			t.Errorf("%s: got %v, want %s", c.name, got, c.code)
			continue
		}
		if c.field != "" && got.Details["field"] != c.field {
			t.Errorf("%s: details.field %v, want %s", c.name, got.Details["field"], c.field)
		}
	}
}

func TestMask(t *testing.T) {
	spec := bounded(nil)
	if got := Mask(spec, []string{"a", "b"}); !reflect.DeepEqual(got, []bool{true, false}) {
		t.Errorf("mask = %v", got)
	}
	// A predictor subset keeps each name's own rule; a subset with no
	// bounded name is the plain solve.
	if got := Mask(spec, []string{"b", "a"}); !reflect.DeepEqual(got, []bool{false, true}) {
		t.Errorf("reordered mask = %v", got)
	}
	if got := Mask(spec, []string{"b"}); got != nil {
		t.Errorf("unbounded subset mask = %v, want nil", got)
	}
	if got := Mask(bounded(func(s *types.RegressionSpec) { s.Bounds = nil }), []string{"a"}); got != nil {
		t.Errorf("absent bounds mask = %v, want nil", got)
	}
}

func TestInferenceUndefinedAndAdvisory(t *testing.T) {
	spec := bounded(nil)
	if InferenceUndefined(spec, nil) {
		t.Error("no active bound: inference is the plain fit's")
	}
	if !InferenceUndefined(spec, []string{"a"}) {
		t.Error("an active bound without resample nulls the inference")
	}
	if InferenceUndefined(bounded(func(s *types.RegressionSpec) { s.Resample = "bootstrap" }), []string{"a"}) {
		t.Error("resample replaces the inference")
	}
	msg, details := Advisory(spec, "regressions[0]", []string{"a"})
	if msg == "" || details["slot"] != "regressions[0]" || details["operator"] != "REG_OLS" ||
		!reflect.DeepEqual(details["active"], []string{"a"}) ||
		!reflect.DeepEqual(details["suggested"], map[string]any{"resample": "bootstrap"}) {
		t.Errorf("advisory = %q %v", msg, details)
	}
}
