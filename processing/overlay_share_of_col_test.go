package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestOverlay_ShareOfCol_CellByCell verifies the OVERLAY_SHARE_OF_COL
// handler computes cell / col_margin for every present host cell.
// Uses synthIndexMarginPayload — the 3 × 3 matrix where:
//
//	     c0    c1    c2
//	r0    1     2     3
//	r1   10    20    30
//	r2  100   200   300
//	col 111   222   333
//
// SHARE_OF_COL with axis=COLUMN therefore produces:
//
//	r0: 1/111,   2/222,   3/333
//	r1: 10/111,  20/222,  30/333
//	r2: 100/111, 200/222, 300/333
//
// Each column's cells sum to 1.0 exactly (the share-of-column
// contract — mirror of TestOverlay_ShareOfRow_CellByCell).
func TestOverlay_ShareOfCol_CellByCell(t *testing.T) {
	host := NewCrosstabHostView(synthIndexMarginPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
			},
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	layer := layers[0]
	if layer.Kind != types.OverlayKindShareOfCol {
		t.Fatalf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindShareOfCol)
	}
	if layer.Scope != types.OverlayScopeCell {
		t.Fatalf("layer.Scope = %q, want %q", layer.Scope, types.OverlayScopeCell)
	}
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want %q", layer.Payload.Shape, types.OverlayShapeMatrix)
	}
	if layer.Payload.Matrix == nil {
		t.Fatalf("layer.Payload.Matrix nil")
	}
	matrix := layer.Payload.Matrix
	if len(matrix.Cells) != 3 {
		t.Fatalf("matrix.Cells row count = %d, want 3", len(matrix.Cells))
	}

	const eps = 1e-9
	// Expected per-row column shares; cell[i][j] = cell_value / col_margin[j].
	expected := [][]float64{
		{1.0 / 111.0, 2.0 / 222.0, 3.0 / 333.0},
		{10.0 / 111.0, 20.0 / 222.0, 30.0 / 333.0},
		{100.0 / 111.0, 200.0 / 222.0, 300.0 / 333.0},
	}

	for i, row := range matrix.Cells {
		if len(row) != 3 {
			t.Fatalf("matrix.Cells[%d] col count = %d, want 3", i, len(row))
		}
		for j, cell := range row {
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent", i, j)
			}
			v, ok := cell.Value.(float64)
			if !ok {
				t.Fatalf("cell[%d][%d] value = %T, want float64", i, j, cell.Value)
			}
			if math.Abs(v-expected[i][j]) > eps {
				t.Fatalf("cell[%d][%d] share = %v, want %v", i, j, v, expected[i][j])
			}
		}
	}

	// Per-column sum must be 1.0 exactly (the SHARE_OF_COL contract).
	for j := 0; j < 3; j++ {
		var sum float64
		for i := 0; i < 3; i++ {
			cell := matrix.Cells[i][j]
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent in column sum", i, j)
			}
			v := cell.Value.(float64)
			sum += v
		}
		if math.Abs(sum-1.0) > eps {
			t.Fatalf("col[%d] sum = %v, want 1.0", j, sum)
		}
	}

	if layer.Summary == nil {
		t.Fatalf("layer.Summary nil")
	}
	if layer.Summary.Count == nil || *layer.Summary.Count != 9 {
		t.Fatalf("summary count = %v, want 9", layer.Summary.Count)
	}
	if layer.Summary.Baseline == nil {
		t.Fatalf("summary baseline nil; want populated")
	}
}

// TestOverlay_ShareOfCol_MissingMargin_EmitsWarning verifies that a
// SHARE_OF_COL spec on a payload whose ColumnMargins slot is unset
// surfaces one PULSE_OVERLAY_REF_ZERO warning per host cell and
// leaves the overlay cells absent. Mirrors the SHARE_OF_ROW missing-
// margin defense.
func TestOverlay_ShareOfCol_MissingMargin_EmitsWarning(t *testing.T) {
	payload := synthIndexMarginPayload()
	payload.ColumnMargins = nil // simulate "column margin not computed"
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn},
			},
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(warnings) != 9 {
		t.Fatalf("expected 9 warnings (one per cell), got %d", len(warnings))
	}
	for i, w := range warnings {
		if w.Code != string(errors.PULSE_OVERLAY_REF_ZERO) {
			t.Fatalf("warning[%d] code = %q, want %q", i, w.Code,
				string(errors.PULSE_OVERLAY_REF_ZERO))
		}
	}
	matrix := layers[0].Payload.Matrix
	for i, row := range matrix.Cells {
		for j, cell := range row {
			if cell.Present {
				t.Fatalf("cell[%d][%d] should be absent when margin missing", i, j)
			}
		}
	}
	if layer := layers[0]; layer.Summary == nil ||
		layer.Summary.Count == nil || *layer.Summary.Count != 0 {
		t.Fatalf("expected summary count = 0 when all cells fail, got %+v",
			layers[0].Summary)
	}
}

// TestOverlay_ShareOfCol_ZeroMargin_EmitsWarningNotInf verifies that a
// zero column-margin denominator surfaces PULSE_OVERLAY_REF_ZERO and
// emits an absent overlay cell rather than leaking Inf / NaN.
func TestOverlay_ShareOfCol_ZeroMargin_EmitsWarningNotInf(t *testing.T) {
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}},
		Cells: [][]types.MatrixCell{
			{{Value: 5.0, Present: true}},
			{{Value: 10.0, Present: true}},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 0.0, Present: true},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}},
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(warnings))
	}
	for _, w := range warnings {
		if w.Code != string(errors.PULSE_OVERLAY_REF_ZERO) {
			t.Fatalf("warning code = %q, want %q", w.Code,
				string(errors.PULSE_OVERLAY_REF_ZERO))
		}
	}
	for _, row := range layers[0].Payload.Matrix.Cells {
		for _, cell := range row {
			if cell.Present {
				t.Fatalf("cell should be absent when margin == 0, got %+v", cell)
			}
			if v, ok := cell.Value.(float64); ok && (math.IsInf(v, 0) || math.IsNaN(v)) {
				t.Fatalf("cell value leaked infinity / NaN: %v", v)
			}
		}
	}
}

// TestOverlay_ShareOfCol_AbsentCellsStayAbsent verifies that a
// structurally missing host cell (Present=false) produces an absent
// overlay cell without emitting a warning. Defends against false
// positive warnings for cells that genuinely had no observation.
func TestOverlay_ShareOfCol_AbsentCellsStayAbsent(t *testing.T) {
	payload := synthIndexMarginPayload()
	payload.Cells[1][1] = types.MatrixCell{Present: false}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}},
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("absent host cell should not warn; got %+v", warnings)
	}
	if layers[0].Payload.Matrix.Cells[1][1].Present {
		t.Fatalf("expected absent overlay cell for absent host cell")
	}
	if layers[0].Summary == nil ||
		layers[0].Summary.Count == nil || *layers[0].Summary.Count != 8 {
		t.Fatalf("expected count = 8 (one absent), got %+v", layers[0].Summary)
	}
}

// TestOverlay_ShareOfCol_DefaultLayerName verifies SHARE_OF_COL
// synthesises a deterministic default name when Name is omitted.
func TestOverlay_ShareOfCol_DefaultLayerName(t *testing.T) {
	host := NewCrosstabHostView(synthIndexMarginPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfCol,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}},
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if got, want := layers[0].Name, "OVERLAY_SHARE_OF_COL_column"; got != want {
		t.Fatalf("synthesised name = %q, want %q", got, want)
	}
}
