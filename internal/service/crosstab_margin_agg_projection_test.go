package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// This file is the projection half of the auxiliary-margin contract, and
// it exists because every other test of that contract hands the operator
// a fully-populated Record built by hand. No projection ever ran in one,
// so the fields the operator actually reads were never in question — and
// the whole feature returned a confident, silent 0 against a real cohort
// while nine stories of unit tests stayed green.
//
// Every test here therefore goes through the REAL path: a written .pulse
// file, opened, decoded under the projection processing.NeededFields
// derives from the request. The fixture carries a deliberately
// UNREFERENCED trailing field so the needed set is provably narrower
// than the schema — installProjection declines to install anything when
// it is not, which would make the whole file vacuous.

// distinctSumProjectionSchema mirrors distinctMarginSchema and adds the
// two fields this contract turns on: a numeric `weight` to sum, and a
// `pad` field nothing in any request below names. Without `pad` the
// needed set would equal the schema and no projection would be installed
// at all — the bug would be invisible and these tests would prove
// nothing.
func distinctSumProjectionSchema() *encoding.Schema {
	brandDict := encoding.NewDictionary()
	brandDict.Add("alpha")
	brandDict.Add("beta")
	brandDict.Add("gamma")

	audienceDict := encoding.NewDictionary()
	audienceDict.Add("core")
	audienceDict.Add("affluent")

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: brandDict},
			{Name: "audience", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: audienceDict},
			{Name: "respondent", Type: encoding.FieldTypeU32, ByteOffset: 2, CsvColumnIdx: 2},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 6, CsvColumnIdx: 3},
			{Name: "pad", Type: encoding.FieldTypeF64, ByteOffset: 14, CsvColumnIdx: 4},
		},
	}
}

// distinctSumProjectionRecords is distinctMarginRecords with one weight
// per respondent — weight(r) == r — so every expected figure below is a
// sum of small integers a reader can check by eye. Respondents recur
// across brands, which is the entire point: a margin summed from cells
// would double-count them.
//
//	                 core                    affluent
//	alpha       {1,2,3,4} -> 10             {1,2} -> 3
//	beta          {3,4,5} -> 12               {2} -> 2
//	gamma         {5,6,7} -> 18                    -> (empty)
//
//	column margin core     = |{1..7}|   -> 28  (cells would sum to 40)
//	column margin affluent = |{1,2}|    ->  3  (cells would sum to  5)
//	row margin alpha       = |{1,2,3,4}|-> 10
//	row margin beta        = |{2,3,4,5}|-> 14
//	row margin gamma       = |{5,6,7}|  -> 18
//	grand margin           = |{1..7}|   -> 28  (cells would sum to 45)
func distinctSumProjectionRecords() [][]uint64 {
	rows := [][3]uint64{
		{0, 0, 1}, {0, 0, 2}, {0, 0, 3}, {0, 0, 4},
		{1, 0, 3}, {1, 0, 4}, {1, 0, 5},
		{2, 0, 5}, {2, 0, 6}, {2, 0, 7},
		{0, 1, 1}, {0, 1, 2},
		{1, 1, 2},
	}
	out := make([][]uint64, 0, len(rows))
	for i, r := range rows {
		out = append(out, []uint64{
			r[0], r[1], r[2],
			math.Float64bits(float64(r[2])),
			// Noise in the unprojected slot. If projection ever
			// mis-aligns the record cursor these values are what
			// leak into `weight`, so they are deliberately nothing
			// like a plausible weight.
			math.Float64bits(float64(9000 + i)),
		})
	}
	return out
}

// TestCrosstabMarginAgg_ProjectionCoversAuxiliaryFields is the load-
// bearing regression for the MarginAggregations half of E2-S7.
//
// The cell names `respondent`, so the distinct KEY is projected whether
// or not params.distinct_by is read. `weight` — the value half — is
// reachable ONLY by walking req.Crosstab.MarginAggregations. Reverting
// that walk leaves `weight` undecoded, NumericValue answers ok=false on
// every record, and every figure below collapses to an absent slot with
// n_null equal to the full admitted count. Reverting the distinct_by
// lookup instead leaves this test GREEN, which is what makes the two
// halves independently mutation-proved.
//
// Driven down BOTH dispatch arms: the fused arm accumulates auxiliaries
// during the walk and the buffered arm recomputes them from raw rows,
// but they share one projection, so a hole shows on both.
func TestCrosstabMarginAgg_ProjectionCoversAuxiliaryFields(t *testing.T) {
	ctx := context.Background()
	schema := distinctSumProjectionSchema()
	cfg := setupTestFS(t, "distinct_sum_proj.pulse", schema, distinctSumProjectionRecords())

	wantCols := map[string]struct {
		sum      float64
		distinct int
	}{
		"core":     {28, 7},
		"affluent": {3, 2},
	}
	wantRows := map[string]struct {
		sum      float64
		distinct int
	}{
		"alpha": {10, 4},
		"beta":  {14, 4},
		"gamma": {18, 3},
	}

	for _, arm := range []struct {
		name       string
		disableFus bool
	}{{"fused", false}, {"buffered", true}} {
		t.Run(arm.name, func(t *testing.T) {
			req := &types.Request{
				Cohort: &types.Cohort{Filename: "distinct_sum_proj.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "audience"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "respondent", Label: "n"},
					MarginAggregations: []*types.Aggregation{{
						Type:   types.AGG_DISTINCT_SUM,
						Field:  "weight",
						Label:  "weighted_base",
						Params: json.RawMessage(`{"distinct_by":"respondent"}`),
					}},
					Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					Shape:   types.CrosstabShapeMatrix,
				},
			}

			svc := New(cfg)
			svc.SetDisableCrosstabFusion(arm.disableFus)
			resp, err := svc.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			ct := resp.Components.Crosstab
			if ct == nil {
				t.Fatalf("no crosstab components on the response")
			}
			if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
				t.Fatalf("no crosstab matrix payload on the response")
			}
			matrix := resp.Crosstab.Matrix

			for c, key := range matrix.ColumnKeys {
				name := axisKeyName(t, key)
				want, ok := wantCols[name]
				if !ok {
					t.Fatalf("unexpected column key %q", name)
				}
				assertAuxFigure(t, "column "+name, ct.ColumnMarginAggregations[c]["weighted_base"], want.sum, want.distinct)
			}
			for r, key := range matrix.RowKeys {
				name := axisKeyName(t, key)
				want, ok := wantRows[name]
				if !ok {
					t.Fatalf("unexpected row key %q", name)
				}
				assertAuxFigure(t, "row "+name, ct.RowMarginAggregations[r]["weighted_base"], want.sum, want.distinct)
			}
			assertAuxFigure(t, "grand", ct.GrandTotalAggregations["weighted_base"], 28, 7)
		})
	}
}

// axisKeyName renders a single-dim axis key as its string label.
func axisKeyName(t *testing.T, key types.AxisKey) string {
	t.Helper()
	if len(key) != 1 {
		t.Fatalf("expected a single-dim axis key, got %#v", key)
	}
	s, ok := key[0].(string)
	if !ok {
		t.Fatalf("axis key member = %#v (%T), want a string", key[0], key[0])
	}
	return s
}

// assertAuxFigure checks one auxiliary margin figure end to end. It
// asserts Present explicitly rather than only comparing the value: an
// unprojected value field produces an ABSENT slot, and a test that only
// compared numbers against a zero-valued `any` could pass on the exact
// failure this file exists to catch.
func assertAuxFigure(t *testing.T, where string, fig types.MarginAggregationFigure, wantSum float64, wantDistinct int) {
	t.Helper()
	if !fig.Present {
		t.Errorf("%s: figure absent; the auxiliary admitted no record — its value field was almost certainly never projected", where)
		return
	}
	got, ok := fig.Value.(float64)
	if !ok {
		t.Errorf("%s: value = %#v (%T), want a float64", where, fig.Value, fig.Value)
		return
	}
	if math.Abs(got-wantSum) > 1e-9 {
		t.Errorf("%s: sum = %v, want %v", where, got, wantSum)
	}
	dc, ok := fig.Components["distinct_count"]
	if !ok {
		t.Errorf("%s: components carry no distinct_count: %#v", where, fig.Components)
		return
	}
	if n := toInt(dc); n != wantDistinct {
		t.Errorf("%s: distinct_count = %v, want %d", where, dc, wantDistinct)
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return -1
}

// TestProcess_ProjectionCoversDistinctByParam is the OTHER half, isolated.
//
// A top-level AGG_DISTINCT_SUM with no crosstab anywhere: the value half
// rides Field and is projected by the aggregation loop that has always
// existed, while the KEY named by params.distinct_by is reachable only
// through addAggParamFields. Reverting that one lookup makes every record
// look like a missing key and the aggregation answers over an empty set;
// reverting the MarginAggregations walk leaves this test GREEN.
//
// Projection on the plain Process path is gated on ProjectBufferedFields
// (the crosstab path installs it unconditionally), so the flag is set
// here for the same reason the pulse facade sets it by default.
func TestProcess_ProjectionCoversDistinctByParam(t *testing.T) {
	ctx := context.Background()
	schema := distinctSumProjectionSchema()
	cfg := setupTestFS(t, "distinct_sum_proj.pulse", schema, distinctSumProjectionRecords())

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "distinct_sum_proj.pulse"},
		Aggregations: []*types.Aggregation{{
			Type:   types.AGG_DISTINCT_SUM,
			Field:  "weight",
			Label:  "weighted_base",
			Params: json.RawMessage(`{"distinct_by":"respondent"}`),
		}},
	}

	svc := New(cfg)
	svc.SetProjectBufferedFields(true)
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("no data rows")
	}
	raw, ok := resp.Data[0]["weighted_base"]
	if !ok {
		t.Fatalf("no weighted_base on the result row: %#v", resp.Data[0])
	}
	got, ok := raw.(float64)
	if !ok {
		t.Fatalf("value = %#v (%T); a nil value means the distinct key was never decoded", raw, raw)
	}
	if math.Abs(got-28) > 1e-9 {
		t.Errorf("distinct sum = %v, want 28 (weights 1..7 summed once per respondent)", got)
	}
}
