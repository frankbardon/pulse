package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func chiSqColPayloadKnown() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 10.0, Present: true},
				{Value: 20.0, Present: true},
			},
			{
				{Value: 20.0, Present: true},
				{Value: 20.0, Present: true},
			},
			{
				{Value: 30.0, Present: true},
				{Value: 20.0, Present: true},
			},
		},
		RowMargins: []types.MatrixCell{
			{Value: 30.0, Present: true},
			{Value: 40.0, Present: true},
			{Value: 50.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 60.0, Present: true},
			{Value: 60.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 120.0, Present: true},
		CellLabel:  "AGG_COUNT_id",
	}
}

// TestOverlay_ChiSqCol_PerColStat verifies the closed-form per-column
// χ² statistic and p-value on the 3 × 2 fixture in
// chiSqColPayloadKnown. Both columns produce ≈ 2.6667 with df=2, p ≈
// 0.2636. Float-tolerant assertion. Also asserts the SERIES payload +
// per-entry Summary plumbing — Statistic, PValue, and Parameters{"df"}
// populated; Scalar / Matrix slots nil; layer-level Summary stays nil
// (the per-column statistics ride on the entries, not the layer slot —
// mirrors the CHISQ_ROW per-entry summary policy).
func TestOverlay_ChiSqCol_PerColStat(t *testing.T) {
	host := NewCrosstabHostView(chiSqColPayloadKnown())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
			Ref:   types.OverlayRef{}, // implicit-margin
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	// Every expected cell is 15, 20, or 25 — all ≥ 5, no low-expected
	// warnings should fire.
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings (all expected cells ≥ 5); got %d: %+v",
			len(warnings), warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	layer := layers[0]

	// Shape contract: SERIES payload, Scalar / Matrix nil.
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("Payload.Shape = %q, want %q", layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Scalar != nil {
		t.Fatalf("Payload.Scalar = %v, want nil for SERIES shape", layer.Payload.Scalar)
	}
	if layer.Payload.Matrix != nil {
		t.Fatalf("Payload.Matrix = %+v, want nil for SERIES shape", layer.Payload.Matrix)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("Payload.Series nil; want populated SeriesPayload")
	}

	entries := layer.Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("Series.Entries length = %d, want 2", len(entries))
	}

	// Per-column hand check: each column has χ² ≈ 2.6667 (5/3 + 0 + 1 = 8/3).
	const wantStat = 8.0 / 3.0 // ≈ 2.6667
	const wantP = 0.2636       // survival(χ²=2.6667, df=2) ≈ exp(-1.3333) ≈ 0.2636
	const epsStat = 1e-4
	const epsP = 5e-4

	for i, e := range entries {
		if e.Summary.Statistic == nil ||
			math.Abs(*e.Summary.Statistic-wantStat) > epsStat {
			t.Errorf("Entries[%d].Summary.Statistic = %v, want %v ± %v",
				i, e.Summary.Statistic, wantStat, epsStat)
		}
		if e.Summary.PValue == nil ||
			math.Abs(*e.Summary.PValue-wantP) > epsP {
			t.Errorf("Entries[%d].Summary.PValue = %v, want %v ± %v",
				i, e.Summary.PValue, wantP, epsP)
		}
		if e.Summary.Parameters == nil {
			t.Errorf("Entries[%d].Summary.Parameters nil; want {\"df\": 2}", i)
			continue
		}
		if df, ok := e.Summary.Parameters["df"]; !ok || df != 2.0 {
			t.Errorf("Entries[%d].Summary.Parameters[\"df\"] = %v (ok=%v), want 2",
				i, df, ok)
		}
	}

	// Layer-level Summary stays nil — per-entry summaries carry the
	// inferential metadata, not the layer slot.
	if layer.Summary != nil {
		t.Errorf("layer.Summary = %+v, want nil (per-entry summaries carry the test result for SERIES inferential overlays)",
			layer.Summary)
	}

	// Layer metadata: kind / scope / synthesised default name.
	if layer.Kind != types.OverlayKindChiSqCol {
		t.Fatalf("Kind = %q, want %q", layer.Kind, types.OverlayKindChiSqCol)
	}
	if layer.Scope != types.OverlayScopeColumn {
		t.Fatalf("Scope = %q, want %q", layer.Scope, types.OverlayScopeColumn)
	}
	if got, want := layer.Name, "chisq_col"; got != want {
		t.Fatalf("default Name = %q, want %q", got, want)
	}
}

// TestOverlay_ChiSqCol_ExpectedLowEmitsWarn verifies the canonical χ²
// low-expected-count heuristic: when any expected cell value in a
// column is below 5 the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW
// warning per offending column (not per cell). Uses a 3 × 2 contingency
// where column 0 has small counts (every expected < 5) and column 1
// has large counts (every expected ≥ 5) so exactly one warning fires.
func TestOverlay_ChiSqCol_ExpectedLowEmitsWarn(t *testing.T) {
	// 3×2. Col 0 sums to 6 (small); col 1 sums to 100 (large).
	// row_margins = [10, 35, 51], grand = 106.
	//
	// c0 expecteds: 10*6/106=0.566, 35*6/106=1.981, 51*6/106=2.887 — all <5
	// c1 expecteds: 10*100/106=9.434, 35*100/106=33.019, 51*100/106=48.113 — all ≥5
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 1.0, Present: true},
				{Value: 9.0, Present: true},
			},
			{
				{Value: 2.0, Present: true},
				{Value: 33.0, Present: true},
			},
			{
				{Value: 3.0, Present: true},
				{Value: 58.0, Present: true},
			},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	// Exactly one warning — for column 0 only.
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 PULSE_OVERLAY_EXPECTED_LOW warning (col 0), got %d: %+v",
			len(warnings), warnings)
	}
	if got, want := warnings[0].Code, "PULSE_OVERLAY_EXPECTED_LOW"; got != want {
		t.Fatalf("warning Code = %q, want %q (stub code)", got, want)
	}
	// Details should carry the column index (0) and count of low-expected
	// cells (all 3 cells in col 0 have expected < 5).
	colIdx, ok := warnings[0].Details["col_index"].(int)
	if !ok || colIdx != 0 {
		t.Fatalf("Details[col_index] = %v (ok=%v), want 0", colIdx, ok)
	}
	low, ok := warnings[0].Details["low_expected_cells"].(int)
	if !ok || low != 3 {
		t.Fatalf("Details[low_expected_cells] = %v (ok=%v), want 3", low, ok)
	}

	// Both columns must still emit entries — the warning is advisory,
	// not fatal. col 0's entry holds a finite statistic alongside the
	// warning so callers can decide whether to trust it.
	entries := layers[0].Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("Series.Entries length = %d, want 2", len(entries))
	}
	for i, e := range entries {
		if e.Summary.Statistic == nil {
			t.Errorf("Entries[%d].Summary.Statistic nil; statistic should still be emitted alongside the warning", i)
			continue
		}
		if v := *e.Summary.Statistic; math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("Entries[%d].Summary.Statistic = %v, want finite chi-square value", i, v)
		}
	}
}

// TestOverlay_ChiSqCol_SeriesEntryOrderMatchesColumnKeys verifies the
// documented parallel-slice contract: SeriesPayload.Entries[i].Key
// equals host ColumnKeys[i] element-for-element. Renderers rely on
// this to lay the SERIES overlay alongside the host axis without
// re-keying.
func TestOverlay_ChiSqCol_SeriesEntryOrderMatchesColumnKeys(t *testing.T) {
	payload := chiSqColPayloadKnown()
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(payload.ColumnKeys) {
		t.Fatalf("Series.Entries length = %d, want %d (parity with ColumnKeys)",
			len(entries), len(payload.ColumnKeys))
	}
	for i, e := range entries {
		if len(e.Key) != len(payload.ColumnKeys[i]) {
			t.Errorf("Entries[%d].Key length = %d, want %d (matching ColumnKeys[%d])",
				i, len(e.Key), len(payload.ColumnKeys[i]), i)
			continue
		}
		for j := range e.Key {
			if e.Key[j] != payload.ColumnKeys[i][j] {
				t.Errorf("Entries[%d].Key[%d] = %v, want %v (matching ColumnKeys[%d][%d])",
					i, j, e.Key[j], payload.ColumnKeys[i][j], i, j)
			}
		}
	}
}

// TestOverlay_ChiSqCol_AbsentCellsHandled verifies the documented
// absent-cell policy: a structurally absent host cell (Present=false)
// is treated as observed count 0. The matrix shape stays rectangular,
// the per-column χ² recurrence runs over the full row count, and the
// statistic uses the absent-as-zero observation without inventing a
// count.
//
// Hand check (3 × 2 with cells[2][0] absent — column 0 has an absent
// row):
//
//	cells   = [[10, 10], [20, 20], [0, 30]]   (absent → 0)
//	col_tot = [30, 60]
//	row_tot = [20, 40, 30]
//	grand   = 90
//	c0 expected = [20*30/90, 40*30/90, 30*30/90]
//	            = [6.6667, 13.3333, 10.0]
//	c0 chi²    = (10-6.6667)²/6.6667 + (20-13.3333)²/13.3333 + (0-10)²/10
//	           = 1.6667 + 3.3333 + 10
//	           = 15.0
//
// Verifies the handler does NOT crash on an absent cell and the
// column's statistic correctly absorbs the zero observation.
func TestOverlay_ChiSqCol_AbsentCellsHandled(t *testing.T) {
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}, {"r2"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 10.0, Present: true},
				{Value: 10.0, Present: true},
			},
			{
				{Value: 20.0, Present: true},
				{Value: 20.0, Present: true},
			},
			{
				{Present: false},
				{Value: 30.0, Present: true},
			},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("Series.Entries length = %d, want 2", len(entries))
	}

	// Column 0 χ² ≈ 15.0 within 1e-3 tolerance (absent cell counted as 0).
	const wantC0 = 15.0
	const eps = 1e-3
	if entries[0].Summary.Statistic == nil {
		t.Fatalf("Entries[0].Summary.Statistic nil")
	}
	if got := *entries[0].Summary.Statistic; math.Abs(got-wantC0) > eps {
		t.Fatalf("Entries[0].Summary.Statistic = %v, want %v ± %v (absent-as-zero policy)",
			got, wantC0, eps)
	}

	// df = rows - 1 = 2 regardless of absent cells — the row count
	// does not collapse.
	if df, ok := entries[0].Summary.Parameters["df"]; !ok || df != 2.0 {
		t.Fatalf("Entries[0].Summary.Parameters[df] = %v (ok=%v), want 2 (row count preserved)",
			df, ok)
	}
}

// TestOverlay_ChiSqCol_DegenerateShapeRejected verifies the handler
// rejects 1 × N contingencies (per-column χ² requires ≥ 2 rows so df
// ≥ 1). The error short-circuits ApplyOverlays so the orchestrator
// surfaces the failure the same way predict would have flagged it
// (defense in depth — validator catches this at predict time too).
func TestOverlay_ChiSqCol_DegenerateShapeRejected(t *testing.T) {
	payload := &types.MatrixPayload{
		RowKeys:    []types.AxisKey{{"r0"}},
		ColumnKeys: []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 1.0, Present: true},
				{Value: 2.0, Present: true},
			},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqCol,
			Scope: types.OverlayScopeColumn,
		},
	}
	_, _, err := ApplyOverlays(specs, host)
	if err == nil {
		t.Fatalf("expected error for 1×2 contingency; got nil")
	}
}
