package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestOverlay_ShareOfRow_CellByCell verifies the OVERLAY_SHARE_OF_ROW
// handler computes cell / row_margin for every present host cell.
// Uses synthIndexMarginPayload — the 3 × 3 matrix where:
//
//	   c0    c1    c2  | row_margin
//	r0  1     2     3  | 6
//	r1 10    20    30  | 60
//	r2 100  200   300  | 600
//
// SHARE_OF_ROW with axis=ROW therefore produces:
//
//	r0: 1/6 ≈ 0.1667,   2/6 ≈ 0.3333,   3/6 = 0.5
//	r1: 10/60 ≈ 0.1667, 20/60 ≈ 0.3333, 30/60 = 0.5
//	r2: 100/600 ≈ 0.1667, 200/600 ≈ 0.3333, 300/600 = 0.5
//
// Each row's cells sum to 1.0 exactly (the share-of-row contract).
func TestOverlay_ShareOfRow_CellByCell(t *testing.T) {
	host := NewCrosstabHostView(synthIndexMarginPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
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
	if layer.Kind != types.OverlayKindShareOfRow {
		t.Fatalf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindShareOfRow)
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
	// Expected per-row column shares (1/6, 2/6, 3/6) repeat for each
	// row because synthIndexMarginPayload has the same column ratio
	// across rows.
	expected := [][]float64{
		{1.0 / 6.0, 2.0 / 6.0, 3.0 / 6.0},
		{10.0 / 60.0, 20.0 / 60.0, 30.0 / 60.0},
		{100.0 / 600.0, 200.0 / 600.0, 300.0 / 600.0},
	}

	for i, row := range matrix.Cells {
		if len(row) != 3 {
			t.Fatalf("matrix.Cells[%d] col count = %d, want 3", i, len(row))
		}
		var sum float64
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
			sum += v
		}
		// Per-row sum must be 1.0 exactly (the SHARE_OF_ROW contract).
		if math.Abs(sum-1.0) > eps {
			t.Fatalf("row[%d] sum = %v, want 1.0", i, sum)
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

// TestOverlay_ShareOfRow_MissingMargin_EmitsWarning verifies that a
// SHARE_OF_ROW spec on a payload whose RowMargins slot is unset
// surfaces one PULSE_OVERLAY_REF_ZERO warning per host cell and
// leaves the overlay cells absent. Mirrors the INDEX_VS_MARGIN
// missing-margin defense.
func TestOverlay_ShareOfRow_MissingMargin_EmitsWarning(t *testing.T) {
	payload := synthIndexMarginPayload()
	payload.RowMargins = nil // simulate "row margin not computed"
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
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

// TestOverlay_ShareOfRow_ZeroMargin_EmitsWarningNotInf verifies that a
// zero row-margin denominator surfaces PULSE_OVERLAY_REF_ZERO and
// emits an absent overlay cell rather than leaking Inf / NaN.
func TestOverlay_ShareOfRow_ZeroMargin_EmitsWarningNotInf(t *testing.T) {
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{{Value: 5.0, Present: true}, {Value: 10.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 0.0, Present: true},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
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
	for _, cell := range layers[0].Payload.Matrix.Cells[0] {
		if cell.Present {
			t.Fatalf("cell should be absent when margin == 0, got %+v", cell)
		}
		if v, ok := cell.Value.(float64); ok && (math.IsInf(v, 0) || math.IsNaN(v)) {
			t.Fatalf("cell value leaked infinity / NaN: %v", v)
		}
	}
}

// TestOverlay_ShareOfRow_AbsentCellsStayAbsent verifies that a
// structurally missing host cell (Present=false) produces an absent
// overlay cell without emitting a warning. Defends against false
// positive warnings for cells that genuinely had no observation.
func TestOverlay_ShareOfRow_AbsentCellsStayAbsent(t *testing.T) {
	payload := synthIndexMarginPayload()
	payload.Cells[1][1] = types.MatrixCell{Present: false}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
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

// TestOverlay_ShareOfRow_DefaultLayerName verifies SHARE_OF_ROW
// synthesises a deterministic default name when Name is omitted.
func TestOverlay_ShareOfRow_DefaultLayerName(t *testing.T) {
	host := NewCrosstabHostView(synthIndexMarginPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if got, want := layers[0].Name, "OVERLAY_SHARE_OF_ROW_row"; got != want {
		t.Fatalf("synthesised name = %q, want %q", got, want)
	}
}
