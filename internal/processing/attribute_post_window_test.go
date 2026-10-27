package processing

import (
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// attribute_post_window_test.go: the engine's post-window attribute
// phase (transformed-predictors E2-S1). A row-local attribute reading a
// window label runs after the windows over the records the label was
// copied onto (derivedFloat, the materialize route's conversion), so its
// values — and a fit on them — equal those over a cohort carrying the
// window column.

// TestWindowPredictor_PostWindowFormulaEqualsMaterializedColumn: hill()
// over a WIN_LAG label, and REG_OLS on it, equal hill() and REG_OLS over
// a cohort carrying the lagged column — values and coefficients bit for
// bit — and the output rows carry the post-window label.
func TestWindowPredictor_PostWindowFormulaEqualsMaterializedColumn(t *testing.T) {
	t.Parallel()

	rows := windowRegRows(40)
	s := windowRegSchema()
	recs := make([]*Record, len(rows))
	for i, r := range rows {
		recs[i] = NewRecord(s, map[string]float64{"t": r[0], "x": r[1], "y": r[2]})
	}
	sat := &types.Attribute{Type: types.ATTR_FORMULA, Label: "sat", Expression: "hill(lag_x, 3, 2)"}
	reg := []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"sat"}}}
	req := &types.Request{
		Windows:     []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}},
		Attributes:  []*types.Attribute{sat},
		Regressions: reg,
	}
	got, err := NewProcessor(s).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	ms := windowRegSchema("lag_x")
	mrecs := make([]*Record, len(rows))
	for i, r := range rows {
		if i == 0 {
			mrecs[i] = NewRecordWithNulls(ms, map[string]float64{"t": r[0], "x": r[1], "y": r[2]}, map[string]bool{"lag_x": true})
			continue
		}
		mrecs[i] = NewRecord(ms, map[string]float64{"t": r[0], "x": r[1], "y": r[2], "lag_x": rows[i-1][1]})
	}
	ref := &types.Request{Attributes: []*types.Attribute{sat}, Regressions: reg}
	want, err := NewProcessor(ms).Process(context.Background(), ref, NewSliceIterator(mrecs))
	if err != nil {
		t.Fatalf("reference Process: %v", err)
	}
	g, w := got.Regressions[0], want.Regressions[0]
	if g.Error != nil || w.Error != nil || g.NObs < 30 || g.NObs != w.NObs {
		t.Fatalf("fits: n %d / %d, errors %v / %v", g.NObs, w.NObs, g.Error, w.Error)
	}
	for _, k := range []string{regression.InterceptKey, "sat"} {
		if math.Float64bits(g.Coefficients[k]) != math.Float64bits(w.Coefficients[k]) {
			t.Errorf("coefficient %s = %v, reference %v", k, g.Coefficients[k], w.Coefficients[k])
		}
	}
	if len(got.Data) != len(rows) {
		t.Fatalf("%d rows, want %d", len(got.Data), len(rows))
	}
	if _, ok := got.Data[0]["sat"]; ok {
		t.Errorf("row 0 carries sat %v over a null lag", got.Data[0]["sat"])
	}
	for i := 1; i < len(rows); i++ {
		// A materialized null leaves the cell absent; a NaN rides as
		// NaN on both routes.
		var want any
		if v, ok := mrecs[i].NumericValue("sat"); ok {
			want = v
		}
		cell := got.Data[i]["sat"]
		if wf, isF := want.(float64); isF && math.IsNaN(wf) {
			if cf, ok := cell.(float64); !ok || !math.IsNaN(cf) {
				t.Fatalf("row %d: sat %v, materialized route NaN", i, cell)
			}
			continue
		}
		if cell != want {
			t.Fatalf("row %d: sat %v, materialized route %v", i, cell, want)
		}
	}
}

// TestWindowPredictor_ProcessorRefusesWindowOrder: a processor driven
// without the service's field-reference pass still refuses a
// window-dependent slot that cannot run after the windows.
func TestWindowPredictor_ProcessorRefusesWindowOrder(t *testing.T) {
	t.Parallel()

	s := windowRegSchema()
	var recs []*Record
	for _, r := range windowRegRows(8) {
		recs = append(recs, NewRecord(s, map[string]float64{"t": r[0], "x": r[1], "y": r[2]}))
	}
	req := &types.Request{
		Windows:    []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}},
		Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "lag_x", Label: "z"}},
	}
	_, err := NewProcessor(s).Process(context.Background(), req, NewSliceIterator(recs))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_ATTRIBUTE_WINDOW_ORDER ||
		!reflect.DeepEqual(ce.Details, map[string]any{"attribute": "ATTR_ZSCORE", "field": "lag_x"}) {
		t.Fatalf("err = %v, want PULSE_ATTRIBUTE_WINDOW_ORDER", err)
	}
}

// TestWindowPredictor_PostWindowNonNumericWindowRefused: a non-numeric
// window value a post-window attribute reads is PROCESSING_CONFIG naming
// the label, as the regression and matrix copies refuse it.
func TestWindowPredictor_PostWindowNonNumericWindowRefused(t *testing.T) {
	t.Parallel()

	s := windowRegSchema()
	var recs []*Record
	for _, r := range windowRegRows(6) {
		recs = append(recs, NewRecord(s, map[string]float64{"t": r[0], "x": r[1], "y": r[2]}))
	}
	exts := &ExtensionRegistry{Windows: map[types.WindowType]window.WindowFactory{
		"WIN_ACME_TEXT": func(*types.Window, window.WindowOptions) (window.WindowComputer, error) { return stringComputer{}, nil },
	}}
	req := &types.Request{
		Windows:    []*types.Window{{Type: "WIN_ACME_TEXT", Field: "x", Label: "w", OrderBy: []types.OrderKey{{Field: "t"}}}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "w + 1"}},
	}
	if got := NewProcessor(s).windowRecordColumns(req, vectors.PostWindowAttributes(req, s, nil, nil)); !reflect.DeepEqual(got, []windowRecordColumn{{label: "w", consumer: "attribute"}}) {
		t.Fatalf("window record columns = %v, want w for the attribute", got)
	}
	_, err := NewProcessorWithExtensions(s, exts).Process(context.Background(), req, NewSliceIterator(recs))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["field"] != "w" || !strings.Contains(ce.Message, "attribute") {
		t.Fatalf("err = %v, want PROCESSING_CONFIG naming the attribute input w", err)
	}
}
