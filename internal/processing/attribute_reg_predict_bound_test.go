package processing

import (
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// TestRegPredict_BoundEqualsIntervalTimesColumn: a single-term
// lower / upper bound scores ci_lower / ci_upper × the column, bit for
// bit, a null column is a null score, and point is the coefficient.
func TestRegPredict_BoundEqualsIntervalTimesColumn(t *testing.T) {
	t.Parallel()

	set, res := bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}})
	cases := []struct {
		params string
		term   string
		coef   float64
	}{
		{`{"fit":"m","terms":["x1"],"bound":"lower"}`, "x1", res.CILower["x1"]},
		{`{"fit":"m","terms":["x1"],"bound":"upper"}`, "x1", res.CIUpper["x1"]},
		{`{"fit":"m","terms":["x2"],"bound":"lower"}`, "x2", res.CILower["x2"]},
		{`{"fit":"m","terms":["x2"],"bound":"point"}`, "x2", res.Coefficients["x2"]},
	}
	schema := predictSchema()
	for _, tc := range cases {
		t.Run(tc.params, func(t *testing.T) {
			if tc.coef == 0 || math.IsNaN(tc.coef) {
				t.Fatalf("fixture bound %v does not discriminate", tc.coef)
			}
			p := NewProcessor(schema)
			p.SetModels(set)
			recs := predictRecords(schema)
			if _, _, err := p.applyAttributes([]*types.Attribute{predictAttr(tc.params)}, recs); err != nil {
				t.Fatal(err)
			}
			for i, r := range recs {
				got, ok := r.NumericValue("p")
				if tc.term == "x2" && predictNullRows[i] {
					if ok || !r.IsNull("p") {
						t.Fatalf("row %d: null column scored %v", i, got)
					}
					continue
				}
				x, _ := r.NumericValue(tc.term)
				if want := tc.coef * x; !ok || math.Float64bits(got) != math.Float64bits(want) {
					t.Fatalf("row %d: %v, want %v", i, got, want)
				}
			}
		})
	}
}

// TestRegPredict_BoundAbsentIsNull: a term the fit reports no interval
// for (a lasso-dropped predictor) scores null on every row, never 0; a
// fit that reports no intervals at all is refused at bind.
func TestRegPredict_BoundAbsentIsNull(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x3"}}
	b, err := model.Bind("m", spec, &types.RegressionResult{Type: types.REG_OLS,
		Coefficients: map[string]float64{model.InterceptKey: 1, "x1": 2, "x3": 0},
		StdErrors:    map[string]float64{model.InterceptKey: 0.1, "x1": 0.2},
		CILower:      map[string]float64{model.InterceptKey: 0.8, "x1": 1.6},
		CIUpper:      map[string]float64{model.InterceptKey: 1.2, "x1": 2.4},
		ConfLevel:    0.95})
	if err != nil {
		t.Fatal(err)
	}
	set := &model.Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	schema := predictSchema()
	p := NewProcessor(schema)
	p.SetModels(set)
	resp, err := p.Process(context.Background(), &types.Request{
		Attributes:   []*types.Attribute{predictAttr(`{"fit":"m","terms":["x3"],"bound":"upper"}`)},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p", Label: "s"}},
	}, NewSliceIterator(predictRecords(schema)))
	if err != nil {
		t.Fatal(err)
	}
	if c := resp.Components.Aggregations[0]; c.N != 0 || c.NNull != 40 {
		t.Fatalf("n=%d n_null=%d, want every row null", c.N, c.NNull)
	}

	// A candidate the fit's selection dropped has no interval either.
	sel := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x3"}, Selection: "forward", Criterion: "aic"}
	sb, err := model.Bind("m", sel, &types.RegressionResult{Type: types.REG_OLS, SelectedFeatures: []string{"x1"},
		Coefficients: map[string]float64{model.InterceptKey: 1, "x1": 2},
		StdErrors:    map[string]float64{model.InterceptKey: 0.1, "x1": 0.2},
		CILower:      map[string]float64{model.InterceptKey: 0.8, "x1": 1.6},
		CIUpper:      map[string]float64{model.InterceptKey: 1.2, "x1": 2.4},
		ConfLevel:    0.95})
	if err != nil {
		t.Fatal(err)
	}
	selSet := &model.Set{}
	if err := selSet.Add(sb); err != nil {
		t.Fatal(err)
	}
	p = NewProcessor(schema)
	p.SetModels(selSet)
	resp, err = p.Process(context.Background(), &types.Request{
		Attributes:   []*types.Attribute{predictAttr(`{"fit":"m","terms":["x3"],"bound":"lower"}`)},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p", Label: "s"}},
	}, NewSliceIterator(predictRecords(schema)))
	if err != nil {
		t.Fatal(err)
	}
	if c := resp.Components.Aggregations[0]; c.N != 0 || c.NNull != 40 {
		t.Fatalf("selection-dropped: n=%d n_null=%d, want every row null", c.N, c.NNull)
	}

	bare, err := model.Bind("m", spec, &types.RegressionResult{Type: types.REG_OLS,
		Coefficients: map[string]float64{model.InterceptKey: 1, "x1": 2, "x3": 0}})
	if err != nil {
		t.Fatal(err)
	}
	bareSet := &model.Set{}
	if err := bareSet.Add(bare); err != nil {
		t.Fatal(err)
	}
	c, err := newRegPredictAttribute(predictAttr(`{"fit":"m","terms":["x1"],"bound":"lower"}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	var ce *perr.CodedError
	if err := BindAttribute(c, nil, bareSet); !stderrors.As(err, &ce) || !reflect.DeepEqual(ce, model.IntervalsUnavailable("m", "bound", "lower")) {
		t.Fatalf("no intervals: err = %#v", err)
	}
}

// TestRegPredict_BoundRefusals: a non-point bound over anything but one
// predictor term, or an unknown bound, is refused at bind.
func TestRegPredict_BoundRefusals(t *testing.T) {
	t.Parallel()

	set, _ := bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}})
	cases := map[string]string{
		`{"fit":"m","bound":"lower"}`:                         model.ReasonBoundTerms,
		`{"fit":"m","terms":["x1","x2"],"bound":"upper"}`:     model.ReasonBoundTerms,
		`{"fit":"m","terms":["(intercept)"],"bound":"lower"}`: model.ReasonBoundTerms,
		`{"fit":"m","terms":["x1"],"bound":"mid"}`:            model.ReasonBoundUnknown,
	}
	for params, reason := range cases {
		c, err := newRegPredictAttribute(predictAttr(params), predictSchema())
		if err != nil {
			t.Fatal(err)
		}
		var ce *perr.CodedError
		if err := BindAttribute(c, nil, set); !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_SCORE_INVALID || ce.Details["reason"] != reason {
			t.Fatalf("%s: err = %v, want reason %s", params, err, reason)
		}
	}
}
