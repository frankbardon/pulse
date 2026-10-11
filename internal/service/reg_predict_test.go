package service

import (
	"context"
	"encoding/json"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// reg_predict_test.go pins ATTR_REG_PREDICT through the service: a slot
// scoring an in-compose fit equals ATTR_REG_FITTED's refit on the same
// rows bit for bit (serial and parallel compose), a holdout slot scores
// rows the fit never saw, and Materialize admits the bound score.

// scoreSlot scores fit "m" over weeks [lo, hi] beside an ATTR_REG_FITTED
// refit of the same spec, and counts the rows where the two agree
// exactly (eq) against the rows scored (n).
func scoreSlot(label, lo, hi string, fitted []string, spec types.RegressionSpec) *types.Request {
	return &types.Request{
		Label:     label,
		Cohort:    &types.Cohort{Filename: fitsCohort},
		Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{lo, hi}}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)},
			{Type: types.ATTR_REG_FITTED, Label: "f", Target: "y", Predictors: fitted, Penalty: spec.Penalty, Alpha: spec.Alpha, L1Ratio: spec.L1Ratio},
			{Type: types.ATTR_FORMULA, Label: "eq", Expression: "p == f ? 1 : 0"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "eq", Label: "eq"},
			{Type: types.AGG_COUNT, Field: "p", Label: "n"},
		},
	}
}

// TestRegPredict_ComposeEqualsFittedBitForBit: ATTR_REG_PREDICT against
// an in-compose fit equals ATTR_REG_FITTED on the fit's training rows
// on every row (unpenalized, ridge, elastic net, forward selection —
// FITTED given the selected features), under serial and parallel
// compose; the holdout slot scores the 13 unseen weeks.
func TestRegPredict_ComposeEqualsFittedBitForBit(t *testing.T) {
	svc := fitsService(t)
	cases := []struct {
		name string
		spec types.RegressionSpec
	}{
		{"ols", types.RegressionSpec{}},
		{"ridge", types.RegressionSpec{Penalty: "l2", Alpha: 0.7}},
		{"elasticnet", types.RegressionSpec{Penalty: "elasticnet", Alpha: 0.02, L1Ratio: 0.4}},
		{"forward selection", types.RegressionSpec{Selection: "forward", Criterion: "aic"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Type, spec.Name, spec.Target, spec.Predictors = types.REG_OLS, "m", "y", []string{"x", "week"}
			fit := fitRequest("0", "90")
			fit.Regressions = []*types.RegressionSpec{&spec}
			fitted := spec.Predictors
			if spec.Selection != "" {
				first, err := svc.Compose(context.Background(), &types.ComposedRequest{
					Fits: []types.FitSpec{{Name: "m", Request: fit}}, Requests: []*types.Request{fitsSlot("s", "0", "90")}})
				if err != nil {
					t.Fatal(err)
				}
				fitted = first.Fits["m"].SelectedFeatures
				if len(fitted) == 0 {
					t.Fatal("selection kept no predictor")
				}
			}
			c := &types.ComposedRequest{
				Fits: []types.FitSpec{{Name: "m", Request: fit}},
				Requests: []*types.Request{
					scoreSlot("train", "0", "90", fitted, spec),
					{Label: "holdout", Cohort: &types.Cohort{Filename: fitsCohort},
						Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{"91", "103"}}},
						Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
						Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "p", Label: "n"}}},
				},
			}
			for mode, out := range composeBoth(t, svc, c) {
				train := out.Responses[0].Data[0]
				if train["n"] != float64(91) || train["eq"] != float64(91) {
					t.Fatalf("%s: train rows scored %v, equal to ATTR_REG_FITTED %v; want 91 / 91", mode, train["n"], train["eq"])
				}
				if got := out.Responses[1].Data[0]["n"]; got != float64(13) {
					t.Fatalf("%s: holdout rows scored %v, want 13", mode, got)
				}
			}
		})
	}
}

// valueSink keeps a materialize run's derived values.
type valueSink struct {
	cols []descx.MaterializeColumn
	rows [][]any
}

func (v *valueSink) Begin(_ *Cohort, cols []descx.MaterializeColumn) error {
	v.cols = cols
	return nil
}

func (v *valueSink) Row(_ int64, derived []any) error {
	v.rows = append(v.rows, derived)
	return nil
}

// TestRegPredict_MaterializeAdmitsBoundScore: Materialize writes a bound
// model's score as a derived column — one value per record — equal to
// the score a Process slot reads.
func TestRegPredict_MaterializeAdmitsBoundScore(t *testing.T) {
	svc := fitsService(t)
	ctx := context.Background()
	set, _, _, err := svc.runComposeFits(ctx, []types.FitSpec{{Name: "m", Request: fitRequest("0", "90")}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx = model.WithSet(ctx, set)
	req := &types.Request{
		Cohort:     &types.Cohort{Filename: fitsCohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
	}
	sink := &valueSink{}
	if err := svc.Materialize(ctx, req, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.cols) != 1 || sink.cols[0].Name != "p" || len(sink.rows) != 104 {
		t.Fatalf("columns %+v, %d rows; want p over 104 rows", sink.cols, len(sink.rows))
	}
	var sum float64
	for i, r := range sink.rows {
		v, ok := r[0].(float64)
		if !ok {
			t.Fatalf("row %d: %v is not a score", i, r[0])
		}
		sum += v
	}
	resp, err := svc.Process(ctx, &types.Request{
		Cohort:       &types.Cohort{Filename: fitsCohort},
		Attributes:   req.Attributes,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p", Label: "s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"]; got != sum {
		t.Fatalf("Process sum %v != materialized sum %v", got, sum)
	}
}
