package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// chiSqRowPayloadKnown returns a synthetic 2 × 3 host MatrixPayload
// whose per-row χ² goodness-of-fit statistics are computable in closed
// form. The matrix:
//
//	      c0   c1   c2  | row_margin
//	r0    10   20   30  |   60
//	r1    20   20   20  |   60
//	col   30   40   50  |  grand = 120
//
// Expected counts (row_margin × col_margin / grand):
//
//	r0: [60*30/120, 60*40/120, 60*50/120] = [15, 20, 25]
//	r1: [60*30/120, 60*40/120, 60*50/120] = [15, 20, 25]
//
// Per-row χ²:
//
//	χ²_r0 = (10-15)²/15 + (20-20)²/20 + (30-25)²/25
//	      = 25/15 + 0 + 25/25
//	      = 1.6667 + 0 + 1.0
//	      = 2.6667
//	χ²_r1 = (20-15)²/15 + (20-20)²/20 + (20-25)²/25
//	      = 25/15 + 0 + 25/25
//	      = 1.6667 + 0 + 1.0
//	      = 2.6667
//
// df = cols - 1 = 2. p-value for χ² ≈ 2.6667, df=2 is ≈ 0.2636 (the
// survival of the χ² distribution with 2 degrees of freedom at 2.6667
// equals exp(-2.6667/2) ≈ 0.2636).
//
// Both rows produce the same statistic on this symmetric contingency —
// the test asserts both entries are populated independently and align
// with RowKeys element-for-element.
func chiSqRowPayloadKnown() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 10.0, Present: true},
				{Value: 20.0, Present: true},
				{Value: 30.0, Present: true},
			},
			{
				{Value: 20.0, Present: true},
				{Value: 20.0, Present: true},
				{Value: 20.0, Present: true},
			},
		},
		RowMargins: []types.MatrixCell{
			{Value: 60.0, Present: true},
			{Value: 60.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 30.0, Present: true},
			{Value: 40.0, Present: true},
			{Value: 50.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 120.0, Present: true},
		CellLabel:  "AGG_COUNT_id",
	}
}

// TestOverlay_ChiSqRow_PerRowStat verifies the closed-form per-row χ²
// statistic and p-value on the 2 × 3 fixture in chiSqRowPayloadKnown.
// Both rows produce ≈ 2.6667 with df=2, p ≈ 0.2636. Float-tolerant
// assertion. Also asserts the SERIES payload + per-entry Summary
// plumbing — Statistic, PValue, and Parameters{"df"} populated;
// Scalar / Matrix slots nil; layer-level Summary stays nil (the per-row
// statistics ride on the entries, not the layer slot).
func TestOverlay_ChiSqRow_PerRowStat(t *testing.T) {
	host := NewCrosstabHostView(chiSqRowPayloadKnown())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
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

	// Per-row hand check: each row has χ² ≈ 2.6667 (5/3 + 0 + 1 = 8/3).
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
	if layer.Kind != types.OverlayKindChiSqRow {
		t.Fatalf("Kind = %q, want %q", layer.Kind, types.OverlayKindChiSqRow)
	}
	if layer.Scope != types.OverlayScopeRow {
		t.Fatalf("Scope = %q, want %q", layer.Scope, types.OverlayScopeRow)
	}
	if got, want := layer.Name, "chisq_row"; got != want {
		t.Fatalf("default Name = %q, want %q", got, want)
	}
}

// TestOverlay_ChiSqRow_ExpectedLowEmitsWarn verifies the canonical χ²
// low-expected-count heuristic: when any expected cell value in a row
// is below 5 the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW warning
// per offending row (not per cell). Uses a 2 × 3 contingency where row
// 0 has small counts (every expected < 5) and row 1 has large counts
// (every expected ≥ 5) so exactly one warning fires.
func TestOverlay_ChiSqRow_ExpectedLowEmitsWarn(t *testing.T) {
	// 2×3. Row 0 sums to 6 (expecteds 1.07 / 2.0 / 2.93); row 1 sums to
	// 100 (expecteds 17.86 / 33.33 / 48.81). col_margins = [10, 35, 51],
	// grand = 106.
	//
	// Row 0 expected = [60*10/106, 60*35/106, 60*51/106] — wait, let me
	// reshape: row sums = [6, 100], col sums = [10, 35, 51], grand=106.
	//
	// r0 expecteds: 6*10/106=0.566, 6*35/106=1.981, 6*51/106=2.887 — all <5
	// r1 expecteds: 100*10/106=9.434, 100*35/106=33.019, 100*51/106=48.113 — all ≥5
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 1.0, Present: true},
				{Value: 2.0, Present: true},
				{Value: 3.0, Present: true},
			},
			{
				{Value: 9.0, Present: true},
				{Value: 33.0, Present: true},
				{Value: 58.0, Present: true},
			},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	// Exactly one warning — for row 0 only.
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 PULSE_OVERLAY_EXPECTED_LOW warning (row 0), got %d: %+v",
			len(warnings), warnings)
	}
	if got, want := warnings[0].Code, "PULSE_OVERLAY_EXPECTED_LOW"; got != want {
		t.Fatalf("warning Code = %q, want %q (stub code; E2-S10 promotes)", got, want)
	}
	// Details should carry the row index (0) and count of low-expected
	// cells (all 3 cells in row 0 have expected < 5).
	rowIdx, ok := warnings[0].Details["row_index"].(int)
	if !ok || rowIdx != 0 {
		t.Fatalf("Details[row_index] = %v (ok=%v), want 0", rowIdx, ok)
	}
	low, ok := warnings[0].Details["low_expected_cells"].(int)
	if !ok || low != 3 {
		t.Fatalf("Details[low_expected_cells] = %v (ok=%v), want 3", low, ok)
	}

	// Both rows must still emit entries — the warning is advisory, not
	// fatal. row 0's entry holds a finite statistic alongside the
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

// TestOverlay_ChiSqRow_SeriesEntryOrderMatchesRowKeys verifies the
// documented parallel-slice contract: SeriesPayload.Entries[i].Key
// equals host RowKeys[i] element-for-element. Renderers rely on this
// to lay the SERIES overlay alongside the host axis without re-keying.
func TestOverlay_ChiSqRow_SeriesEntryOrderMatchesRowKeys(t *testing.T) {
	payload := chiSqRowPayloadKnown()
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(payload.RowKeys) {
		t.Fatalf("Series.Entries length = %d, want %d (parity with RowKeys)",
			len(entries), len(payload.RowKeys))
	}
	for i, e := range entries {
		if len(e.Key) != len(payload.RowKeys[i]) {
			t.Errorf("Entries[%d].Key length = %d, want %d (matching RowKeys[%d])",
				i, len(e.Key), len(payload.RowKeys[i]), i)
			continue
		}
		for j := range e.Key {
			if e.Key[j] != payload.RowKeys[i][j] {
				t.Errorf("Entries[%d].Key[%d] = %v, want %v (matching RowKeys[%d][%d])",
					i, j, e.Key[j], payload.RowKeys[i][j], i, j)
			}
		}
	}
}

// TestOverlay_ChiSqRow_AbsentCellsHandled verifies the documented
// absent-cell policy: a structurally absent host cell (Present=false)
// is treated as observed count 0. The matrix shape stays rectangular,
// the per-row χ² recurrence runs over the full column count, and the
// statistic uses the absent-as-zero observation without inventing a
// count.
//
// Hand check (2 × 3 with cells[0][2] absent):
//
//	cells   = [[10, 20, 0], [10, 20, 30]]   (absent → 0)
//	row_tot = [30, 60]
//	col_tot = [20, 40, 30]
//	grand   = 90
//	r0 expected = [30*20/90, 30*40/90, 30*30/90]
//	            = [6.6667, 13.3333, 10.0]
//	r0 chi²    = (10-6.6667)²/6.6667 + (20-13.3333)²/13.3333 + (0-10)²/10
//	           = 1.6667 + 3.3333 + 10
//	           = 15.0
//
// Verifies the handler does NOT crash on an absent cell and the row's
// statistic correctly absorbs the zero observation.
func TestOverlay_ChiSqRow_AbsentCellsHandled(t *testing.T) {
	payload := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}, {"c2"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 10.0, Present: true},
				{Value: 20.0, Present: true},
				{Present: false},
			},
			{
				{Value: 10.0, Present: true},
				{Value: 20.0, Present: true},
				{Value: 30.0, Present: true},
			},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
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

	// Row 0 χ² ≈ 15.0 within 1e-3 tolerance (absent cell counted as 0).
	const wantR0 = 15.0
	const eps = 1e-3
	if entries[0].Summary.Statistic == nil {
		t.Fatalf("Entries[0].Summary.Statistic nil")
	}
	if got := *entries[0].Summary.Statistic; math.Abs(got-wantR0) > eps {
		t.Fatalf("Entries[0].Summary.Statistic = %v, want %v ± %v (absent-as-zero policy)",
			got, wantR0, eps)
	}

	// df = cols - 1 = 2 regardless of absent cells — the column count
	// does not collapse.
	if df, ok := entries[0].Summary.Parameters["df"]; !ok || df != 2.0 {
		t.Fatalf("Entries[0].Summary.Parameters[df] = %v (ok=%v), want 2 (column count preserved)",
			df, ok)
	}
}

// TestOverlay_ChiSqRow_DegenerateShapeRejected verifies the handler
// rejects N × 1 contingencies (per-row χ² requires ≥ 2 columns so df
// ≥ 1). The error short-circuits ApplyOverlays so the orchestrator
// surfaces the failure the same way predict would have flagged it
// (defense in depth — validator catches this at predict time too).
func TestOverlay_ChiSqRow_DegenerateShapeRejected(t *testing.T) {
	payload := &types.MatrixPayload{
		RowKeys:    []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys: []types.AxisKey{{"c0"}},
		Cells: [][]types.MatrixCell{
			{{Value: 1.0, Present: true}},
			{{Value: 2.0, Present: true}},
		},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindChiSqRow,
			Scope: types.OverlayScopeRow,
		},
	}
	_, _, err := ApplyOverlays(specs, host)
	if err == nil {
		t.Fatalf("expected error for 2×1 contingency; got nil")
	}
}
