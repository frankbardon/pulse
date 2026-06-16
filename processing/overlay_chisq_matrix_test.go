package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// chiSqMatrixPayloadKnown returns a synthetic 2 × 2 host MatrixPayload
// whose χ² statistic is computable in closed form. Standard textbook
// 2×2 contingency:
//
//	     c0   c1   | row_margin
//	r0   10   20   |   30
//	r1   20   10   |   30
//	col  30   30   |  grand = 60
//
// Expected counts (row_margin × col_margin / grand):
//
//	expected = [[15, 15], [15, 15]]
//
// Per-cell (obs - exp)² / exp:
//
//	(10-15)² / 15 = 25/15 ≈ 1.6667
//
// Four cells contribute identically, so χ² = 4 × 25/15 = 100/15 ≈
// 6.6667. df = (2-1) × (2-1) = 1. Survival at χ² ≈ 6.6667 with df=1 is
// roughly 0.00982 (p ≈ 0.01). Lookups via the standard χ² CDF table or
// any statistical reference confirm the value within 1e-4.
func chiSqMatrixPayloadKnown() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}, {Value: 20.0, Present: true}},
			{{Value: 20.0, Present: true}, {Value: 10.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 30.0, Present: true},
			{Value: 30.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 30.0, Present: true},
			{Value: 30.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 60.0, Present: true},
		CellLabel:  "AGG_COUNT_id",
	}
}

// TestOverlay_ChiSqMatrix_StatMatchesGonum verifies the closed-form
// χ² statistic on a textbook 2 × 2 contingency. The handler must
// surface chisq ≈ 6.6667, df = 1, and p ≈ 0.00982 within 1e-4. Also
// asserts the SCALAR payload + Summary plumbing — Statistic, PValue,
// and Parameters{"df"} populated; Matrix / Series slots nil.
func TestOverlay_ChiSqMatrix_StatMatchesGonum(t *testing.T) {
	host := NewCrosstabHostView(chiSqMatrixPayloadKnown())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
			Ref:   types.OverlayRef{}, // implicit-margin
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	// Expected counts in this contingency are 15 each — no low-expected
	// warnings should fire (all ≥ 5).
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings (all expected cells ≥ 5); got %d: %+v",
			len(warnings), warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	layer := layers[0]

	// Shape contract: SCALAR payload, Matrix / Series nil.
	if layer.Payload.Shape != types.OverlayShapeScalar {
		t.Fatalf("Payload.Shape = %q, want %q", layer.Payload.Shape, types.OverlayShapeScalar)
	}
	if layer.Payload.Matrix != nil {
		t.Fatalf("Payload.Matrix = %+v, want nil for SCALAR shape", layer.Payload.Matrix)
	}
	if layer.Payload.Series != nil {
		t.Fatalf("Payload.Series = %+v, want nil for SCALAR shape", layer.Payload.Series)
	}
	if layer.Payload.Scalar == nil {
		t.Fatalf("Payload.Scalar nil; want populated chi-square statistic")
	}

	const wantStat = 100.0 / 15.0 // ≈ 6.6667
	const eps = 1e-4
	if got := *layer.Payload.Scalar; math.Abs(got-wantStat) > eps {
		t.Fatalf("Payload.Scalar = %v, want %v ± %v", got, wantStat, eps)
	}

	// Summary surface: Statistic mirrors Scalar; PValue ≈ 0.00982; df = 1.
	if layer.Summary == nil {
		t.Fatalf("layer.Summary nil; want populated Statistic/PValue/Parameters")
	}
	if layer.Summary.Statistic == nil ||
		math.Abs(*layer.Summary.Statistic-wantStat) > eps {
		t.Fatalf("Summary.Statistic = %v, want %v", layer.Summary.Statistic, wantStat)
	}
	const wantP = 0.00982 // χ²=6.6667, df=1 — standard reference
	if layer.Summary.PValue == nil ||
		math.Abs(*layer.Summary.PValue-wantP) > 5e-4 {
		t.Fatalf("Summary.PValue = %v, want %v ± 5e-4", layer.Summary.PValue, wantP)
	}
	if layer.Summary.Parameters == nil {
		t.Fatalf("Summary.Parameters nil; want {\"df\": 1}")
	}
	if df, ok := layer.Summary.Parameters["df"]; !ok || df != 1.0 {
		t.Fatalf("Summary.Parameters[\"df\"] = %v (ok=%v), want 1", df, ok)
	}

	// Layer metadata: kind / scope / synthesised default name.
	if layer.Kind != types.OverlayKindChiSqMatrix {
		t.Fatalf("Kind = %q, want %q", layer.Kind, types.OverlayKindChiSqMatrix)
	}
	if layer.Scope != types.OverlayScopeMatrix {
		t.Fatalf("Scope = %q, want %q", layer.Scope, types.OverlayScopeMatrix)
	}
	if got, want := layer.Name, "chisq_matrix"; got != want {
		t.Fatalf("default Name = %q, want %q", got, want)
	}
}

func TestOverlay_ChiSqMatrix_ExpectedLowEmitsWarn(t *testing.T) {
	// 2×2 with small counts. Row margins = 5 each, col margins = 5 each,
	// grand = 10. Expected cells = 5 × 5 / 10 = 2.5 (all below 5).
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{{Value: 2.0, Present: true}, {Value: 3.0, Present: true}},
			{{Value: 3.0, Present: true}, {Value: 2.0, Present: true}},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 PULSE_OVERLAY_EXPECTED_LOW warning, got %d: %+v",
			len(warnings), warnings)
	}
	if got, want := warnings[0].Code, "PULSE_OVERLAY_EXPECTED_LOW"; got != want {
		t.Fatalf("warning Code = %q, want %q (stub code)", got, want)
	}
	// Details should carry the count of low-expected cells (all 4 in
	// this contingency since every expected = 2.5).
	low, ok := warnings[0].Details["low_expected_cells"].(int)
	if !ok || low != 4 {
		t.Fatalf("Details[low_expected_cells] = %v (ok=%v), want 4", low, ok)
	}
	if expMin, ok := warnings[0].Details["expected_min"].(float64); !ok || expMin != 2.5 {
		t.Fatalf("Details[expected_min] = %v (ok=%v), want 2.5", expMin, ok)
	}

	// The statistic is still emitted — the warning is advisory, not
	// fatal. The handler must surface a finite chi-square value alongside
	// the warning so callers can decide whether to trust it.
	if layers[0].Payload.Scalar == nil {
		t.Fatalf("Payload.Scalar nil; statistic should still be emitted alongside the warning")
	}
	if v := *layers[0].Payload.Scalar; math.IsNaN(v) || math.IsInf(v, 0) {
		t.Fatalf("Payload.Scalar = %v, want finite chi-square value", v)
	}
}

// TestOverlay_ChiSqMatrix_AbsentCellsHandled verifies the documented
// absent-cell policy: a structurally absent host cell (Present=false)
// is treated as observed count 0. The matrix shape stays rectangular,
// the χ² recurrence runs over the full row × col grid, and the
// statistic uses the absent-as-zero observation without inventing a
// count.
//
// Hand check (2 × 2 with cells.[0][1] absent):
//
//	cells   = [[10, 0], [10, 10]]   (absent → 0)
//	row_tot = [10, 20]
//	col_tot = [20, 10]
//	grand   = 30
//	expected = row_tot × col_tot / grand
//	         = [[10*20/30, 10*10/30], [20*20/30, 20*10/30]]
//	         = [[6.6667, 3.3333], [13.3333, 6.6667]]
//	chi²    = Σ (obs - exp)² / exp
//	        = (10-6.6667)²/6.6667 + (0-3.3333)²/3.3333
//	        + (10-13.3333)²/13.3333 + (10-6.6667)²/6.6667
//	        ≈ 1.6667 + 3.3333 + 0.8333 + 1.6667
//	        ≈ 7.5
//
// Since at least one expected cell (3.3333) is below 5, the low-
// expected warning must also fire. The handler must NOT crash on an
// absent cell.
func TestOverlay_ChiSqMatrix_AbsentCellsHandled(t *testing.T) {
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}, {Present: false}},
			{{Value: 10.0, Present: true}, {Value: 10.0, Present: true}},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	// At least one low-expected warning must fire because expected[0][1]
	// ≈ 3.3333 < 5.
	if len(warnings) != 1 {
		t.Fatalf("expected 1 PULSE_OVERLAY_EXPECTED_LOW warning (expected[0][1] ≈ 3.33 < 5); got %d: %+v",
			len(warnings), warnings)
	}
	if got := warnings[0].Code; got != "PULSE_OVERLAY_EXPECTED_LOW" {
		t.Fatalf("warning Code = %q, want PULSE_OVERLAY_EXPECTED_LOW", got)
	}

	// χ² ≈ 7.5 within 5e-3 tolerance.
	if layers[0].Payload.Scalar == nil {
		t.Fatalf("Payload.Scalar nil")
	}
	const wantStat = 7.5
	const eps = 5e-3
	if got := *layers[0].Payload.Scalar; math.Abs(got-wantStat) > eps {
		t.Fatalf("Payload.Scalar = %v, want %v ± %v (absent-as-zero policy)", got, wantStat, eps)
	}

	// df still 1; the absent cell does not collapse the matrix shape.
	if df, ok := layers[0].Summary.Parameters["df"]; !ok || df != 1.0 {
		t.Fatalf("Summary.Parameters[df] = %v (ok=%v), want 1 (absent cell preserves rectangular shape)",
			df, ok)
	}

	// Observed count = 3 (one cell absent in a 4-cell matrix).
	if layers[0].Summary.Count == nil || *layers[0].Summary.Count != 3 {
		t.Fatalf("Summary.Count = %v, want 3 (one absent host cell)", layers[0].Summary.Count)
	}
}

// TestOverlay_ChiSqMatrix_DegenerateShapeRejected verifies the handler
// rejects 1×N or N×1 contingencies (χ² independence requires ≥ 2×2).
// The error short-circuits ApplyOverlays so the orchestrator surfaces
// the failure the same way predict would have flagged it (defense in
// depth — validator catches this at predict time too).
func TestOverlay_ChiSqMatrix_DegenerateShapeRejected(t *testing.T) {
	payload := &types.MatrixPayload{
		RowKeys:    []types.AxisKey{{"r0"}},
		ColumnKeys: []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{{Value: 1.0, Present: true}, {Value: 2.0, Present: true}},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqMatrix,
			Scope: types.OverlayScopeMatrix,
		},
	}
	_, _, err := ApplyOverlays(specs, host)
	if err == nil {
		t.Fatalf("expected error for 1×2 contingency; got nil")
	}
}
