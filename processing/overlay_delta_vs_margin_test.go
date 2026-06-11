package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// deltaMeanPayload returns a 3 × 3 host MatrixPayload whose row /
// column / grand margins are the MEAN of the slice they cover. Mirrors
// zscoreMeanPayload so the per-axis delta arithmetic is easy to
// hand-verify: each cell minus its row mean (axis=row), column mean
// (axis=column), or grand mean (axis=grand) lands on a uniform
// (-Δ, 0, +Δ) shape per slice.
//
// Cells:
//
//	     c0   c1   c2   | row_mean
//	r0   10   20   30   |   20
//	r1   40   50   60   |   50
//	r2   70   80   90   |   80
//	col  40   50   60   |  grand_mean = 50
//
// Margins are the means of the slice, matching what AGG_MEAN would
// emit; the handler does NOT recompute, it consumes whatever value the
// host produced.
func deltaMeanPayload() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}, {Value: 30.0, Present: true}},
			{{Value: 40.0, Present: true}, {Value: 50.0, Present: true}, {Value: 60.0, Present: true}},
			{{Value: 70.0, Present: true}, {Value: 80.0, Present: true}, {Value: 90.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 20.0, Present: true},
			{Value: 50.0, Present: true},
			{Value: 80.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 40.0, Present: true},
			{Value: 50.0, Present: true},
			{Value: 60.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 50.0, Present: true},
		CellLabel:  "AGG_MEAN_value",
	}
}

// TestOverlay_DeltaVsMargin_RowAxis verifies the ROW-axis dispatch of
// OVERLAY_DELTA_VS_MARGIN. Per cell: cell - row_mean → uniform
// (-10, 0, +10) shape per row. Per-row sum of deltas must be exactly
// zero (the cells centre on the row's mean by construction). No
// warnings — DELTA_VS_MARGIN never emits PULSE_OVERLAY_REF_ZERO
// because there is no division.
func TestOverlay_DeltaVsMargin_RowAxis(t *testing.T) {
	host := NewCrosstabHostView(deltaMeanPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindDeltaVsMargin,
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
		t.Fatalf("expected no warnings (DELTA_VS_MARGIN has no division), got %d: %+v",
			len(warnings), warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	matrix := layers[0].Payload.Matrix
	if matrix == nil {
		t.Fatalf("layer.Payload.Matrix nil")
	}

	const eps = 1e-9
	// Per row the deviations from the row margin are uniform shape
	// (-10, 0, +10).
	expected := [][]float64{
		{-10.0, 0.0, 10.0},
		{-10.0, 0.0, 10.0},
		{-10.0, 0.0, 10.0},
	}

	for i, row := range matrix.Cells {
		var rowSum float64
		for j, cell := range row {
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent", i, j)
			}
			v, ok := cell.Value.(float64)
			if !ok {
				t.Fatalf("cell[%d][%d] value = %T, want float64", i, j, cell.Value)
			}
			if math.Abs(v-expected[i][j]) > eps {
				t.Fatalf("cell[%d][%d] delta = %v, want %v", i, j, v, expected[i][j])
			}
			rowSum += v
		}
		if math.Abs(rowSum) > eps {
			t.Fatalf("row %d delta sum = %v, want ≈ 0 (cells centre on row margin)", i, rowSum)
		}
	}

	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil")
	}
	if layers[0].Summary.Count == nil || *layers[0].Summary.Count != 9 {
		t.Fatalf("summary count = %v, want 9", layers[0].Summary.Count)
	}
	if layers[0].Summary.Baseline == nil || *layers[0].Summary.Baseline != 0.0 {
		t.Fatalf("summary baseline = %v, want 0", layers[0].Summary.Baseline)
	}
	if layers[0].Summary.Min == nil || *layers[0].Summary.Min != -10.0 {
		t.Fatalf("summary min = %v, want -10", layers[0].Summary.Min)
	}
	if layers[0].Summary.Max == nil || *layers[0].Summary.Max != 10.0 {
		t.Fatalf("summary max = %v, want 10", layers[0].Summary.Max)
	}
}

// TestOverlay_DeltaVsMargin_ColAxis verifies the COLUMN-axis dispatch.
// Per cell: cell - col_mean → uniform (-30, 0, +30) shape per column.
// Per-column sum of deltas must be exactly zero.
func TestOverlay_DeltaVsMargin_ColAxis(t *testing.T) {
	host := NewCrosstabHostView(deltaMeanPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindDeltaVsMargin,
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
	matrix := layers[0].Payload.Matrix

	const eps = 1e-9
	// Per column the deviations from the column margin are uniform
	// shape (-30, 0, +30).
	expected := [][]float64{
		{-30.0, -30.0, -30.0},
		{0.0, 0.0, 0.0},
		{30.0, 30.0, 30.0},
	}

	for i, row := range matrix.Cells {
		for j, cell := range row {
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent", i, j)
			}
			v := cell.Value.(float64)
			if math.Abs(v-expected[i][j]) > eps {
				t.Fatalf("cell[%d][%d] delta = %v, want %v", i, j, v, expected[i][j])
			}
		}
	}

	// Per-column sum should be exactly 0 (each column centres on its mean).
	for j := 0; j < 3; j++ {
		var sum float64
		for i := 0; i < 3; i++ {
			sum += matrix.Cells[i][j].Value.(float64)
		}
		if math.Abs(sum) > eps {
			t.Fatalf("col %d delta sum = %v, want ≈ 0", j, sum)
		}
	}

	if layers[0].Summary == nil ||
		layers[0].Summary.Count == nil || *layers[0].Summary.Count != 9 {
		t.Fatalf("summary count = %v, want 9", layers[0].Summary)
	}
}

// TestOverlay_DeltaVsMargin_GrandAxis verifies the GRAND-axis dispatch.
// Every cell minus the grand-total slot. The whole-matrix sum of deltas
// must be exactly zero (cells centre on the grand-mean centerpoint)
// and the centre cell (which equals the grand mean) must delta to 0.
func TestOverlay_DeltaVsMargin_GrandAxis(t *testing.T) {
	host := NewCrosstabHostView(deltaMeanPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindDeltaVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand},
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
	matrix := layers[0].Payload.Matrix

	const eps = 1e-9
	grandMargin := 50.0
	cellValues := [3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	}
	var sum float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			cell := matrix.Cells[i][j]
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent", i, j)
			}
			v := cell.Value.(float64)
			want := cellValues[i][j] - grandMargin
			if math.Abs(v-want) > eps {
				t.Fatalf("cell[%d][%d] delta = %v, want %v", i, j, v, want)
			}
			sum += v
		}
	}
	if math.Abs(sum) > eps {
		t.Fatalf("grand-axis whole-matrix delta sum = %v, want ≈ 0", sum)
	}

	// Center cell (50) equals the grand margin exactly so its delta = 0.
	if v := matrix.Cells[1][1].Value.(float64); math.Abs(v) > eps {
		t.Fatalf("center cell delta = %v, want 0 (cell equals grand margin)", v)
	}

	// Synthesised name should carry the chosen axis.
	if got, want := layers[0].Name, "OVERLAY_DELTA_VS_MARGIN_grand"; got != want {
		t.Fatalf("synthesised name = %q, want %q", got, want)
	}
}

// TestOverlay_DeltaVsMargin_NullHostCell_AbsentOverlay verifies the
// existing null contract: when a host cell is absent (Present=false)
// the overlay cell stays absent — DELTA_VS_MARGIN never invents a
// value from a missing observation. No warnings are emitted because
// there is no division-by-zero hazard; the absent overlay cell is the
// only signal.
func TestOverlay_DeltaVsMargin_NullHostCell_AbsentOverlay(t *testing.T) {
	// Construct a payload where the (1,1) center cell is absent.
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}, {Value: 30.0, Present: true}},
			{{Value: 40.0, Present: true}, {Present: false}, {Value: 60.0, Present: true}},
			{{Value: 70.0, Present: true}, {Value: 80.0, Present: true}, {Value: 90.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 20.0, Present: true},
			{Value: 50.0, Present: true},
			{Value: 80.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 40.0, Present: true},
			{Value: 50.0, Present: true},
			{Value: 60.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 50.0, Present: true},
		CellLabel:  "AGG_MEAN_value",
	}
	host := NewCrosstabHostView(payload)

	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindDeltaVsMargin,
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
		t.Fatalf("expected zero warnings on absent host cell (no PULSE_OVERLAY_REF_ZERO for DELTA_VS_MARGIN); got %d: %+v",
			len(warnings), warnings)
	}
	matrix := layers[0].Payload.Matrix

	// Center overlay cell must be absent — the host cell was null and
	// DELTA_VS_MARGIN does not invent a value.
	if matrix.Cells[1][1].Present {
		t.Fatalf("(1,1) overlay cell present; want absent (host cell was null)")
	}

	// Every other cell should be present with the expected delta.
	const eps = 1e-9
	rowMargins := []float64{20, 50, 80}
	cellValues := [3][3]float64{
		{10, 20, 30},
		{40, 0, 60},
		{70, 80, 90},
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i == 1 && j == 1 {
				continue
			}
			cell := matrix.Cells[i][j]
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent; want present", i, j)
			}
			v := cell.Value.(float64)
			want := cellValues[i][j] - rowMargins[i]
			if math.Abs(v-want) > eps {
				t.Fatalf("cell[%d][%d] delta = %v, want %v", i, j, v, want)
			}
		}
	}

	// Summary count should equal 8 (9 cells minus the 1 absent host).
	if layers[0].Summary == nil ||
		layers[0].Summary.Count == nil || *layers[0].Summary.Count != 8 {
		t.Fatalf("summary count = %v, want 8", layers[0].Summary)
	}
}
