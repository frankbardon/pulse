package processing

import (
	"context"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// regression_window_test.go: a regression reads a same-request window
// label (transformed-predictors E1-S1). Over the record rows the engine
// copies each label a regression reads onto its record after the
// windows ran (copyWindowMembers), converted as the materialize route
// stores it, so the fit equals the fit over a cohort carrying the column.

func windowRegSchema(extra ...string) *encoding.Schema {
	fields := []encoding.Field{
		{Name: "t", Type: encoding.FieldTypeF64},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}
	for _, n := range extra {
		fields = append(fields, encoding.Field{Name: n, Type: encoding.FieldTypeF64, Nullable: true})
	}
	return &encoding.Schema{Fields: fields}
}

// windowRegRows returns t, x, y for n rows (t ascending), y loosely
// linear in the previous row's x.
func windowRegRows(n int) [][3]float64 {
	out := make([][3]float64, n)
	for i := range out {
		x := float64(i%7) + 0.5*math.Sin(float64(i))
		out[i] = [3]float64{float64(i), x, 0}
	}
	for i := range out {
		prev := 0.0
		if i > 0 {
			prev = out[i-1][1]
		}
		out[i][2] = float64(2*prev) + 1 + 0.1*math.Cos(float64(3*i))
	}
	return out
}

// TestWindowPredictor_RegressionReadsWindowLabel: REG_OLS on a WIN_LAG
// label equals the same fit over a cohort carrying the lagged column
// (the first row's null lag drops it listwise), bit for bit.
func TestWindowPredictor_RegressionReadsWindowLabel(t *testing.T) {
	rows := windowRegRows(40)
	s := windowRegSchema()
	recs := make([]*Record, len(rows))
	for i, r := range rows {
		recs[i] = NewRecord(s, map[string]float64{"t": r[0], "x": r[1], "y": r[2]})
	}
	req := &types.Request{
		Windows:     []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}},
		Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"lag_x"}}},
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
	ref := &types.Request{Regressions: req.Regressions}
	want, err := NewProcessor(ms).Process(context.Background(), ref, NewSliceIterator(mrecs))
	if err != nil {
		t.Fatalf("reference Process: %v", err)
	}
	g, w := got.Regressions[0], want.Regressions[0]
	if g.Error != nil || w.Error != nil {
		t.Fatalf("fit errors: %v / %v", g.Error, w.Error)
	}
	if g.NObs != len(rows)-1 || g.NObs != w.NObs {
		t.Fatalf("n = %d, reference %d, want %d", g.NObs, w.NObs, len(rows)-1)
	}
	for _, k := range []string{regression.InterceptKey, "lag_x"} {
		if math.Float64bits(g.Coefficients[k]) != math.Float64bits(w.Coefficients[k]) {
			t.Errorf("coefficient %s = %v, reference %v", k, g.Coefficients[k], w.Coefficients[k])
		}
	}
	if math.Abs(g.Coefficients["lag_x"]-2) > 0.05 {
		t.Errorf("slope on lag_x = %v, want ≈ 2", g.Coefficients["lag_x"])
	}
	// The response rows are the windows-only shape's, unchanged.
	if len(got.Data) != len(rows) || got.Data[0]["lag_x"] != nil || got.Data[1]["lag_x"] != rows[0][1] {
		t.Errorf("data rows changed: %d rows, first lag %v, second %v", len(got.Data), got.Data[0]["lag_x"], got.Data[1]["lag_x"])
	}
}

// stringComputer writes a string into its label: an extension window
// whose output no regression can read.
type stringComputer struct{}

func (stringComputer) Compute(rows []map[string]any, _ [][]int, label string) error {
	for _, r := range rows {
		r[label] = "high"
	}
	return nil
}

// TestWindowPredictor_NonNumericWindowRefused: a non-numeric window
// value a regression reads — an extension window's, or a lag's string
// default — is PROCESSING_CONFIG naming the label, as the matrix-member
// copy refuses it.
func TestWindowPredictor_NonNumericWindowRefused(t *testing.T) {
	s := windowRegSchema()
	recs := func() RecordIterator {
		var out []*Record
		for _, r := range windowRegRows(10) {
			out = append(out, NewRecord(s, map[string]float64{"t": r[0], "x": r[1], "y": r[2]}))
		}
		return NewSliceIterator(out)
	}
	exts := &ExtensionRegistry{Windows: map[types.WindowType]window.WindowFactory{
		"WIN_ACME_TEXT": func(*types.Window, window.WindowOptions) (window.WindowComputer, error) { return stringComputer{}, nil },
	}}
	cases := map[string]*types.Window{
		"extension": {Type: "WIN_ACME_TEXT", Field: "x", Label: "w", OrderBy: []types.OrderKey{{Field: "t"}}},
		"lag default": {Type: types.WIN_LAG, Field: "x", Label: "w", OrderBy: []types.OrderKey{{Field: "t"}},
			Params: []byte(`{"default":"none"}`)},
	}
	for name, win := range cases {
		t.Run(name, func(t *testing.T) {
			req := &types.Request{
				Windows:     []*types.Window{win},
				Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"w"}}},
			}
			_, err := NewProcessorWithExtensions(s, exts).Process(context.Background(), req, recs())
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["field"] != "w" ||
				!strings.Contains(ce.Message, "regression") {
				t.Fatalf("err = %v, want PROCESSING_CONFIG naming the regression input w", err)
			}
		})
	}
}

// TestWindowPredictor_NoRegressionReadCopiesNothing: a window no
// regression (or matrix) reads never lands on the records, so a request
// naming no window label runs exactly as before — the record keeps no
// such column.
func TestWindowPredictor_NoRegressionReadCopiesNothing(t *testing.T) {
	s := windowRegSchema()
	p := NewProcessor(s)
	req := &types.Request{
		Windows:     []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}},
		Regressions: []*types.RegressionSpec{{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}},
	}
	if got := p.windowRecordColumns(req, vectors.PostWindow{}); len(got) != 0 {
		t.Fatalf("window record columns = %v, want none", got)
	}
	req.Regressions[0].Predictors = []string{"x", "lag_x"}
	req.Regressions[0].Covariance = &types.RegressionCovariance{Type: "cluster", Cluster: "lag_x"}
	got := p.windowRecordColumns(req, vectors.PostWindow{})
	if len(got) != 1 || got[0].label != "lag_x" || got[0].consumer != "regression" {
		t.Fatalf("window record columns = %+v, want one regression lag_x", got)
	}
}
