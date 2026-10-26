package processing

import (
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// windows_first_test.go: the engine's ungrouped windows-first arm
// (mmm-followups E4-S2). An aggregation reading a window label — or the
// label of a post-window attribute reading one — runs over the records
// AFTER the windows ran over the record rows and their values were
// copied on: it aggregates exactly the per-record values the same
// request returns without its aggregations, and a window column behaves
// as a cohort column carrying the same values, weights included.

// TestWindowsFirst_AggregationEqualsSumOfRecordRows: AGG_SUM over an
// ATTR_FORMULA `coef(...) * <window label>` equals the sum of the
// per-record contributions the request returns without aggregations;
// the aggregation Components follow the declared slot order.
func TestWindowsFirst_AggregationEqualsSumOfRecordRows(t *testing.T) {
	set, _ := coefFit(t)
	schema := predictSchema()
	wins := []*types.Window{{Type: types.WIN_LAG, Field: "x1", Label: "lag_x1", OrderBy: []types.OrderKey{{Field: "y"}}}}
	// The first record's lag is null; ATTR_FORMULA raises on a null
	// input, so the formula coalesces it.
	attrs := []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "contrib", Expression: `coef("m", "x1") * (lag_x1 ?? 0)`}}

	rows, err := processCoef(t, set, nil, &types.Request{Windows: wins, Attributes: attrs}, predictRecords(schema))
	if err != nil {
		t.Fatalf("record rows: %v", err)
	}
	var want, wantLag float64
	lagged := 0
	for _, r := range rows.Data {
		want += r["contrib"].(float64)
		if v, ok := r["lag_x1"].(float64); ok {
			wantLag += v
			lagged++
		}
	}
	if lagged != len(rows.Data)-1 || want == 0 {
		t.Fatalf("%d of %d rows carry a lag (sum %v); the fixture's first lag should be the only null", lagged, len(rows.Data), want)
	}

	resp, err := processCoef(t, set, nil, &types.Request{Windows: wins, Attributes: attrs, Aggregations: []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "contrib", Label: "s"},
		{Type: types.AGG_COUNT, Field: "x1", Label: "n"},
		{Type: types.AGG_SUM, Field: "lag_x1", Label: "lag_sum"},
	}}, predictRecords(schema))
	if err != nil {
		t.Fatalf("windows-first: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("rows = %v, want the one aggregation row", resp.Data)
	}
	row := resp.Data[0]
	if got := row["s"].(float64); math.Abs(got-want) > 1e-12*math.Abs(want) {
		t.Fatalf("AGG_SUM(contrib) = %v, want the record rows' sum %v", got, want)
	}
	if got := row["lag_sum"].(float64); math.Abs(got-wantLag) > 1e-12*math.Abs(wantLag) {
		t.Fatalf("AGG_SUM(lag_x1) = %v, want %v", got, wantLag)
	}
	if _, leaked := row["lag_x1"]; leaked {
		t.Fatalf("the aggregation row carries a window column: %v", row)
	}
	var labels []string
	for _, c := range resp.Components.Aggregations {
		labels = append(labels, c.Label)
	}
	if !reflect.DeepEqual(labels, []string{"s", "n", "lag_sum"}) {
		t.Fatalf("Components.Aggregations labels %v, want declared order", labels)
	}
	if c := resp.Components.Aggregations[2]; c.N != lagged || c.NNull != 1 {
		t.Fatalf("lag_x1 floor {n %d, n_null %d}, want {%d, 1}", c.N, c.NNull, lagged)
	}
}

// TestWindowsFirst_WindowColumnAggregatesAsCohortColumn: weighted and
// unweighted aggregations over a WIN_LAG label equal the same
// aggregations over a cohort carrying the lagged column (null first
// row) — data and every Components entry, sum_weights and n_eff
// included.
func TestWindowsFirst_WindowColumnAggregatesAsCohortColumn(t *testing.T) {
	src := windowRegRows(30)
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "lag_x", Label: "wmean", Weight: types.SlotWeightField("w")},
			{Type: types.AGG_SUM, Field: "lag_x", Label: "fsum", Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})},
			{Type: types.AGG_STDDEV, Field: "lag_x", Label: "sd"},
			{Type: types.AGG_RATIO, Field: "lag_x", Label: "r", Params: []byte(`{"numerator_field":"lag_x","denominator_field":"y"}`)},
		}
	}
	ws := windowRegSchema("w")
	var wrecs []*Record
	for i, r := range src {
		wrecs = append(wrecs, NewRecord(ws, map[string]float64{"t": r[0], "x": r[1], "y": r[2], "w": float64(1 + i%3)}))
	}
	got, err := NewProcessor(ws).Process(context.Background(), &types.Request{
		Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}},
		Aggregations: aggs(),
	}, NewSliceIterator(wrecs))
	if err != nil {
		t.Fatalf("windows-first: %v", err)
	}

	cs := windowRegSchema("w", "lag_x")
	var crecs []*Record
	for i, r := range src {
		vals := map[string]float64{"t": r[0], "x": r[1], "y": r[2], "w": float64(1 + i%3)}
		if i == 0 {
			crecs = append(crecs, NewRecordWithNulls(cs, vals, map[string]bool{"lag_x": true}))
			continue
		}
		vals["lag_x"] = src[i-1][1]
		crecs = append(crecs, NewRecord(cs, vals))
	}
	// The windows-first arm is buffered; the cohort-column request would
	// stream (Welford, other bits), so it runs the buffered arm too.
	cp := NewProcessor(cs)
	want, err := cp.processRecords(context.Background(), cp.prepare(&types.Request{Aggregations: aggs()}), crecs)
	if err != nil {
		t.Fatalf("cohort column: %v", err)
	}
	if !reflect.DeepEqual(got.Data, want.Data) {
		t.Fatalf("data %v, want %v", got.Data, want.Data)
	}
	if !reflect.DeepEqual(got.Components, want.Components) {
		t.Fatalf("components %+v, want %+v", got.Components, want.Components)
	}
	if got.Components.Aggregations[0].NEff == nil || got.Components.Aggregations[1].SumWeights == nil {
		t.Fatal("the weighted slots report no weighted floor")
	}
}

// TestWindowsFirst_GroupedAndCrosstabPending: the grouped and crosstab
// windows-first shapes are not executed yet — PROCESSING_CONFIG
// windows_first_pending rather than grouping a column no record carries.
func TestWindowsFirst_GroupedAndCrosstabPending(t *testing.T) {
	s := windowRegSchema()
	var recs []*Record
	for _, r := range windowRegRows(8) {
		recs = append(recs, NewRecord(s, map[string]float64{"t": r[0], "x": r[1], "y": r[2]}))
	}
	win := []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "lag_x", Label: "s"}}
	for name, req := range map[string]*types.Request{
		"grouped": {Windows: win, Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "t", Interval: 4}}, Aggregations: sum},
		"crosstab": {Windows: win, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "lag_x", Interval: 1}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "t", Interval: 4}},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewProcessor(s).Process(context.Background(), req, NewSliceIterator(recs))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["reason"] != "windows_first_pending" {
				t.Fatalf("err = %v, want PROCESSING_CONFIG windows_first_pending", err)
			}
		})
	}
}

// TestWindowsFirst_NonNumericWindowRefused: a non-numeric window value
// an aggregation reads is PROCESSING_CONFIG naming the label, as the
// regression, matrix and post-window attribute copies refuse it.
func TestWindowsFirst_NonNumericWindowRefused(t *testing.T) {
	s := windowRegSchema()
	var recs []*Record
	for _, r := range windowRegRows(6) {
		recs = append(recs, NewRecord(s, map[string]float64{"t": r[0], "x": r[1], "y": r[2]}))
	}
	exts := &ExtensionRegistry{Windows: map[types.WindowType]window.WindowFactory{
		"WIN_ACME_TEXT": func(*types.Window, window.WindowOptions) (window.WindowComputer, error) { return stringComputer{}, nil },
	}}
	req := &types.Request{
		Windows:      []*types.Window{{Type: "WIN_ACME_TEXT", Field: "x", Label: "w", OrderBy: []types.OrderKey{{Field: "t"}}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "w", Label: "s"}},
	}
	if got := NewProcessor(s).windowRecordColumns(req, vectors.PostWindowAttributes(req, s, nil, nil)); !reflect.DeepEqual(got, []windowRecordColumn{{label: "w", consumer: "aggregation"}}) {
		t.Fatalf("window record columns = %v, want w for the aggregation", got)
	}
	_, err := NewProcessorWithExtensions(s, exts).Process(context.Background(), req, NewSliceIterator(recs))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["field"] != "w" || !strings.Contains(ce.Message, "aggregation") {
		t.Fatalf("err = %v, want PROCESSING_CONFIG naming the aggregation input w", err)
	}
}
