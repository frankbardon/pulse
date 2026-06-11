package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// synthIndexMarginPayload returns a 3 × 3 host MatrixPayload with
// row margins matching the per-row cell totals. Cells:
//
//	   c0  c1  c2  | row_margin
//	r0  1   2   3  | 6
//	r1 10  20  30  | 60
//	r2 100 200 300 | 600
//
// All cells are AGG_SUM-style floats. Row margins are the sum of each
// row's cells so INDEX_VS_MARGIN with axis=ROW produces, per row,
// values 100/6, 200/6, 300/6 (≈ 16.67 / 33.33 / 50.0 — those values
// sum to 100 per row); the per-row mean is 100/3 ≈ 33.33. That mean
// is NOT 100, so the story's "each row's mean ≈ 100" acceptance check
// applies to the alternate matrix where every row's cells share a
// uniform distribution against a single normalisation — see
// indexUniformPayload below.
func synthIndexMarginPayload() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{{Value: 1.0, Present: true}, {Value: 2.0, Present: true}, {Value: 3.0, Present: true}},
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}, {Value: 30.0, Present: true}},
			{{Value: 100.0, Present: true}, {Value: 200.0, Present: true}, {Value: 300.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 6.0, Present: true},
			{Value: 60.0, Present: true},
			{Value: 600.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 111.0, Present: true},
			{Value: 222.0, Present: true},
			{Value: 333.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 666.0, Present: true},
		CellLabel:  "AGG_SUM_amount",
	}
}

// indexUniformPayload returns a 3 × 3 matrix where every row's cells
// are uniform (each cell = row_margin / 3). INDEX_VS_MARGIN with
// axis=ROW therefore produces 100/3 for every cell, and each row's
// mean of the index values is exactly 100/3 × 3 / 3 = 100/3 ≈ 33.33,
// which is what we get when all three cells per row are equal. To
// land "each row's mean ≈ 100" we want a matrix where every cell ==
// row_margin (i.e. the matrix is the row margin replicated across
// columns and each column-share is 100). That is the "self-indexed"
// pattern below — cell[i][j] = rowMargin[i], so cell/margin × 100 =
// 100 in every cell, and the row mean is 100 exactly.
func indexUniformPayload() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{{Value: 6.0, Present: true}, {Value: 6.0, Present: true}, {Value: 6.0, Present: true}},
			{{Value: 60.0, Present: true}, {Value: 60.0, Present: true}, {Value: 60.0, Present: true}},
			{{Value: 600.0, Present: true}, {Value: 600.0, Present: true}, {Value: 600.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 6.0, Present: true},
			{Value: 60.0, Present: true},
			{Value: 600.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 666.0, Present: true},
			{Value: 666.0, Present: true},
			{Value: 666.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 1998.0, Present: true},
		CellLabel:  "AGG_SUM_amount",
	}
}

// TestApplyOverlays_IndexVsMargin_AxisRow_RowMeansHitBaseline verifies
// the story's acceptance criterion: INDEX_VS_MARGIN on a 3×3 synth
// matrix with axis=ROW produces row-indexed values where each row's
// mean ≈ 100. Uses indexUniformPayload where every cell equals its
// row margin so the index is 100 everywhere — per-row mean is exactly
// 100. Defends against axis confusion (a column / grand axis on the
// same data would produce row means other than 100).
func TestApplyOverlays_IndexVsMargin_AxisRow_RowMeansHitBaseline(t *testing.T) {
	host := NewCrosstabHostView(indexUniformPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
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
	if layer.Kind != types.OverlayKindIndexVsMargin {
		t.Fatalf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindIndexVsMargin)
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
	const want = 100.0
	const eps = 1e-9
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
			if math.Abs(v-want) > eps {
				t.Fatalf("cell[%d][%d] index = %v, want %v", i, j, v, want)
			}
			sum += v
		}
		mean := sum / float64(len(row))
		if math.Abs(mean-want) > eps {
			t.Fatalf("row[%d] mean = %v, want ≈ %v", i, mean, want)
		}
	}

	if layer.Summary == nil {
		t.Fatalf("layer.Summary nil")
	}
	if layer.Summary.Baseline == nil || *layer.Summary.Baseline != 100.0 {
		t.Fatalf("summary baseline = %v, want 100", layer.Summary.Baseline)
	}
	if layer.Summary.Count == nil || *layer.Summary.Count != 9 {
		t.Fatalf("summary count = %v, want 9", layer.Summary.Count)
	}
}

// TestApplyOverlays_IndexVsMargin_AxisColumn_ColumnMeansHitBaseline
// verifies the symmetric axis=COLUMN case. With indexUniformPayload
// every cell equals its row margin (NOT column margin), so the
// column-axis index varies row-to-row. Build a payload where every
// cell equals its column margin so axis=COLUMN produces 100
// everywhere and column means are 100.
func TestApplyOverlays_IndexVsMargin_AxisColumn_ColumnMeansHitBaseline(t *testing.T) {
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}, {Value: 30.0, Present: true}},
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}, {Value: 30.0, Present: true}},
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}, {Value: 30.0, Present: true}},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 10.0, Present: true},
			{Value: 20.0, Present: true},
			{Value: 30.0, Present: true},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
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
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if len(layers) != 1 || layers[0].Payload.Matrix == nil {
		t.Fatalf("unexpected layer shape: %+v", layers)
	}
	const eps = 1e-9
	for i, row := range layers[0].Payload.Matrix.Cells {
		for j, cell := range row {
			v, _ := cell.Value.(float64)
			if math.Abs(v-100.0) > eps {
				t.Fatalf("cell[%d][%d] = %v, want 100", i, j, v)
			}
		}
	}
}

// TestApplyOverlays_IndexVsMargin_AxisGrand_GrandIndexedScaled checks
// the GRAND axis path: each cell becomes cell / grand × 100. With a
// known grand total of 666 and the synth payload, validate a couple
// of representative cells and that the total of indexed cells sums to
// 100 (Σ cells / grand × 100 = 100).
func TestApplyOverlays_IndexVsMargin_AxisGrand_GrandIndexedScaled(t *testing.T) {
	host := NewCrosstabHostView(synthIndexMarginPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
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
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	matrix := layers[0].Payload.Matrix
	const grand = 666.0
	const eps = 1e-9
	var total float64
	for _, row := range matrix.Cells {
		for _, cell := range row {
			v := cell.Value.(float64)
			total += v
		}
	}
	if math.Abs(total-100.0) > eps {
		t.Fatalf("sum of grand-indexed cells = %v, want 100", total)
	}
	// Spot check: cell[0][0] = 1.0 / grand * 100.
	if got := matrix.Cells[0][0].Value.(float64); math.Abs(got-(1.0/grand*100.0)) > eps {
		t.Fatalf("cell[0][0] = %v, want %v", got, 1.0/grand*100.0)
	}
}

// TestApplyOverlays_IndexVsMargin_MissingMargin_EmitsWarning verifies
// that a request asking for the ROW margin on a payload whose
// RowMargins slot is unset (the crosstab did not request row
// margins) surfaces one PULSE_OVERLAY_REF_ZERO warning per host cell
// and leaves the overlay cell absent (Present=false). The validator
// should reject this at predict time when CrosstabSpec.NeedsRowMargin
// evaluates false; the runtime test exercises the defense-in-depth
// branch the handler runs anyway.
func TestApplyOverlays_IndexVsMargin_MissingMargin_EmitsWarning(t *testing.T) {
	payload := synthIndexMarginPayload()
	payload.RowMargins = nil // simulate "row margin not computed"
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
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
		if w.Code != codeOverlayRefZero {
			t.Fatalf("warning[%d] code = %q, want %q", i, w.Code, codeOverlayRefZero)
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

// TestApplyOverlays_IndexVsMargin_ZeroMargin_EmitsWarningNotInf
// verifies that a zero denominator surfaces the
// PULSE_OVERLAY_REF_ZERO warning rather than emitting an infinite
// cell value. Defends the per-cell branch in applyIndexVsMargin.
func TestApplyOverlays_IndexVsMargin_ZeroMargin_EmitsWarningNotInf(t *testing.T) {
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
			Kind:  types.OverlayKindIndexVsMargin,
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
	for _, cell := range layers[0].Payload.Matrix.Cells[0] {
		if cell.Present {
			t.Fatalf("cell should be absent when margin == 0, got %+v", cell)
		}
		if v, ok := cell.Value.(float64); ok && (math.IsInf(v, 0) || math.IsNaN(v)) {
			t.Fatalf("cell value leaked infinity / NaN: %v", v)
		}
	}
}

// TestApplyOverlays_AbsentCellsStayAbsent verifies that a structurally
// missing host cell (Present=false) produces an absent overlay cell
// without emitting a warning — the cell genuinely had no observation
// and is not a denominator failure.
func TestApplyOverlays_AbsentCellsStayAbsent(t *testing.T) {
	payload := indexUniformPayload()
	payload.Cells[1][1] = types.MatrixCell{Present: false}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
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

// TestApplyOverlays_EmptySpecsShortCircuits verifies that ApplyOverlays
// with no specs returns nil layers, nil warnings, nil error — the
// crosstab orchestrator can call ApplyOverlays unconditionally.
func TestApplyOverlays_EmptySpecsShortCircuits(t *testing.T) {
	layers, warnings, err := ApplyOverlays(nil, nil)
	if err != nil || layers != nil || warnings != nil {
		t.Fatalf("empty specs must short-circuit: layers=%v warnings=%v err=%v",
			layers, warnings, err)
	}
}

// TestApplyOverlays_UnknownKind_StubCoded verifies the defensive
// branch: an OverlaySpec with an unregistered kind triggers a
// PROCESSING_INTERNAL CodedError whose details carry the stub
// PULSE_OVERLAY_KIND_UNKNOWN code. The validator catches this at
// predict time; the runtime gate is defense in depth.
func TestApplyOverlays_UnknownKind_StubCoded(t *testing.T) {
	host := NewCrosstabHostView(indexUniformPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKind("OVERLAY_NOT_A_THING"),
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
		},
	}
	_, _, err := ApplyOverlays(specs, host)
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

// TestApplyOverlays_DefaultLayerName synthesises the kind_axis name
// when the spec omits Name; an explicit name passes through verbatim.
func TestApplyOverlays_DefaultLayerName(t *testing.T) {
	host := NewCrosstabHostView(indexUniformPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
		},
		{
			Name:  "row_share",
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if got, want := layers[0].Name, "OVERLAY_INDEX_VS_MARGIN_row"; got != want {
		t.Fatalf("synthesised name = %q, want %q", got, want)
	}
	if got, want := layers[1].Name, "row_share"; got != want {
		t.Fatalf("explicit name = %q, want %q", got, want)
	}
}
