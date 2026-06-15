package processing

import (
	stderrors "errors"
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Per-handler unit tests for the six COMPOSE-host overlay kinds
// landed by E7-S9: OVERLAY_INDEX_VS_REF, OVERLAY_DELTA_VS_REF,
// OVERLAY_PROP_Z_CELL, OVERLAY_T_CELL, OVERLAY_CHISQ_VS_REF, and
// OVERLAY_RANK. Each test pairs a synthetic 3×3 matrix on the
// reference + target side with hand-computed known answers.
//
// The fixtures use the canonical {"r0","r1","r2"} × {"c0","c1","c2"}
// axis-key shape to keep the lookup tables predictable and to mirror
// the chassis-side schema-match tests under
// processing/compose_overlay_schemamatch_test.go.

// makeMatrixFromValues constructs a *types.Response carrying a
// MatrixPayload with the given 3×3 cell values. NaN values render as
// absent (Present=false) so per-handler tests can exercise the
// absent-cell branch on demand.
func makeMatrixFromValues(values [3][3]float64) *types.Response {
	rowKeys := []types.AxisKey{{"r0"}, {"r1"}, {"r2"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}, {"c2"}}
	cells := make([][]types.MatrixCell, 3)
	for i := 0; i < 3; i++ {
		row := make([]types.MatrixCell, 3)
		for j := 0; j < 3; j++ {
			v := values[i][j]
			if math.IsNaN(v) {
				row[j] = types.MatrixCell{Present: false}
				continue
			}
			row[j] = types.MatrixCell{Value: v, Present: true}
		}
		cells[i] = row
	}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      rowKeys,
				ColumnKeys:   colKeys,
				Cells:        cells,
			},
		},
	}
}

// makeMatrixWithRowMargins is the PROP_Z fixture builder — same shape
// as makeMatrixFromValues but adds explicit per-row margins so the
// per-cell two-proportion z-test has sample sizes to read.
func makeMatrixWithRowMargins(values [3][3]float64, rowMargins [3]float64) *types.Response {
	resp := makeMatrixFromValues(values)
	mx := resp.Crosstab.Matrix
	mx.RowMargins = make([]types.MatrixCell, 3)
	for i, m := range rowMargins {
		mx.RowMargins[i] = types.MatrixCell{Value: m, Present: true}
	}
	return resp
}

// approxEqual asserts two floats are within tol of each other.
func approxEqual(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) && math.IsNaN(want) {
		return
	}
	if math.IsNaN(got) || math.IsNaN(want) {
		t.Errorf("%s: got %v, want %v", name, got, want)
		return
	}
	if math.Abs(got-want) > tol {
		t.Errorf("%s: got %v, want %v (tolerance %v)", name, got, want, tol)
	}
}

// cellAt returns the per-cell value from a MATRIX-shape OverlayLayer.
// Fails the test when the layer is not MATRIX-shape OR when the cell
// is absent.
func cellAt(t *testing.T, layer types.OverlayLayer, i, j int) float64 {
	t.Helper()
	if layer.Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layer.Payload.Shape = %q, want %q", layer.Payload.Shape, types.OverlayShapeMatrix)
	}
	mx := layer.Payload.Matrix
	if mx == nil {
		t.Fatal("layer.Payload.Matrix is nil")
	}
	if i >= len(mx.Cells) || j >= len(mx.Cells[i]) {
		t.Fatalf("cell (%d, %d) out of range", i, j)
	}
	cell := mx.Cells[i][j]
	if !cell.Present {
		t.Fatalf("cell (%d, %d) absent", i, j)
	}
	v, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("cell (%d, %d) value not float64: %T", i, j, cell.Value)
	}
	return v
}

// requireRefZeroWarning asserts the warnings slice carries at least one
// PULSE_OVERLAY_REF_ZERO entry.
func requireRefZeroWarning(t *testing.T, warnings []OverlayWarning) {
	t.Helper()
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			return
		}
	}
	t.Errorf("expected PULSE_OVERLAY_REF_ZERO warning, got %d warnings: %+v", len(warnings), warnings)
}

// composeSpecMatrixRef wires a minimal ComposeOverlaySpec for the
// MATRIX-host Compose kinds the per-handler tests exercise.
func composeSpecMatrixRef(kind types.OverlayKind, params map[string]any) types.ComposeOverlaySpec {
	return types.ComposeOverlaySpec{
		Name:      "test",
		Kind:      kind,
		Scope:     types.OverlayScopeCell,
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Params:    params,
	}
}

// TestApplyIndexVsRef_KnownAnswer exercises the per-cell ratio
// (target / ref) * scale against hand-computed indices. Scale
// defaults to 100.
func TestApplyIndexVsRef_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	target := makeMatrixFromValues([3][3]float64{
		{15, 30, 45},
		{60, 75, 90},
		{105, 120, 135},
	})
	spec := composeSpecMatrixRef(types.OverlayKindIndexVsRef, nil)
	layer, warnings, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// Every cell is target / ref * 100 = 1.5 * 100 = 150.
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "cell", cellAt(t, layer, i, j), 150.0, 1e-9)
		}
	}
	if layer.Kind != types.OverlayKindIndexVsRef {
		t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindIndexVsRef)
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 100.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 100", layer.Summary)
	}
}

// TestApplyIndexVsRef_ScaleOne pins the Params["scale"] = 1 raw-ratio
// authoring shape — every cell should equal target / ref directly
// (no ×100 scaling).
func TestApplyIndexVsRef_ScaleOne(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 2, 4},
		{1, 1, 1},
		{1, 1, 1},
	})
	target := makeMatrixFromValues([3][3]float64{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	spec := composeSpecMatrixRef(types.OverlayKindIndexVsRef, map[string]any{"scale": 1.0})
	layer, _, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef: %v", err)
	}
	approxEqual(t, "cell (0,0)", cellAt(t, layer, 0, 0), 1.0, 1e-9)
	approxEqual(t, "cell (0,1)", cellAt(t, layer, 0, 1), 0.5, 1e-9)
	approxEqual(t, "cell (0,2)", cellAt(t, layer, 0, 2), 0.25, 1e-9)
	if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 1.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 1 (scale=1)", layer.Summary)
	}
}

// TestApplyIndexVsRef_ZeroRefEmitsWarningAndNaN asserts that a zero
// reference cell emits PULSE_OVERLAY_REF_ZERO and substitutes NaN on
// the affected cell. Other cells stay finite.
func TestApplyIndexVsRef_ZeroRefEmitsWarningAndNaN(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{0, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	target := makeMatrixFromValues([3][3]float64{
		{5, 5, 5},
		{5, 5, 5},
		{5, 5, 5},
	})
	spec := composeSpecMatrixRef(types.OverlayKindIndexVsRef, nil)
	layer, warnings, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef: %v", err)
	}
	requireRefZeroWarning(t, warnings)
	// Cell (0,0) ⇒ NaN because ref was zero.
	mx := layer.Payload.Matrix
	if !math.IsNaN(mx.Cells[0][0].Value.(float64)) {
		t.Errorf("cell (0,0) = %v, want NaN (zero ref)", mx.Cells[0][0].Value)
	}
	// Other cells ⇒ 500.
	approxEqual(t, "cell (0,1)", cellAt(t, layer, 0, 1), 500.0, 1e-9)
}

// TestApplyDeltaVsRef_KnownAnswer pins the per-cell subtraction.
func TestApplyDeltaVsRef_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{2, 4, 6},
		{8, 10, 12},
		{14, 16, 18},
	})
	spec := composeSpecMatrixRef(types.OverlayKindDeltaVsRef, nil)
	layer, warnings, err := applyDeltaVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyDeltaVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	want := [3][3]float64{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "cell", cellAt(t, layer, i, j), want[i][j], 1e-9)
		}
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 0.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 0", layer.Summary)
	}
}

// TestApplyDeltaVsRef_ZeroRefNoNaN asserts that a zero reference cell
// produces a finite delta (target - 0 = target) and does NOT emit
// PULSE_OVERLAY_REF_ZERO — subtraction is well-defined for any finite
// reference value.
func TestApplyDeltaVsRef_ZeroRefNoNaN(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{0, 1, 2},
		{3, 4, 5},
		{6, 7, 8},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	spec := composeSpecMatrixRef(types.OverlayKindDeltaVsRef, nil)
	layer, warnings, err := applyDeltaVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyDeltaVsRef: %v", err)
	}
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("PULSE_OVERLAY_REF_ZERO emitted against zero ref; DELTA must not emit this code")
		}
	}
	approxEqual(t, "cell (0,0)", cellAt(t, layer, 0, 0), 10.0, 1e-9)
	approxEqual(t, "cell (0,1)", cellAt(t, layer, 0, 1), 9.0, 1e-9)
}

// TestApplyPropZCell_KnownAnswer pins a hand-computed two-proportion
// p-value for a known (success, n) pair. Cell (0,0): 50/100 target vs
// 60/100 ref — should produce a p-value ≈ 0.157 (z ≈ -1.4142).
func TestApplyPropZCell_KnownAnswer(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{60, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	target := makeMatrixWithRowMargins(
		[3][3]float64{
			{50, 50, 50},
			{50, 50, 50},
			{50, 50, 50},
		},
		[3]float64{100, 100, 100},
	)
	spec := composeSpecMatrixRef(types.OverlayKindPropZCell, nil)
	layer, _, err := applyPropZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyPropZCell: %v", err)
	}
	// Cell (0,0): 50/100 target vs 60/100 ref.
	// pa = 0.5, pb = 0.6, pooled = 110/200 = 0.55
	// se = sqrt(0.55 * 0.45 * (1/100 + 1/100)) = sqrt(0.2475 * 0.02)
	//    = sqrt(0.00495) ≈ 0.070356
	// z = (0.5 - 0.6) / 0.070356 ≈ -1.4213
	// p_value = 2 * (1 - Φ(1.4213)) ≈ 0.1552
	got := cellAt(t, layer, 0, 0)
	approxEqual(t, "p-value (0,0)", got, 0.1552, 0.005)
	// Cell (0,1): 50/100 target vs 50/100 ref ⇒ p-value = 1.0 (z = 0).
	approxEqual(t, "p-value (0,1)", cellAt(t, layer, 0, 1), 1.0, 1e-6)
}

// TestApplyTCell_KnownAnswer pins a Welch t-test p-value for a known
// (mean, variance, n) triple. Cell (0,0): target_mean=10, ref_mean=9,
// var=1, n=2 ⇒ se = sqrt(0.5+0.5)=1, t=1, df=2 ⇒ p ≈ 0.4226.
func TestApplyTCell_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, _, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	// se = sqrt(1/2 + 1/2) = 1; t = (10 - 9) / 1 = 1
	// df = (1)² / (0.25 / 1 + 0.25 / 1) = 1 / 0.5 = 2
	// p_value (two-sided, df=2) for t=1 is ≈ 0.4226
	got := cellAt(t, layer, 0, 0)
	approxEqual(t, "p-value", got, 0.4226, 0.01)
	if layer.Kind != types.OverlayKindTCell {
		t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindTCell)
	}
}

// TestApplyChiSqVsRef_KnownAnswer pins a whole-matrix χ² statistic
// + p-value for hand-computed expected counts.
//
// Target = [[10, 20, 30], [40, 50, 60], [70, 80, 90]] (sum 450)
// Ref    = [[20, 20, 20], [20, 20, 20], [20, 20, 20]] (sum 180)
// scale = 450 / 180 = 2.5
// expected = [[50, 50, 50], [50, 50, 50], [50, 50, 50]]
// chisq    = Σ (target - 50)² / 50
//
//	(10-50)²/50 = 32, (20-50)²/50 = 18, (30-50)²/50 = 8
//	(40-50)²/50 = 2,  (50-50)²/50 = 0,  (60-50)²/50 = 2
//	(70-50)²/50 = 8,  (80-50)²/50 = 18, (90-50)²/50 = 32
//
// total = 120
// df = 9 - 1 = 8
func TestApplyChiSqVsRef_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{20, 20, 20},
		{20, 20, 20},
		{20, 20, 20},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	spec := types.ComposeOverlaySpec{
		Name:      "chisq_vs_ref",
		Kind:      types.OverlayKindChiSqVsRef,
		Scope:     types.OverlayScopeMatrix,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	layer, _, err := applyChiSqVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyChiSqVsRef: %v", err)
	}
	if layer.Payload.Shape != types.OverlayShapeScalar {
		t.Fatalf("layer.Payload.Shape = %q, want SCALAR", layer.Payload.Shape)
	}
	if layer.Summary == nil || layer.Summary.Statistic == nil {
		t.Fatal("layer.Summary.Statistic = nil")
	}
	approxEqual(t, "chisq statistic", *layer.Summary.Statistic, 120.0, 1e-6)
	if layer.Summary.Parameters == nil {
		t.Fatal("layer.Summary.Parameters = nil")
	}
	approxEqual(t, "df", layer.Summary.Parameters["df"], 8.0, 1e-9)
	if layer.Summary.PValue == nil {
		t.Fatal("layer.Summary.PValue = nil")
	}
	// p-value for χ²=120, df=8 is effectively 0 (chiSquareSurvival ≈ 0).
	if *layer.Summary.PValue > 1e-15 {
		t.Errorf("p-value = %v, want ≈ 0", *layer.Summary.PValue)
	}
}

// TestApplyChiSqVsRef_EmptyTargetEmitsRefZero asserts the degenerate
// path: empty target_N ⇒ NaN + PULSE_OVERLAY_REF_ZERO.
func TestApplyChiSqVsRef_EmptyTargetEmitsRefZero(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	target := makeMatrixFromValues([3][3]float64{
		{0, 0, 0},
		{0, 0, 0},
		{0, 0, 0},
	})
	spec := types.ComposeOverlaySpec{
		Kind:      types.OverlayKindChiSqVsRef,
		Reference: "baseline",
		Targets:   []string{"treatment"},
	}
	layer, warnings, err := applyChiSqVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyChiSqVsRef: %v", err)
	}
	requireRefZeroWarning(t, warnings)
	if layer.Summary.Statistic == nil || !math.IsNaN(*layer.Summary.Statistic) {
		t.Errorf("statistic = %v, want NaN", layer.Summary.Statistic)
	}
}

// TestApplyRank_PopulationMatrix_KnownAnswer pins matrix-wide
// average-tie ranks. Target = [[10, 20, 30], [40, 50, 60], [70, 80,
// 90]]. Descending sort ⇒ 90 is rank 1; 10 is rank 9.
func TestApplyRank_PopulationMatrix_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	spec := composeSpecMatrixRef(types.OverlayKindRank, map[string]any{"population": "matrix"})
	layer, _, err := applyRank(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyRank: %v", err)
	}
	// 90 is rank 1, 80 is rank 2, ..., 10 is rank 9.
	want := [3][3]float64{
		{9, 8, 7},
		{6, 5, 4},
		{3, 2, 1},
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "rank", cellAt(t, layer, i, j), want[i][j], 1e-9)
		}
	}
}

// TestApplyRank_PopulationRow_KnownAnswer pins per-row ranks. Each
// row is ranked independently across its 3 columns.
func TestApplyRank_PopulationRow_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 30, 20},
		{50, 40, 60},
		{90, 70, 80},
	})
	spec := composeSpecMatrixRef(types.OverlayKindRank, map[string]any{"population": "row"})
	layer, _, err := applyRank(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyRank: %v", err)
	}
	// Row 0: 30 is rank 1, 20 is rank 2, 10 is rank 3.
	// Row 1: 60 is rank 1, 50 is rank 2, 40 is rank 3.
	// Row 2: 90 is rank 1, 80 is rank 2, 70 is rank 3.
	want := [3][3]float64{
		{3, 1, 2},
		{2, 3, 1},
		{1, 3, 2},
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "rank", cellAt(t, layer, i, j), want[i][j], 1e-9)
		}
	}
}

// TestApplyRank_PopulationColumn_KnownAnswer pins per-column ranks.
func TestApplyRank_PopulationColumn_KnownAnswer(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 50, 90},
		{30, 40, 70},
		{20, 60, 80},
	})
	spec := composeSpecMatrixRef(types.OverlayKindRank, map[string]any{"population": "column"})
	layer, _, err := applyRank(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyRank: %v", err)
	}
	// Col 0: 30 rank 1, 20 rank 2, 10 rank 3.
	// Col 1: 60 rank 1, 50 rank 2, 40 rank 3.
	// Col 2: 90 rank 1, 80 rank 2, 70 rank 3.
	want := [3][3]float64{
		{3, 2, 1},
		{1, 3, 3},
		{2, 1, 2},
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "rank", cellAt(t, layer, i, j), want[i][j], 1e-9)
		}
	}
}

// TestApplyRank_PopulationDefaultsToMatrix asserts that missing
// Params["population"] defaults to "matrix" mode (no warning).
func TestApplyRank_PopulationDefaultsToMatrix(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	})
	// No Params slot — should default to matrix mode.
	spec := composeSpecMatrixRef(types.OverlayKindRank, nil)
	layer, warnings, err := applyRank(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyRank: %v", err)
	}
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_PARAM_MISSING) {
			t.Errorf("PARAM_MISSING warning fired against default 'matrix' mode; got %+v", w)
		}
	}
	approxEqual(t, "rank (0,0)", cellAt(t, layer, 0, 0), 9.0, 1e-9)
}

// TestApplyRank_TiesAverageRank verifies the canonical "average" tie-
// breaking convention: equal values share the average of their would-
// be ranks. [10, 20, 20, 30] ⇒ [4, 2.5, 2.5, 1].
func TestApplyRank_TiesAverageRank(t *testing.T) {
	ranks := computeAverageRanks([]float64{10, 20, 20, 30})
	want := []float64{4, 2.5, 2.5, 1}
	for i, got := range ranks {
		approxEqual(t, "rank", got, want[i], 1e-9)
	}
}

// TestApplyComposeOverlays_SixKindsDispatch is the dispatch-level
// integration test asserting every E7-S9 kind routes through its
// registered handler (NOT the chassis stub) when called via
// ApplyComposeOverlays. The fixture exercises one spec per kind
// against the same ref / target matrix.
func TestApplyComposeOverlays_SixKindsDispatch(t *testing.T) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{
			{20, 25, 25},
			{20, 20, 20},
			{20, 20, 20},
		},
		[3]float64{100, 100, 100},
	)
	target := makeMatrixWithRowMargins(
		[3][3]float64{
			{10, 20, 30},
			{40, 50, 60},
			{30, 40, 50},
		},
		[3]float64{100, 100, 100},
	)
	specs := []types.ComposeOverlaySpec{
		{Kind: types.OverlayKindIndexVsRef, Scope: types.OverlayScopeCell, Reference: "baseline", Targets: []string{"treatment"}},
		{Kind: types.OverlayKindDeltaVsRef, Scope: types.OverlayScopeCell, Reference: "baseline", Targets: []string{"treatment"}},
		{Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell, Reference: "baseline", Targets: []string{"treatment"}},
		{Kind: types.OverlayKindTCell, Scope: types.OverlayScopeCell, Reference: "baseline", Targets: []string{"treatment"}},
		{Kind: types.OverlayKindChiSqVsRef, Scope: types.OverlayScopeMatrix, Reference: "baseline", Targets: []string{"treatment"}},
		{Kind: types.OverlayKindRank, Scope: types.OverlayScopeCell, Reference: "baseline", Targets: []string{"treatment"}},
	}
	responses := []*types.Response{ref, target}
	labels := []string{"baseline", "treatment"}
	layers, _, err := ApplyComposeOverlays(specs, responses, labels)
	if err != nil {
		t.Fatalf("ApplyComposeOverlays: %v", err)
	}
	if len(layers) != len(specs) {
		t.Fatalf("got %d layers, want %d", len(layers), len(specs))
	}
	for i, layer := range layers {
		if layer.Kind != specs[i].Kind {
			t.Errorf("layer[%d].Kind = %q, want %q", i, layer.Kind, specs[i].Kind)
		}
		// Stub layers carry an empty payload by construction; real
		// handlers populate either Matrix or Scalar. CHISQ_VS_REF is
		// SCALAR; the rest are MATRIX.
		switch specs[i].Kind {
		case types.OverlayKindChiSqVsRef:
			if layer.Payload.Shape != types.OverlayShapeScalar {
				t.Errorf("layer[%d] CHISQ_VS_REF shape = %q, want SCALAR", i, layer.Payload.Shape)
			}
			if layer.Summary == nil || layer.Summary.Statistic == nil {
				t.Errorf("layer[%d] CHISQ_VS_REF summary statistic missing", i)
			}
		default:
			if layer.Payload.Shape != types.OverlayShapeMatrix {
				t.Errorf("layer[%d] %q shape = %q, want MATRIX", i, specs[i].Kind, layer.Payload.Shape)
			}
			if layer.Payload.Matrix == nil {
				t.Errorf("layer[%d] %q payload matrix nil", i, specs[i].Kind)
			}
		}
	}
}

// makeMatrixFromTriples constructs a *types.Response carrying a
// MatrixPayload whose cells are WelfordTriple rich payloads
// (mirrors AGG_WELFORD's crosstab output). Used by the triple-aware
// arms of the T_CELL / Z_CELL handlers (E1-S9, E1-S17).
//
// E3-S7 migration: also populates Response.Components.Crosstab.
// CellComponents with the matching `{n, mean, variance}` map per
// cell so the migrated handlers can consume the universal Components
// surface. The legacy WelfordTriple in MatrixCell.Value stays —
// E3-S8 removes the writer side.
func makeMatrixFromTriples(triples [3][3]WelfordTriple) *types.Response {
	rowKeys := []types.AxisKey{{"r0"}, {"r1"}, {"r2"}}
	colKeys := []types.AxisKey{{"c0"}, {"c1"}, {"c2"}}
	cells := make([][]types.MatrixCell, 3)
	cellComponents := make([][]map[string]any, 3)
	for i := 0; i < 3; i++ {
		row := make([]types.MatrixCell, 3)
		compRow := make([]map[string]any, 3)
		for j := 0; j < 3; j++ {
			row[j] = types.MatrixCell{Value: triples[i][j], Present: true}
			compRow[j] = map[string]any{
				"n":        int(triples[i][j].N),
				"n_null":   0,
				"mean":     triples[i][j].Mean,
				"variance": triples[i][j].Variance,
			}
		}
		cells[i] = row
		cellComponents[i] = compRow
	}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      rowKeys,
				ColumnKeys:   colKeys,
				Cells:        cells,
			},
		},
		Components: &types.ResponseComponents{
			Crosstab: &types.CrosstabComponents{
				CellComponents: cellComponents,
			},
		},
	}
}

// TestApplyTCell_TripleKnownAnswer pins the Welch p-value for the
// triple-bearing path. Both sides carry WelfordTriple cells —
// (Mean, Variance, N) is read off the triple directly and the Params
// defaults are bypassed.
//
//	target = {mean: 10, var: 4, n: 10}
//	ref    = {mean:  9, var: 4, n: 10}
//	se     = sqrt(4/10 + 4/10) ≈ 0.8944
//	t      = 1 / 0.8944 ≈ 1.118
//	df_ws  ≈ 18.0 (equal var, equal n closes to 2*(n-1))
//	p      ≈ 0.2783
func TestApplyTCell_TripleKnownAnswer(t *testing.T) {
	triple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeMatrixFromTriples([3][3]WelfordTriple{
		{triple, triple, triple},
		{triple, triple, triple},
		{triple, triple, triple},
	})
	ref := makeMatrixFromTriples([3][3]WelfordTriple{
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
	})
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, warnings, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// Cross-check against welchTTest directly (the same helper backing
	// the matrix arm) so the test is byte-equivalent to the
	// implementation under test.
	want, ok := welchTTest(10.0, 4.0, 10.0, 9.0, 4.0, 10.0)
	if !ok {
		t.Fatal("welchTTest reference computation failed")
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "cell (triple)", cellAt(t, layer, i, j), want, 1e-9)
		}
	}
}

// TestApplyTCell_TripleBypassesParamsDefaults asserts the precedence
// rule: cells carrying a WelfordTriple ignore the Params defaults.
// Params here, if honoured, would shift p toward 1.0; the triple path
// must produce the same answer as TestApplyTCell_TripleKnownAnswer.
func TestApplyTCell_TripleBypassesParamsDefaults(t *testing.T) {
	triple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeMatrixFromTriples([3][3]WelfordTriple{
		{triple, triple, triple},
		{triple, triple, triple},
		{triple, triple, triple},
	})
	ref := makeMatrixFromTriples([3][3]WelfordTriple{
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
		{refTriple, refTriple, refTriple},
	})
	spec := composeSpecMatrixRef(types.OverlayKindTCell, map[string]any{
		"variance_target":    100.0,
		"variance_ref":       100.0,
		"sample_size_target": 1000.0,
		"sample_size_ref":    1000.0,
	})
	layer, _, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	want, _ := welchTTest(10.0, 4.0, 10.0, 9.0, 4.0, 10.0)
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "cell (triple bypasses Params)", cellAt(t, layer, i, j), want, 1e-9)
		}
	}
}

// TestApplyTCell_TripleDegenerateInsufficientN exercises the n<2 arm
// of the triple-bearing path. Every cell should surface NaN +
// PULSE_OVERLAY_REF_ZERO.
func TestApplyTCell_TripleDegenerateInsufficientN(t *testing.T) {
	tri := WelfordTriple{Mean: 10, Variance: 4, N: 1}
	refTri := WelfordTriple{Mean: 9, Variance: 4, N: 1}
	target := makeMatrixFromTriples([3][3]WelfordTriple{
		{tri, tri, tri},
		{tri, tri, tri},
		{tri, tri, tri},
	})
	ref := makeMatrixFromTriples([3][3]WelfordTriple{
		{refTri, refTri, refTri},
		{refTri, refTri, refTri},
		{refTri, refTri, refTri},
	})
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, warnings, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	if len(warnings) != 9 {
		t.Errorf("len(warnings) = %d, want 9 (every cell N<2)", len(warnings))
	}
	for _, w := range warnings {
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("warning.Code = %q, want %q", w.Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
		}
	}
	if !math.IsNaN(cellAt(t, layer, 0, 0)) {
		t.Errorf("cell (0,0) = %v, want NaN", cellAt(t, layer, 0, 0))
	}
}

// TestApplyTCell_TripleZeroVarianceBothSides exercises the
// degenerate-variance arm of the triple-bearing path. Both sides
// carry Variance=0 ⇒ welchTTest returns NaN/false ⇒ every cell
// surfaces NaN + PULSE_OVERLAY_REF_ZERO.
func TestApplyTCell_TripleZeroVarianceBothSides(t *testing.T) {
	tri := WelfordTriple{Mean: 10, Variance: 0, N: 10}
	refTri := WelfordTriple{Mean: 9, Variance: 0, N: 10}
	target := makeMatrixFromTriples([3][3]WelfordTriple{
		{tri, tri, tri},
		{tri, tri, tri},
		{tri, tri, tri},
	})
	ref := makeMatrixFromTriples([3][3]WelfordTriple{
		{refTri, refTri, refTri},
		{refTri, refTri, refTri},
		{refTri, refTri, refTri},
	})
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, warnings, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	if len(warnings) != 9 {
		t.Errorf("len(warnings) = %d, want 9 (every cell zero-SE)", len(warnings))
	}
	if !math.IsNaN(cellAt(t, layer, 0, 0)) {
		t.Errorf("cell (0,0) = %v, want NaN", cellAt(t, layer, 0, 0))
	}
}

// TestApplyTCell_MixedShapeRejected asserts the defensive
// PULSE_OVERLAY_SCHEMA_DIVERGENT arm. The compose schema-match gate
// (E1-S8) should reject mixed-shape pairs BEFORE dispatch, but the
// handler also defensively rejects a target triple paired with a
// scalar reference at runtime.
func TestApplyTCell_MixedShapeRejected(t *testing.T) {
	tri := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	target := makeMatrixFromTriples([3][3]WelfordTriple{
		{tri, tri, tri},
		{tri, tri, tri},
		{tri, tri, tri},
	})
	ref := makeMatrixFromValues([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	_, _, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err == nil {
		t.Fatal("applyTCell: want error for mixed triple/scalar pair, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT) {
		t.Errorf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT)
	}
}

// TestApplyTCell_ScalarFallbackByteIdentical asserts the additive
// contract: a request paired with scalar-only cells produces byte-
// identical output to the pre-S9 Params-default path. We use the same
// 3×3 fixture as TestApplyTCell_KnownAnswer and compare cell-for-cell.
func TestApplyTCell_ScalarFallbackByteIdentical(t *testing.T) {
	ref := makeMatrixFromValues([3][3]float64{
		{9, 9, 9},
		{9, 9, 9},
		{9, 9, 9},
	})
	target := makeMatrixFromValues([3][3]float64{
		{10, 10, 10},
		{10, 10, 10},
		{10, 10, 10},
	})
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, warnings, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// The hand-computed reference: se=1, t=1, df=2 ⇒ p ≈ 0.4226.
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			approxEqual(t, "cell (scalar fallback)", cellAt(t, layer, i, j), 0.4226, 0.01)
		}
	}
}

// TestApplyIndexVsRef_NonMatrixSlotErrors asserts the handler returns
// a coded PULSE_OVERLAY_SLOT_NOT_CROSSTAB error when reference or
// target carries a non-MATRIX shape. Defense in depth — the chassis
// schema-match gate (E7-S7 + kindRequiresMatrix flip) is the primary
// surface that catches this case BEFORE dispatch, but the handler
// guards against direct callers.
func TestApplyIndexVsRef_NonMatrixSlotErrors(t *testing.T) {
	ref := &types.Response{Data: []map[string]any{
		{"region": "us", "sum": 1.0},
	}}
	target := makeMatrixFromValues([3][3]float64{
		{1, 1, 1},
		{1, 1, 1},
		{1, 1, 1},
	})
	spec := composeSpecMatrixRef(types.OverlayKindIndexVsRef, nil)
	_, _, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err == nil {
		t.Fatal("applyIndexVsRef: want error against non-MATRIX ref, got nil")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB) {
		t.Fatalf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB)
	}
}
