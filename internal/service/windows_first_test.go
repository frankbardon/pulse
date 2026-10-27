package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// windows_first_test.go: the ungrouped windows-first arm end to end
// (mmm-followups E4-S2) — predict admits exactly what Process runs, and
// refuses exactly what Process refuses, with the same code. Kept small:
// the engine's own behaviour is pinned in internal/processing.

// TestWindowsFirst_PredictMatchesRuntime: an ungrouped request whose
// aggregations read a window label (directly, through a column param,
// through a post-window formula, beside a regression on it, under a
// slot weight) runs and walks clean; a request weight (the windows have
// no weighted form), a window-label weight, a sort on
// a window label over the aggregation row and a window reading an
// aggregation label are refused by both with one code.
func TestWindowsFirst_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	svc, _, data := derivedRegCohort(t)
	sum := func(field, label string) *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_SUM, Field: field, Label: label}
	}
	win := func() []*types.Window { return []*types.Window{lagX()} }
	cases := []struct {
		name string
		req  *types.Request
		code errors.Code // "" = admitted
	}{
		{"sum over a window label", &types.Request{Windows: win(), Aggregations: []*types.Aggregation{sum("lag_x", "s")}}, ""},
		{"column param", &types.Request{Windows: win(), Aggregations: []*types.Aggregation{{Type: types.AGG_RATIO, Field: "lag_x", Label: "r",
			Params: []byte(`{"numerator_field":"lag_x","denominator_field":"y"}`)}}}, ""},
		{"post-window formula", &types.Request{Windows: win(),
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "sat", Expression: "(lag_x ?? 0) * 2"}},
			Aggregations: []*types.Aggregation{sum("sat", "s")}}, ""},
		{"regression beside", &types.Request{Windows: win(), Aggregations: []*types.Aggregation{sum("lag_x", "s")},
			Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"lag_x"}}}}, ""},
		{"slot weight", &types.Request{Windows: win(),
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "lag_x", Label: "m", Weight: types.SlotWeightField("w")}}}, ""},
		// A request weight reaches the windows, which have no weighted
		// form — refused on both sides, windows-first or not.
		{"request weight", &types.Request{Windows: win(), Weight: &types.WeightSpec{Field: "w"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "lag_x", Label: "m"}}}, errors.PROCESSING_CONFIG},
		{"window-label weight", &types.Request{Windows: win(), Weight: &types.WeightSpec{Field: "lag_x"},
			Aggregations: []*types.Aggregation{sum("lag_x", "s")}}, errors.SERVICE_VALIDATION},
		{"sort on a window label", &types.Request{Windows: win(), Aggregations: []*types.Aggregation{sum("lag_x", "s")},
			Sort: []types.OrderKey{{Field: "lag_x"}}}, errors.SERVICE_VALIDATION},
		{"window reading an aggregation", &types.Request{
			Windows:      []*types.Window{lagX(), {Type: types.WIN_LAG, Field: "s_y", Label: "lag_s", OrderBy: []types.OrderKey{{Field: "t"}}}},
			Aggregations: []*types.Aggregation{sum("lag_x", "s"), sum("y", "s_y")}}, errors.PULSE_WINDOW_INVALID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.req.Cohort = &types.Cohort{Filename: "reg.pulse"}
			pred := descx.Predict(bytes.NewReader(data), c.req, &descx.PredictOptions{})
			resp, err := svc.Process(context.Background(), c.req)
			if c.code == "" {
				if len(pred.Errors) != 0 || err != nil {
					t.Fatalf("predict errors %+v, Process err %v; want both admitted", pred.Errors, err)
				}
				if pr, _ := pred.Data.(*descriptor.PredictResult); pr == nil || pr.Streamable {
					t.Fatal("predict reports a windows-first request streamable")
				}
				if len(resp.Data) != 1 {
					t.Fatalf("rows = %v, want the one aggregation row", resp.Data)
				}
				return
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("Process err = %v, want %s", err, c.code)
			}
			if len(pred.Errors) == 0 || pred.Errors[0].Code != string(c.code) {
				t.Fatalf("predict errors %+v, want %s", pred.Errors, c.code)
			}
		})
	}
}

// TestWindowsFirst_ServiceSumsLaggedColumn: through the service (the
// projected buffered decode included) AGG_SUM over WIN_LAG(x) is the sum
// of every x but the last, with the one null lag counted.
func TestWindowsFirst_ServiceSumsLaggedColumn(t *testing.T) {
	t.Parallel()

	svc, _, _ := derivedRegCohort(t)
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: "reg.pulse"},
		Windows:      []*types.Window{lagX()},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "lag_x", Label: "s"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	var want float64
	for i := 0; i < 47; i++ {
		want += 0.5 + float64(i%9) + 0.25*math.Sin(float64(i))
	}
	if got := resp.Data[0]["s"].(float64); math.Abs(got-want) > 1e-9 {
		t.Fatalf("sum of lags %v, want %v", got, want)
	}
	if c := resp.Components.Aggregations[0]; c.N != 47 || c.NNull != 1 {
		t.Fatalf("floor {n %d, n_null %d}, want {47, 1}", c.N, c.NNull)
	}
}

// TestWindowsFirst_ServiceGroupedAndCrosstab: through the service (the
// projected buffered decode, and the crosstab dispatch the fusion gate
// declines) a grouped AGG_SUM over WIN_LAG(x) and a crosstab of it
// partition the same lag total the ungrouped sum reports; predict admits
// both, buffered and unfused.
func TestWindowsFirst_ServiceGroupedAndCrosstab(t *testing.T) {
	t.Parallel()

	svc, _, data := derivedRegCohort(t)
	var want float64
	for i := 0; i < 47; i++ {
		want += 0.5 + float64(i%9) + 0.25*math.Sin(float64(i))
	}
	tBucket := func() *types.Group { return &types.Group{Type: types.GROUP_RANGE, Field: "t", Interval: 12} }
	for name, req := range map[string]*types.Request{
		"grouped": {Windows: []*types.Window{lagX()}, Groups: []*types.Group{tBucket()},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "lag_x", Label: "s"}}},
		"crosstab": {Windows: []*types.Window{lagX()}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{tBucket()},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "w", Interval: 1}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "lag_x", Label: "c"},
			Margins: types.CrosstabMargins{Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		}},
	} {
		t.Run(name, func(t *testing.T) {
			req.Cohort = &types.Cohort{Filename: "reg.pulse"}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			pr, _ := pred.Data.(*descriptor.PredictResult)
			if len(pred.Errors) != 0 || pr == nil || pr.Streamable || (req.Crosstab != nil && (pr.CrosstabFusable == nil || *pr.CrosstabFusable)) {
				t.Fatalf("predict errors %+v, result %+v; want admitted, buffered, unfused", pred.Errors, pr)
			}
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			var got float64
			if req.Crosstab != nil {
				got = resp.Crosstab.Matrix.GrandTotal.Scalar()
			} else {
				if len(resp.Data) != 4 {
					t.Fatalf("rows = %v, want 4 t buckets", resp.Data)
				}
				for _, row := range resp.Data {
					got += row["s"].(float64)
				}
			}
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("lag total %v, want %v", got, want)
			}
		})
	}
}
