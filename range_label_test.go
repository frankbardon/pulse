package pulse

import (
	"context"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// range_label_test.go pins range_label(table, x[, unmatched]) at the
// facade: it reaches ATTR_FORMULA (as lookup()'s banded keys, inside
// total() and inside Materialize), FILTER_EXPRESSION (an unmatched row's
// null label compares false, no error) and a Compose optimize objective;
// predict accepts every one of those requests; the run refuses an
// unknown table and a value of the wrong kind with their range-table
// codes; and an embedder may not register a function under its name.

const rangeLabelCohort = "rl.pulse"

func rlF64(v float64) *float64 { return &v }

// rangeLabelOptions registers two numeric bandings — score_bands (low
// [-inf, 10), mid [10, 20), a gap, high [30, +inf)) and ltv_bands (lo
// [-inf, 0.8), hi [0.8, +inf)) — and the rate grid keyed by both labels
// (an "other" score row for the gap).
func rangeLabelOptions(fsys afero.Fs) Options {
	grid := map[string]float64{}
	for i, s := range []string{"low", "mid", "high", "other"} {
		grid[s+"|lo"] = float64(2*i + 1)
		grid[s+"|hi"] = float64(2*i + 2)
	}
	return Options{FS: fsys, Extensions: Extensions{
		RangeTables: map[string]RangeTable{
			"score_bands": {NumericRanges: []NumericRangeSpec{
				{Label: "low", End: rlF64(10)},
				{Label: "mid", Start: rlF64(10), End: rlF64(20)},
				{Label: "high", Start: rlF64(30)},
			}},
			"ltv_bands": {NumericRanges: []NumericRangeSpec{
				{Label: "lo", End: rlF64(0.8)},
				{Label: "hi", Start: rlF64(0.8)},
			}},
		},
		LookupTables: map[string]LookupTable{"rate_grid": {Rows: grid}},
	}}
}

// rangeLabelPulse is an instance over a four-row cohort: score 5 / 15 /
// 25 (the gap) / 35, ltv 0.5 / 0.9 / 0.7 / 0.95.
func rangeLabelPulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, rangeLabelCohort, []string{"score", "ltv", "region"}, [][]string{
		{"5", "0.5", "north"}, {"15", "0.9", "south"}, {"25", "0.7", "north"}, {"35", "0.95", "south"},
	})
	p, err := New(rangeLabelOptions(fsys))
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// rateExpr is the banded rate: low|lo 1, mid|hi 4, other|lo 7, high|hi 6.
const rateExpr = `lookup("rate_grid", range_label("score_bands", score, "other"), range_label("ltv_bands", ltv))`

// TestRangeLabel_ReachesFormulaFilterTotal: one request reads
// range_label in a formula (lookup keys), a total() argument and a
// filter; predict judges it valid and the run's figures are the banded
// ones.
func TestRangeLabel_ReachesFormulaFilterTotal(t *testing.T) {
	t.Parallel()

	p, _ := rangeLabelPulse(t)
	ctx := context.Background()
	req := &Request{
		Cohort: &types.Cohort{Filename: rangeLabelCohort},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "rate", Expression: rateExpr},
			{Type: types.ATTR_FORMULA, Label: "mid_total", Expression: `total(range_label("score_bands", score) == "mid" ? score : 0)`},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "rate", Label: "rate_sum"},
			{Type: types.AGG_MAX, Field: "mid_total", Label: "mid_total"},
		},
	}
	if pred, err := p.Predict(ctx, req); err != nil || !pred.Valid {
		t.Fatalf("predict: %v %+v", err, pred)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["rate_sum"]; got != 1.0+4+7+6 {
		t.Errorf("rate_sum = %v, want 18", got)
	}
	if got := resp.Data[0]["mid_total"]; got != 15.0 {
		t.Errorf("mid_total = %v, want 15", got)
	}

	filtered := &Request{
		Cohort:       &types.Cohort{Filename: rangeLabelCohort},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `range_label("score_bands", score) in ["low", "high"]`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
	}
	if pred, err := p.Predict(ctx, filtered); err != nil || !pred.Valid {
		t.Fatalf("predict filter: %v %+v", err, pred)
	}
	resp, err = p.Process(ctx, filtered)
	if err != nil {
		t.Fatalf("filter (the unmatched row must drop, not fail): %v", err)
	}
	if got := resp.Data[0]["s"]; got != 40.0 {
		t.Errorf("filtered sum = %v, want 40 (5 + 35)", got)
	}
}

// TestRangeLabel_RunRefusals: an unknown table is
// PULSE_RANGE_TABLE_UNKNOWN, a categorical label against a numeric
// table PULSE_RANGE_TABLE_KIND_MISMATCH.
func TestRangeLabel_RunRefusals(t *testing.T) {
	t.Parallel()

	p, _ := rangeLabelPulse(t)
	for expr, code := range map[string]perr.Code{
		`range_label("nope", score) == "low"`:         perr.PULSE_RANGE_TABLE_UNKNOWN,
		`range_label("score_bands", region) == "low"`: perr.PULSE_RANGE_TABLE_KIND_MISMATCH,
	} {
		_, err := p.Process(context.Background(), &Request{
			Cohort:       &types.Cohort{Filename: rangeLabelCohort},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		})
		if !perr.HasCode(err, code) {
			t.Errorf("%s: %v, want %s", expr, err, code)
		}
	}
}

// TestRangeLabel_Materialize: a Materialize attribute writes the banded
// rate per row.
func TestRangeLabel_Materialize(t *testing.T) {
	t.Parallel()

	p, _ := rangeLabelPulse(t)
	req := &Request{
		Cohort:     &types.Cohort{Filename: rangeLabelCohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "rate", Expression: rateExpr}},
	}
	if _, pres := predictMat(t, p, req); !pres.Valid {
		t.Fatalf("predict materialize invalid: %+v", pres)
	}
	if _, err := p.Materialize(context.Background(), req, "out.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	schema, rows := readAll(t, p, "out.pulse")
	col := slices.IndexFunc(schema.Fields, func(f encoding.Field) bool { return f.Name == "rate" })
	if col < 0 {
		t.Fatalf("no rate column in %v", schema.Fields)
	}
	if got := column(rows, col); !slices.Equal(got, []any{1.0, 4.0, 7.0, 6.0}) {
		t.Errorf("rate = %v, want [1 4 7 6]", got)
	}
}

// TestRangeLabel_ComposeOptimize: an optimize objective reads
// range_label; predict and the run both accept it, and the run solves
// over it.
func TestRangeLabel_ComposeOptimize(t *testing.T) {
	t.Parallel()

	p, _ := rangeLabelPulse(t)
	ctx := context.Background()
	c := &ComposedRequest{
		Requests: []*Request{{
			Cohort:       &types.Cohort{Filename: rangeLabelCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}},
		Optimize: []types.OptimizeSpec{{
			Name:      "band",
			Variables: []types.OptimizeVariable{{Name: "v", Lower: types.BoundValue(0), Upper: types.BoundValue(9)}},
			Objective: `v * (range_label("score_bands", v) == "low" ? 1 : 0)`,
			Direction: types.OptimizeMaximize,
		}},
	}
	if env, err := p.PredictCompose(ctx, c); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
	out, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Optimizations) != 1 || out.Optimizations[0].Error != nil {
		t.Fatalf("optimizations = %+v", out.Optimizations)
	}
}

// TestRangeLabel_NameReserved: an embedder may not register an
// expression function named range_label.
func TestRangeLabel_NameReserved(t *testing.T) {
	t.Parallel()

	_, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{
		ExprFunctions: []ExprFunction{{Name: "range_label", Fn: func(a string, x float64) string { return "" }}},
	}})
	if !perr.HasCode(err, perr.PULSE_EXTENSION_NAME_RESERVED) {
		t.Fatalf("embedder range_label(): %v, want PULSE_EXTENSION_NAME_RESERVED", err)
	}
}
