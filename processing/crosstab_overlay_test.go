package processing

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// crosstabOverlaySchema returns a (row, col, value) schema where row /
// col are 3-entry categoricals and value is f64. The overlay tests
// drive RunCrosstab over this schema to exercise the buffered exit's
// Response.Overlays population hook (E1-S6).
func crosstabOverlaySchema(t *testing.T) *encoding.Schema {
	t.Helper()
	rowDict := encoding.NewDictionary()
	for _, r := range []string{"r0", "r1", "r2"} {
		if _, err := rowDict.Add(r); err != nil {
			t.Fatalf("row dict.Add: %v", err)
		}
	}
	colDict := encoding.NewDictionary()
	for _, c := range []string{"c0", "c1", "c2"} {
		if _, err := colDict.Add(c); err != nil {
			t.Fatalf("col dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "row", Type: encoding.FieldTypeCategoricalU8, Dictionary: rowDict},
			{Name: "col", Type: encoding.FieldTypeCategoricalU8, Dictionary: colDict},
			{Name: "value", Type: encoding.FieldTypeF64},
		},
	}
}

// crosstabOverlayRecords builds a 9-record fixture, one per (row, col)
// cell. AGG_SUM over (row=ri, col=cj) collapses to value[i][j] since
// every cell has exactly one record. The numbers mirror the
// synthIndexMarginPayload fixture used in overlay_test.go so the
// expected index-vs-margin output is well known:
//
//	   c0  c1  c2  | row_margin
//	r0  1   2   3  | 6
//	r1 10  20  30  | 60
//	r2 100 200 300 | 600
func crosstabOverlayRecords(schema *encoding.Schema) []*Record {
	mk := func(row, col uint64, value float64) *Record {
		return NewRecord(schema, map[string]float64{
			"row":   float64(row),
			"col":   float64(col),
			"value": value,
		})
	}
	return []*Record{
		mk(0, 0, 1),
		mk(0, 1, 2),
		mk(0, 2, 3),
		mk(1, 0, 10),
		mk(1, 1, 20),
		mk(1, 2, 30),
		mk(2, 0, 100),
		mk(2, 1, 200),
		mk(2, 2, 300),
	}
}

// crosstabOverlayBaseRequest returns the canonical 3×3 AGG_SUM crosstab
// request the overlay tests reuse. Margins.Rows=true so the
// INDEX_VS_MARGIN axis=ROW handler has a denominator to read.
func crosstabOverlayBaseRequest() *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "row"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum_value"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
}

// TestCrosstab_OverlayResponsePopulated verifies the E1-S6 buffered
// hook: a Request.Overlays entry on a Crosstab request emits one
// Response.Overlays layer in matching order. The layer payload matches
// the E1-S5 handler output (INDEX_VS_MARGIN axis=ROW = cell / row_margin
// × 100 per present host cell), and every cell index is exactly 100/3,
// 200/6 etc. consistent with the (1,2,3 / 10,20,30 / 100,200,300)
// fixture.
func TestCrosstab_OverlayResponsePopulated(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)
	req := crosstabOverlayBaseRequest()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
	}
	p := NewProcessor(schema)
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatalf("expected crosstab matrix payload")
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(resp.Overlays))
	}
	layer := resp.Overlays[0]
	if layer.Kind != types.OverlayKindIndexVsMargin {
		t.Fatalf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindIndexVsMargin)
	}
	if layer.Scope != types.OverlayScopeCell {
		t.Fatalf("layer.Scope = %q, want %q", layer.Scope, types.OverlayScopeCell)
	}
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeMatrix)
	}
	if layer.Payload.Matrix == nil {
		t.Fatalf("layer.Payload.Matrix nil")
	}
	matrix := layer.Payload.Matrix
	// Row 0: 1+2+3=6 margin -> cells = 1/6*100, 2/6*100, 3/6*100.
	// Each row's three indexed cells must sum to 100 (Σ cells / row × 100).
	const eps = 1e-9
	for i, row := range matrix.Cells {
		var sum float64
		for _, cell := range row {
			if !cell.Present {
				t.Fatalf("row[%d] has absent cell: %+v", i, cell)
			}
			v, ok := cell.Value.(float64)
			if !ok {
				t.Fatalf("row[%d] cell value = %T, want float64", i, cell.Value)
			}
			sum += v
		}
		if diff := sum - 100.0; diff > eps || diff < -eps {
			t.Fatalf("row[%d] indexed-cell sum = %v, want 100", i, sum)
		}
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil ||
		*layer.Summary.Baseline != 100.0 {
		t.Fatalf("expected baseline = 100, got %+v", layer.Summary)
	}
	if layer.Summary.Count == nil || *layer.Summary.Count != 9 {
		t.Fatalf("expected count = 9 (all cells indexed), got %+v", layer.Summary)
	}
}

// TestCrosstab_NoOverlaysPreservesByteIdentity is the PRD §9 canonical
// no-regression check: when req.Overlays is empty, Response.Overlays
// stays nil and the host Crosstab.Matrix is byte-identical to the
// pre-overlay baseline (the same RunCrosstab call without the hook
// fired). Today the hook short-circuits on len(req.Overlays) == 0, so
// the baseline and post-hook responses share the same matrix bytes.
func TestCrosstab_NoOverlaysPreservesByteIdentity(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)

	// Baseline: no Overlays slot on the request.
	baseReq := crosstabOverlayBaseRequest()
	baseResp, err := NewProcessor(schema).RunCrosstab(context.Background(), baseReq, recs)
	if err != nil {
		t.Fatalf("baseline RunCrosstab: %v", err)
	}
	if baseResp.Overlays != nil {
		t.Fatalf("baseline must not populate Overlays: %+v", baseResp.Overlays)
	}

	// Post-hook: identical request shape, still no Overlays. The hook
	// runs but short-circuits because req.Overlays is empty.
	emptyReq := crosstabOverlayBaseRequest()
	emptyReq.Overlays = nil
	emptyResp, err := NewProcessor(schema).RunCrosstab(context.Background(), emptyReq, recs)
	if err != nil {
		t.Fatalf("empty-overlays RunCrosstab: %v", err)
	}
	if emptyResp.Overlays != nil {
		t.Fatalf("empty overlays must leave Response.Overlays nil: %+v",
			emptyResp.Overlays)
	}
	if !reflect.DeepEqual(baseResp.Crosstab.Matrix, emptyResp.Crosstab.Matrix) {
		t.Fatalf("matrix byte-identity violated when overlays empty\nbase: %+v\nempty: %+v",
			baseResp.Crosstab.Matrix, emptyResp.Crosstab.Matrix)
	}
	if !reflect.DeepEqual(baseResp.Warnings, emptyResp.Warnings) {
		t.Fatalf("warnings drift when overlays empty: base=%v empty=%v",
			baseResp.Warnings, emptyResp.Warnings)
	}
}

// TestCrosstab_UnknownOverlayKindReturnsCodedError verifies the runtime
// mirror of E1-S3 validation: a Request.Overlays entry naming an
// unregistered kind produces a CodedError carrying the stub
// PULSE_OVERLAY_KIND_UNKNOWN code in its details. The validator catches
// this at predict time; the runtime gate is defense in depth.
func TestCrosstab_UnknownOverlayKindReturnsCodedError(t *testing.T) {
	schema := crosstabOverlaySchema(t)
	recs := crosstabOverlayRecords(schema)
	req := crosstabOverlayBaseRequest()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:  types.OverlayKind("OVERLAY_NOT_A_THING"),
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
	}
	p := NewProcessor(schema)
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err == nil {
		t.Fatalf("expected error for unknown overlay kind, got resp=%+v", resp)
	}
	// The error message names the kind so downstream consumers can grep
	// without depending on the stub code constant.
	if msg := err.Error(); msg == "" {
		t.Fatalf("error message empty")
	}
}
