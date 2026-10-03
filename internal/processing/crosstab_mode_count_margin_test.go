package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// modeCountMarginSchema / modeCountMarginRecords: a 2×2 region × segment
// grid over a small-integer measure v.
//
//	(north, retail):    [1, 1, 2]  modal count 2
//	(north, wholesale): [2, 2, 3]  modal count 2
//	(south, retail):    [2, 3]     modal count 1
//	(south, wholesale): [4]        modal count 1
//
// Every margin's modal count over its OWN raw rows differs from the sum
// of its cells: north [1,1,2,2,2,3] → 3 (cells sum 4), south [2,3,4] → 1
// (sum 2), retail [1,1,2,2,3] → 2 (sum 3), wholesale [2,2,3,4] → 2
// (sum 3), grand → 4 (value 2 holds four rows; sum 6).
func modeCountMarginSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add: %v", err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("north", "south")},
		{Name: "segment", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("retail", "wholesale")},
		{Name: "v", Type: encoding.FieldTypeF64},
	}}
}

func modeCountMarginRecords(schema *encoding.Schema) []*Record {
	mk := func(region, segment, v float64) *Record {
		return NewRecord(schema, map[string]float64{"region": region, "segment": segment, "v": v})
	}
	return []*Record{
		mk(0, 0, 1), mk(0, 0, 1), mk(0, 0, 2),
		mk(0, 1, 2), mk(0, 1, 2), mk(0, 1, 3),
		mk(1, 0, 2), mk(1, 0, 3),
		mk(1, 1, 4),
	}
}

func modeCountMarginRequest(cell types.AggregationType, params json.RawMessage) *types.Request {
	return &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
		Cell:    &types.Aggregation{Type: cell, Field: "v", Label: "m", Params: params},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}}
}

// runFusedCrosstabUngated drives the fused state WITHOUT AssertCanFuse,
// so a cell the dispatch routes to the buffered path can still be
// checked for what the fused accumulators would compute.
func runFusedCrosstabUngated(t *testing.T, schema *encoding.Schema, req *types.Request, recs []*Record) *types.Response {
	t.Helper()
	state, err := NewFusedCrosstabState(req.Crosstab, schema, &ExtensionRegistry{})
	if err != nil {
		t.Fatalf("NewFusedCrosstabState: %v", err)
	}
	for _, r := range recs {
		state.AddTotalRow()
		if err := state.Update(r); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	resp, err := state.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return resp
}

func marginScalars(m *types.MatrixPayload) (rows, cols []float64, grand float64) {
	for _, c := range m.RowMargins {
		rows = append(rows, c.Scalar())
	}
	for _, c := range m.ColumnMargins {
		cols = append(cols, c.Scalar())
	}
	return rows, cols, m.GrandTotal.Scalar()
}

func assertMargins(t *testing.T, arm string, m *types.MatrixPayload, wantRows, wantCols []float64, wantGrand float64) {
	t.Helper()
	rows, cols, grand := marginScalars(m)
	if !equalFloats(rows, wantRows) || !equalFloats(cols, wantCols) || grand != wantGrand {
		t.Errorf("%s margins: rows %v cols %v grand %v; want rows %v cols %v grand %v",
			arm, rows, cols, grand, wantRows, wantCols, wantGrand)
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCrosstab_ModeCountMarginRecomputes: an AGG_MODE_COUNT cell's
// margins are the modal count of each margin's raw rows — never the sum
// (nor the max) of the cells' modal counts — on the buffered arm, and
// the fused accumulators compute the same figures. Because the margin is
// not a sum of cells the operator is MarginRecompute, so the fused gate
// routes the cell to the buffered path.
func TestCrosstab_ModeCountMarginRecomputes(t *testing.T) {
	schema := modeCountMarginSchema(t)
	recs := modeCountMarginRecords(schema)
	req := modeCountMarginRequest(types.AGG_MODE_COUNT, nil)

	buffered := runBufferedCrosstab(t, schema, req, recs)
	if got := buffered.Crosstab.Matrix.Cells; got[0][0].Scalar() != 2 || got[0][1].Scalar() != 2 || got[1][0].Scalar() != 1 || got[1][1].Scalar() != 1 {
		t.Fatalf("cells = %v, want [[2 2] [1 1]]", got)
	}
	assertMargins(t, "buffered", buffered.Crosstab.Matrix, []float64{3, 1}, []float64{2, 2}, 4)

	fused := runFusedCrosstabUngated(t, schema, req, recs)
	assertMatrixEqual(t, buffered.Crosstab.Matrix, fused.Crosstab.Matrix)

	ok, reason := CanFuseCrosstab(req, schema, nil)
	if ok || !strings.Contains(reason, "recompute-margin cell aggregator (AGG_MODE_COUNT)") {
		t.Errorf("CanFuseCrosstab = %v, %q; want a recompute-margin refusal", ok, reason)
	}
}

// TestCrosstab_FrequencyMarginSumsCells: AGG_FREQUENCY counts the rows
// equal to one value, so each margin IS the sum of its cells — the
// MarginSummable class — and the fused arm (admitted) matches buffered.
func TestCrosstab_FrequencyMarginSumsCells(t *testing.T) {
	schema := modeCountMarginSchema(t)
	recs := modeCountMarginRecords(schema)
	req := modeCountMarginRequest(types.AGG_FREQUENCY, json.RawMessage(`{"value": 3}`))

	buffered := runBufferedCrosstab(t, schema, req, recs)
	// value 3: north-retail 0, north-wholesale 1, south-retail 1,
	// south-wholesale 0 — every margin is the sum of its cells.
	assertMargins(t, "buffered", buffered.Crosstab.Matrix, []float64{1, 1}, []float64{1, 1}, 2)

	if ok, reason := CanFuseCrosstab(req, schema, nil); !ok {
		t.Fatalf("CanFuseCrosstab refused AGG_FREQUENCY: %s", reason)
	}
	fused := runFusedCrosstab(t, schema, req, recs)
	assertMatrixEqual(t, buffered.Crosstab.Matrix, fused.Crosstab.Matrix)
}
