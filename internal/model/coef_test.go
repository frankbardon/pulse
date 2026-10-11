package model

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// coefSet binds "m" (OLS over a, b, c with intervals; c has no
// interval, as a lasso-dropped predictor would not), "sel" (selection
// over a, b keeping b) and "bare" (a fit with no standard errors).
func coefSet(t *testing.T) *Set {
	t.Helper()
	s := &Set{}
	add := func(name string, spec *types.RegressionSpec, res *types.RegressionResult) {
		b, err := Bind(name, spec, res)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	add("m", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b", "c"}},
		&types.RegressionResult{Type: types.REG_OLS,
			Coefficients: map[string]float64{InterceptKey: 1.25, "a": 0.1 + 0.2, "b": -3.5, "c": 7},
			StdErrors:    map[string]float64{InterceptKey: 0.5, "a": 0.03, "b": 0.75},
			CILower:      map[string]float64{InterceptKey: 0.2, "a": 0.24, "b": -5},
			CIUpper:      map[string]float64{InterceptKey: 2.3, "a": 0.36, "b": -2},
			ConfLevel:    0.95})
	add("sel", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b"}, Selection: "forward"},
		&types.RegressionResult{Type: types.REG_OLS, SelectedFeatures: []string{"b"},
			Coefficients: map[string]float64{InterceptKey: 2, "b": 4},
			StdErrors:    map[string]float64{InterceptKey: 1, "b": 1},
			CILower:      map[string]float64{InterceptKey: 0, "b": 2},
			CIUpper:      map[string]float64{InterceptKey: 4, "b": 6},
			ConfLevel:    0.9})
	add("bare", &types.RegressionSpec{Type: types.REG_GLM, Target: "y"},
		&types.RegressionResult{Type: types.REG_GLM, Coefficients: map[string]float64{InterceptKey: 0.5}, StdErrors: map[string]float64{}})
	return s
}

// TestCoef: every stat of every term at the result's own float64 —
// bit-equal, never re-derived — NaN (never 0) where the fit reports no
// figure for the term, and 0 for a selection-dropped candidate's point.
func TestCoef(t *testing.T) {
	s := coefSet(t)
	cases := []struct {
		fit, term, stat string
		want            float64
	}{
		{"m", InterceptKey, StatPoint, 1.25},
		{"m", InterceptKey, StatLower, 0.2},
		{"m", InterceptKey, StatUpper, 2.3},
		{"m", InterceptKey, StatSE, 0.5},
		{"m", "a", StatPoint, 0.1 + 0.2},
		{"m", "a", StatLower, 0.24},
		{"m", "a", StatUpper, 0.36},
		{"m", "a", StatSE, 0.03},
		{"m", "b", StatPoint, -3.5},
		{"m", "c", StatPoint, 7},
		{"m", "c", StatLower, math.NaN()},
		{"m", "c", StatSE, math.NaN()},
		{"sel", "b", StatUpper, 6},
		{"sel", "a", StatPoint, 0},
		{"sel", "a", StatLower, math.NaN()},
		{"bare", InterceptKey, StatPoint, 0.5},
	}
	for _, tc := range cases {
		got, err := Coef(s, tc.fit, tc.term, tc.stat)
		if err != nil {
			t.Fatalf("coef(%s, %s, %s): %v", tc.fit, tc.term, tc.stat, err)
		}
		if math.Float64bits(got) != math.Float64bits(tc.want) && !(math.IsNaN(got) && math.IsNaN(tc.want)) {
			t.Errorf("coef(%s, %s, %s) = %v, want %v", tc.fit, tc.term, tc.stat, got, tc.want)
		}
	}
}

// TestCoef_Refusals: the coded refusals in rule order — unknown fit,
// unknown term, unknown stat — and, at runtime only, an interval or
// standard error read from a fit that reports none.
func TestCoef_Refusals(t *testing.T) {
	s := coefSet(t)
	cases := []struct {
		name            string
		fit, term, stat string
		want            *errors.CodedError
	}{
		{"unknown fit", "nope", "a", StatPoint, UnknownModel("nope", []string{"bare", "m", "sel"})},
		{"unknown fit wins over the stat", "nope", "zz", "median", UnknownModel("nope", []string{"bare", "m", "sel"})},
		{"unknown term", "m", "zz", StatPoint, TermUnknown("m", "zz", []string{"a", "b", "c"})},
		{"unknown term wins over the stat", "m", "zz", "median", TermUnknown("m", "zz", []string{"a", "b", "c"})},
		{"unknown stat", "m", "a", "median", StatUnknown("m", "median")},
		{"empty stat", "m", "a", "", StatUnknown("m", "")},
		{"lower without intervals", "bare", InterceptKey, StatLower, IntervalsUnavailable("bare", "stat", StatLower)},
		{"upper without intervals", "bare", InterceptKey, StatUpper, IntervalsUnavailable("bare", "stat", StatUpper)},
		{"se without standard errors", "bare", InterceptKey, StatSE, IntervalsUnavailable("bare", "stat", StatSE)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Coef(s, tc.fit, tc.term, tc.stat)
			if !reflect.DeepEqual(err, tc.want) {
				t.Fatalf("err = %#v\nwant %#v", err, tc.want)
			}
			if !math.IsNaN(v) {
				t.Fatalf("refused call returned %v, want NaN", v)
			}
		})
	}
	if _, err := Coef(nil, "m", "a", StatPoint); !reflect.DeepEqual(err, UnknownModel("m", nil)) {
		t.Fatalf("nil set = %#v", err)
	}
	if got := StatUnknown("m", "x"); got.Code != errors.PULSE_MODEL_STAT_UNKNOWN || !reflect.DeepEqual(got.Details["valid"], CoefStats()) {
		t.Fatalf("stat unknown = %#v", got)
	}
	if got := IntervalsUnavailable("m", "bound", BoundLower); got.Code != errors.PULSE_MODEL_INTERVALS_UNAVAILABLE || got.Details["bound"] != BoundLower {
		t.Fatalf("intervals unavailable = %#v", got)
	}
}

// TestCheckCoef_RuntimeMatchesPredict: the static rule refuses a call
// against predict's FitCatalog exactly as against the runtime's Set —
// the parity the field-reference pass relies on.
func TestCheckCoef_RuntimeMatchesPredict(t *testing.T) {
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b"}}
	b, err := Bind("m", spec, &types.RegressionResult{Coefficients: map[string]float64{InterceptKey: 1, "a": 2, "b": 3}})
	if err != nil {
		t.Fatal(err)
	}
	set := &Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	cat := CatalogFromFits([]types.FitSpec{{Name: "m", Request: olsReq(spec)}})
	for _, call := range [][3]string{{"m", "a", StatPoint}, {"x", "a", StatPoint}, {"m", "z", StatSE}, {"m", InterceptKey, "mean"}} {
		rt, pr := CheckCoef(set, call[0], call[1], call[2]), CheckCoef(cat, call[0], call[1], call[2])
		if !reflect.DeepEqual(rt, pr) {
			t.Errorf("%v: runtime %#v, predict %#v", call, rt, pr)
		}
	}
	if err := CheckCoef(nil, "m", "a", StatPoint); !reflect.DeepEqual(err, UnknownModel("m", nil)) {
		t.Fatalf("nil catalog = %#v", err)
	}
}
