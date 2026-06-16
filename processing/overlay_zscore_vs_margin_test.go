package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// zscoreMeanPayload returns a 3 × 3 host MatrixPayload whose row /
// column / grand margins are the MEAN of the slice they cover (rather
// than the AGG_SUM that synthIndexMarginPayload models). Z-scores
// computed against a slice-mean margin produce values that average to
// 0 across each margin slice, which makes hand-computation transparent
// and matches the canonical "(cell - mean) / sd" interpretation a
// statistician expects when reading a ZSCORE_VS_MARGIN layer.
//
// Cells (deliberately picked so each row / column slice is non-
// degenerate but easy to evaluate):
//
//	     c0   c1   c2   | row_mean
//	r0   10   20   30   |   20
//	r1   40   50   60   |   50
//	r2   70   80   90   |   80
//	col  40   50   60   |  grand_mean = 50
//
// Per-row population variance: ((10-20)² + (20-20)² + (30-20)²)/3 = 200/3
//
//	→ sd_row0 = sqrt(200/3) ≈ 8.16496580927726
//	→ sd_row1 / sd_row2 identical (same shape per row).
//
// Per-column population variance: ((10-40)² + (40-40)² + (70-40)²)/3 = 600
//
//	→ sd_col0 = sqrt(600) ≈ 24.49489742783178
//	→ sd_col1 / sd_col2 identical (same shape per column).
//
// Grand variance: every cell minus mean=50 → squared deviations:
//
//	(10,20,30,40,50,60,70,80,90) - 50 = (-40,-30,-20,-10,0,10,20,30,40)
//	squared sum = 1600+900+400+100+0+100+400+900+1600 = 6000
//	variance = 6000 / 9 = 666.666...
//	sd_grand = sqrt(666.666...) ≈ 25.81988897471611
//
// Margins are filled in as cells so MarginFor returns the matching
// mean; this is the same shape AGG_MEAN would produce in a real host
// crosstab.
func zscoreMeanPayload() *types.MatrixPayload {
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

// TestOverlay_ZScoreVsMargin_RowAxis verifies the ROW-axis dispatch
// of OVERLAY_ZSCORE_VS_MARGIN. Each cell is normalized against its
// row's margin (= row mean here) and divided by the population
// standard deviation of the row's cells. Per-row mean of the produced
// scores must be zero (the cells centre on the row's centerpoint by
// construction) and the produced layer must match hand-computed
// expectations exactly within 1e-9.
func TestOverlay_ZScoreVsMargin_RowAxis(t *testing.T) {
	host := NewCrosstabHostView(zscoreMeanPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindZScoreVsMargin,
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
	matrix := layers[0].Payload.Matrix
	if matrix == nil {
		t.Fatalf("layer.Payload.Matrix nil")
	}

	const eps = 1e-9
	// Row r0: cells (10,20,30), margin=20, sd=sqrt(200/3).
	sdRow0 := math.Sqrt(200.0 / 3.0)
	// Row r1: cells (40,50,60), margin=50, sd=sqrt(200/3).
	sdRow1 := math.Sqrt(200.0 / 3.0)
	// Row r2: cells (70,80,90), margin=80, sd=sqrt(200/3).
	sdRow2 := math.Sqrt(200.0 / 3.0)

	// Per row the deviations from the row margin are uniform shape
	// (-10, 0, +10) → expected[i][j] = deviation / sdRow{i}.
	expected := [][]float64{
		{-10.0 / sdRow0, 0.0 / sdRow0, 10.0 / sdRow0},
		{-10.0 / sdRow1, 0.0 / sdRow1, 10.0 / sdRow1},
		{-10.0 / sdRow2, 0.0 / sdRow2, 10.0 / sdRow2},
	}

	for i, row := range matrix.Cells {
		var sliceSum float64
		for j, cell := range row {
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent", i, j)
			}
			v, ok := cell.Value.(float64)
			if !ok {
				t.Fatalf("cell[%d][%d] value = %T, want float64", i, j, cell.Value)
			}
			if math.Abs(v-expected[i][j]) > eps {
				t.Fatalf("cell[%d][%d] z = %v, want %v", i, j, v, expected[i][j])
			}
			sliceSum += v
		}
		if math.Abs(sliceSum) > eps {
			t.Fatalf("row %d slice sum = %v, want ≈ 0 (z-scores centre on margin)", i, sliceSum)
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
}

// TestOverlay_ZScoreVsMargin_ColAxis verifies the COLUMN-axis
// dispatch. Per-column standard deviation, per-column margin
// (= column mean) subtracted from each cell, ÷ sd → per-column
// distribution of z-scores summing to zero within each column.
func TestOverlay_ZScoreVsMargin_ColAxis(t *testing.T) {
	host := NewCrosstabHostView(zscoreMeanPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindZScoreVsMargin,
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
	// Each column: cells differ by 30 from the mean of the column.
	// Variance = ((c-mean)² ...) / 3, all columns identical shape.
	// Col c0: (10,40,70), mean=40, var = (900+0+900)/3 = 600.
	sdCol0 := math.Sqrt(600.0)
	sdCol1 := math.Sqrt(600.0)
	sdCol2 := math.Sqrt(600.0)

	// Per column the deviations from the column margin are uniform
	// shape (-30, 0, +30) → expected[i][j] = deviation / sdCol{j}.
	expected := [][]float64{
		{-30.0 / sdCol0, -30.0 / sdCol1, -30.0 / sdCol2},
		{0.0 / sdCol0, 0.0 / sdCol1, 0.0 / sdCol2},
		{30.0 / sdCol0, 30.0 / sdCol1, 30.0 / sdCol2},
	}

	for i, row := range matrix.Cells {
		for j, cell := range row {
			if !cell.Present {
				t.Fatalf("cell[%d][%d] absent", i, j)
			}
			v := cell.Value.(float64)
			if math.Abs(v-expected[i][j]) > eps {
				t.Fatalf("cell[%d][%d] z = %v, want %v", i, j, v, expected[i][j])
			}
		}
	}

	// Per-column sum should be ≈ 0 (each column centres on its mean).
	for j := 0; j < 3; j++ {
		var sum float64
		for i := 0; i < 3; i++ {
			sum += matrix.Cells[i][j].Value.(float64)
		}
		if math.Abs(sum) > eps {
			t.Fatalf("col %d sum = %v, want ≈ 0", j, sum)
		}
	}

	if layers[0].Summary == nil ||
		layers[0].Summary.Count == nil || *layers[0].Summary.Count != 9 {
		t.Fatalf("summary count = %v, want 9", layers[0].Summary)
	}
}

// TestOverlay_ZScoreVsMargin_GrandAxis verifies the GRAND-axis
// dispatch. One sd over every present cell in the matrix; margin =
// grand-total slot. The whole-matrix sum of z-scores must be ≈ 0
// (cells centre on the grand-mean centerpoint) and individual values
// must match hand-computed expectations.
func TestOverlay_ZScoreVsMargin_GrandAxis(t *testing.T) {
	host := NewCrosstabHostView(zscoreMeanPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindZScoreVsMargin,
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
	// Grand sd: variance over (10,20,30,40,50,60,70,80,90) about mean 50
	// is sum((x-50)²)/9 = 6000/9 = 666.6667.
	sdGrand := math.Sqrt(6000.0 / 9.0)
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
			want := (cellValues[i][j] - grandMargin) / sdGrand
			if math.Abs(v-want) > eps {
				t.Fatalf("cell[%d][%d] z = %v, want %v", i, j, v, want)
			}
			sum += v
		}
	}
	if math.Abs(sum) > eps {
		t.Fatalf("grand-axis whole-matrix z sum = %v, want ≈ 0", sum)
	}

	// Center cell (50) equals the grand mean exactly so its z = 0.
	if v := matrix.Cells[1][1].Value.(float64); math.Abs(v) > eps {
		t.Fatalf("center cell z = %v, want 0 (cell equals grand margin)", v)
	}
}

// TestOverlay_ZScoreVsMargin_ZeroSD_EmitsWarning verifies that when
// every cell in a margin slice is identical the population sd is zero
// and the handler surfaces one PULSE_OVERLAY_REF_ZERO warning per
// affected cell, leaving the overlay cells absent. Defends the same
// null contract the SHARE_OF_* and INDEX_VS_MARGIN handlers use for
// missing / zero denominators.
func TestOverlay_ZScoreVsMargin_ZeroSD_EmitsWarning(t *testing.T) {
	// Construct a payload whose first row's cells are all identical
	// (sd_row0 = 0). The other two rows have non-zero variance so the
	// warning surface is row-scoped, not whole-matrix.
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{{Value: 5.0, Present: true}, {Value: 5.0, Present: true}, {Value: 5.0, Present: true}},
			{{Value: 40.0, Present: true}, {Value: 50.0, Present: true}, {Value: 60.0, Present: true}},
			{{Value: 70.0, Present: true}, {Value: 80.0, Present: true}, {Value: 90.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 5.0, Present: true},
			{Value: 50.0, Present: true},
			{Value: 80.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 38.333, Present: true},
			{Value: 45.0, Present: true},
			{Value: 51.666, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 45.0, Present: true},
		CellLabel:  "AGG_MEAN_value",
	}
	host := NewCrosstabHostView(payload)

	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindZScoreVsMargin,
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
	// Three cells in row 0 each fail with sd=0.
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings (one per row-0 cell), got %d: %+v", len(warnings), warnings)
	}
	for i, w := range warnings {
		if w.Code != string(errors.PULSE_OVERLAY_REF_ZERO) {
			t.Fatalf("warning[%d] code = %q, want %q", i, w.Code,
				string(errors.PULSE_OVERLAY_REF_ZERO))
		}
	}
	matrix := layers[0].Payload.Matrix
	for j := 0; j < 3; j++ {
		if matrix.Cells[0][j].Present {
			t.Fatalf("row-0 cell[%d] should be absent when sd=0", j)
		}
	}
	// Rows 1 and 2 still produced valid z-scores.
	for i := 1; i < 3; i++ {
		for j := 0; j < 3; j++ {
			cell := matrix.Cells[i][j]
			if !cell.Present {
				t.Fatalf("row %d col %d should be present (non-degenerate slice)", i, j)
			}
			v := cell.Value.(float64)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("cell[%d][%d] value leaked non-finite: %v", i, j, v)
			}
		}
	}
	if layers[0].Summary == nil ||
		layers[0].Summary.Count == nil || *layers[0].Summary.Count != 6 {
		t.Fatalf("summary count = %v, want 6 (3 + 3 present cells)", layers[0].Summary)
	}
}

// TestOverlay_ZScoreVsMargin_DefaultLayerName verifies ZSCORE_VS_MARGIN
// synthesises a deterministic default name combining the kind with
// the axis the caller chose — unlike the SHARE_OF_* triad whose default
// reflects a structurally-locked axis, ZSCORE_VS_MARGIN honours
// whichever axis the spec named.
func TestOverlay_ZScoreVsMargin_DefaultLayerName(t *testing.T) {
	host := NewCrosstabHostView(zscoreMeanPayload())
	for _, tc := range []struct {
		axis types.MarginAxis
		want string
	}{
		{types.MarginAxisRow, "OVERLAY_ZSCORE_VS_MARGIN_row"},
		{types.MarginAxisColumn, "OVERLAY_ZSCORE_VS_MARGIN_column"},
		{types.MarginAxisGrand, "OVERLAY_ZSCORE_VS_MARGIN_grand"},
	} {
		t.Run(string(tc.axis), func(t *testing.T) {
			specs := []types.OverlaySpec{
				{
					Kind:  types.OverlayKindZScoreVsMargin,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: tc.axis}},
				},
			}
			layers, _, err := ApplyOverlays(specs, host)
			if err != nil {
				t.Fatalf("ApplyOverlays: %v", err)
			}
			if got := layers[0].Name; got != tc.want {
				t.Fatalf("synthesised name = %q, want %q", got, tc.want)
			}
		})
	}
}
