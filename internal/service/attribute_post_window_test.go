package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// attribute_post_window_test.go: the post-window attribute phase end to
// end (transformed-predictors E2-S1). A row-local attribute reading a
// window label — directly or through an earlier post-window attribute —
// runs after the windows over the window-copied records, its label read
// by a regression and carried on the output row; every other
// window-dependent attribute is PULSE_ATTRIBUTE_WINDOW_ORDER, refused by
// Process and predict alike.

func hill(x, k, s float64) float64 { return 1 / (1 + math.Pow(k/x, s)) }

// postWindowRequest: lag_x = WIN_LAG(x); dbl = 2·x (pre-window); sat =
// hill(lag_x, 3, 2) and sat2 = sat · dbl (post-window, sat2 through
// sat); REG_OLS y on sat.
func postWindowRequest() *types.Request {
	return &types.Request{
		Cohort:  &types.Cohort{Filename: "reg.pulse"},
		Windows: []*types.Window{lagX()},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "sat", Expression: "hill(lag_x, 3, 2)"},
			{Type: types.ATTR_FORMULA, Label: "dbl", Expression: "x * 2"},
			{Type: types.ATTR_FORMULA, Label: "sat2", Expression: "sat == nil ? nil : sat * dbl"},
		},
		Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"sat2"}}},
	}
}

// TestWindowPredictor_PostWindowAttributesRunAfterWindows: hill() over a
// window label and a formula over that post-window label evaluate on
// every record (null where the lag is null), ride the output rows, and
// feed the regression; predict accepts the same request.
func TestWindowPredictor_PostWindowAttributesRunAfterWindows(t *testing.T) {
	t.Parallel()

	svc, _, data := derivedRegCohort(t)
	req := postWindowRequest()
	if pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{}); len(pred.Errors) != 0 {
		t.Fatalf("predict refused: %+v", pred.Errors)
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 48 {
		t.Fatalf("%d rows, want 48", len(resp.Data))
	}
	for i, row := range resp.Data {
		lag, has := row["lag_x"].(float64)
		if !has {
			if _, ok := row["sat"]; ok {
				t.Fatalf("row %d: sat %v over a null lag", i, row["sat"])
			}
			if _, ok := row["sat2"]; ok {
				t.Fatalf("row %d: sat2 %v over a null sat", i, row["sat2"])
			}
			continue
		}
		want := hill(lag, 3, 2)
		if row["sat"] != want {
			t.Fatalf("row %d: sat %v, want hill(%v) = %v", i, row["sat"], lag, want)
		}
		if row["sat2"] != want*row["dbl"].(float64) {
			t.Fatalf("row %d: sat2 %v != sat·dbl %v", i, row["sat2"], want*row["dbl"].(float64))
		}
	}
	if r := onlyRegression(t, resp); r.NObs != 47 {
		t.Fatalf("n_obs %d, want 47 (the null first lag dropped listwise)", r.NObs)
	}
}

// TestWindowPredictor_PostWindowMaterializeMatchesProcess: materialize
// writes a post-window attribute's column — the same value Process puts
// on the output row.
func TestWindowPredictor_PostWindowMaterializeMatchesProcess(t *testing.T) {
	t.Parallel()

	svc, _, _ := derivedRegCohort(t)
	req := postWindowRequest()
	req.Regressions = nil
	sink := &valueSink{}
	if err := svc.Materialize(context.Background(), req, sink); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	col := -1
	for i, c := range sink.cols {
		if c.Name == "sat2" {
			col = i
		}
	}
	if col < 0 || len(sink.rows) != len(resp.Data) {
		t.Fatalf("columns %+v, %d rows; want sat2 over %d rows", sink.cols, len(sink.rows), len(resp.Data))
	}
	for i, row := range resp.Data {
		got, want := sink.rows[i][col], row["sat2"]
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("row %d: materialized %v, Process %v", i, got, want)
		}
	}
}

// TestWindowPredictor_AttributeWindowOrderParity: a window-dependent
// attribute that cannot run after the windows is
// PULSE_ATTRIBUTE_WINDOW_ORDER {attribute, field} — from Process and
// from predict, with the same message.
func TestWindowPredictor_AttributeWindowOrderParity(t *testing.T) {
	t.Parallel()

	svc, _, data := derivedRegCohort(t)
	cases := map[string]struct {
		req     *types.Request
		details map[string]any
	}{
		"not row-local": {&types.Request{
			Windows:    []*types.Window{lagX()},
			Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "lag_x", Label: "z"}},
		}, map[string]any{"attribute": "ATTR_ZSCORE", "field": "lag_x"}},
		"formula with total()": {&types.Request{
			Windows:    []*types.Window{lagX()},
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "share", Expression: "lag_x / total(lag_x)"}},
		}, map[string]any{"attribute": "ATTR_FORMULA", "field": "lag_x"}},
		"through a post-window label": {&types.Request{
			Windows: []*types.Window{lagX()},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "sat", Expression: "hill(lag_x, 3, 2)"},
				{Type: types.ATTR_ZSCORE, Field: "sat", Label: "z"},
			},
		}, map[string]any{"attribute": "ATTR_ZSCORE", "field": "sat"}},
		"aggregated shape": {&types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "s"}},
			Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "s", Label: "lag_s", OrderBy: []types.OrderKey{{Field: "s"}}}},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "lag_s * 2"}},
		}, map[string]any{"attribute": "ATTR_FORMULA", "field": "lag_s"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.req.Cohort = &types.Cohort{Filename: "reg.pulse"}
			_, err := svc.Process(context.Background(), c.req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_ATTRIBUTE_WINDOW_ORDER || !reflect.DeepEqual(ce.Details, c.details) {
				t.Fatalf("Process err = %v (%+v), want PULSE_ATTRIBUTE_WINDOW_ORDER %v", err, ce, c.details)
			}
			pred := descx.Predict(bytes.NewReader(data), c.req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message && reflect.DeepEqual(e.Details, ce.Details) {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v lack %s %q", pred.Errors, ce.Code, ce.Message)
			}
		})
	}
}
