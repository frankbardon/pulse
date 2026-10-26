package processing

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// windows_first_catalog_test.go: windows-first behaves the same however
// it is triggered (mmm-followups E4-S6). A request windows-first only
// through a bound model's terms — an aggregation over an
// ATTR_REG_PREDICT label whose model reads a window — reads the window
// labels as record columns everywhere the mode does: a regression, a
// holdout predicate and a matrix member over the window label run, each
// equal to the same request over a cohort storing the lagged column.
// And a crosstab fits its regressions on the buffered arm, and refuses a
// window it does not read.

// leadModelSet binds "m": a hand-made REG_OLS whose one term is the
// window label lead_x — the catalog that turns the mode on.
func leadModelSet(t *testing.T) *model.Set {
	t.Helper()
	b, err := model.Bind("m", &types.RegressionSpec{Type: types.REG_OLS, Target: "x", Predictors: []string{"lead_x"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{model.InterceptKey: 1, "lead_x": 0.5}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	set := &model.Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	return set
}

// TestWindowsFirst_CatalogTriggeredRecordReaders: a request
// windows-first only through m's terms (the aggregation over its score
// p, m reading lead_x) runs a regression, a holdout regression and a
// matrix over ANOTHER window label, lag_x, which only they read —
// exactly as the same request turned windows-first directly (an extra
// aggregation over lag_x) runs them, bit for bit.
func TestWindowsFirst_CatalogTriggeredRecordReaders(t *testing.T) {
	set := leadModelSet(t)
	build := func(direct bool) *types.Request {
		req := &types.Request{
			Windows: []*types.Window{wfLagXWindow(),
				{Type: types.WIN_LEAD, Field: "x", Label: "lead_x", PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "d"}}}},
			Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: []byte(`{"fit":"m"}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p", Label: "s"}},
			Regressions: []*types.RegressionSpec{
				{Type: types.REG_OLS, Target: "x", Predictors: []string{"lag_x"}},
				{Type: types.REG_OLS, Target: "x", Predictors: []string{"d"}, Holdout: "lag_x > 4"},
			},
			Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Fields: []string{"x", "lag_x"}}},
		}
		if direct {
			req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "lag_x", Label: "ls"})
		}
		return req
	}
	run := func(direct bool) *types.Response {
		p := NewProcessor(wfSchema(false))
		p.SetModels(set)
		recs := wfRecords(false)
		for i, r := range recs {
			r.SetMergePosition(0, 0, i) // the matrix slot's blocked merge reads it
		}
		resp, err := p.Process(context.Background(), build(direct), NewSliceIterator(recs))
		if err != nil {
			t.Fatalf("direct=%v: %v", direct, err)
		}
		return resp
	}
	got, want := run(false), run(true)
	if len(got.Regressions) != 2 || got.Regressions[0].Error != nil || got.Regressions[1].Error != nil || got.Regressions[0].NObs == 0 {
		t.Fatalf("regressions %+v, want both fitted", got.Regressions)
	}
	if !reflect.DeepEqual(got.Regressions, want.Regressions) {
		t.Fatalf("regressions\n got %+v\nwant %+v", got.Regressions, want.Regressions)
	}
	if len(got.Matrices) != 1 || !reflect.DeepEqual(got.Matrices, want.Matrices) {
		t.Fatalf("matrices\n got %+v\nwant %+v", got.Matrices, want.Matrices)
	}
	if got.Data[0]["s"] != want.Data[0]["s"] {
		t.Fatalf("score sum %v, want %v", got.Data[0]["s"], want.Data[0]["s"])
	}
}

// TestCrosstab_RegressionsFitOnBufferedArm: a crosstab's regressions fit
// over its filtered records — the record-row request's fit, bit for bit
// (they were silently dropped before) — beside the unchanged cells.
func TestCrosstab_RegressionsFitOnBufferedArm(t *testing.T) {
	regs := func() []*types.RegressionSpec {
		return []*types.RegressionSpec{{Type: types.REG_OLS, Target: "x", Predictors: []string{"d"}}}
	}
	ct := func() *types.CrosstabSpec {
		return &types.CrosstabSpec{Rows: []*types.Group{wfQuarterGroup()}, Columns: []*types.Group{wfRegionGroup()},
			Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "x", Label: "c"}, Shape: types.CrosstabShapeMatrix}
	}
	p := NewProcessor(wfSchema(false))
	got, err := p.RunCrosstab(context.Background(), &types.Request{Crosstab: ct(), Regressions: regs()}, wfRecords(false))
	if err != nil {
		t.Fatalf("crosstab: %v", err)
	}
	want, err := NewProcessor(wfSchema(false)).Process(context.Background(), &types.Request{Regressions: regs()}, NewSliceIterator(wfRecords(false)))
	if err != nil {
		t.Fatalf("record rows: %v", err)
	}
	if len(got.Regressions) != 1 || !reflect.DeepEqual(got.Regressions, want.Regressions) {
		t.Fatalf("crosstab regressions\n got %+v\nwant %+v", got.Regressions, want.Regressions)
	}
	plain, err := NewProcessor(wfSchema(false)).RunCrosstab(context.Background(), &types.Request{Crosstab: ct()}, wfRecords(false))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Crosstab, plain.Crosstab) {
		t.Fatalf("the regression moved the cells")
	}
}

// TestCrosstab_UnreadWindowRefused: a crosstab whose axes, cell and
// margins read no window output has no row for its window to run over:
// PULSE_WINDOW_INVALID crosstab_window_unread, never a silent drop (a
// grouped or aggregated request runs the window over its output rows,
// refused when they lack its names).
func TestCrosstab_UnreadWindowRefused(t *testing.T) {
	req := &types.Request{Windows: []*types.Window{wfLagXWindow()}, Crosstab: &types.CrosstabSpec{
		Rows: []*types.Group{wfQuarterGroup()}, Columns: []*types.Group{wfRegionGroup()},
		Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "x", Label: "c"}}}
	_, err := NewProcessor(wfSchema(false)).RunCrosstab(context.Background(), req, wfRecords(false))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WINDOW_INVALID || ce.Details["reason"] != "crosstab_window_unread" || ce.Details["window_index"] != 0 {
		t.Fatalf("err = %v, want PULSE_WINDOW_INVALID crosstab_window_unread", err)
	}
}
