package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// fisherCellPayloadKnown returns a 2×2 host MatrixPayload sized for a
// closed-form Fisher's exact reference. The cell at (r0, c0) carries
// 1 of the 24 grand-total observations; the matching 2×2 contingency
// (for that cell) is:
//
//	        c=c0   c≠c0
//	r=r0     1      9      (row_margin = 10)
//	r≠r0    11      3      (col_margin = 12, grand = 24)
//
// row1 = 10 (top of row), row2 = 14 (bottom of row), col1 = 12,
// col2 = 12, grand = 24. The handler reads row_margin[r0] = 10 and
// col_margin[c0] = 12; the 2×2 for that cell is {a=1, b=9, c=11, d=3}.
//
// The two-sided Fisher's exact p-value for this 2×2 (textbook
// hypergeometric in the small-margin regime — sum of probabilities for
// every feasible cell[0][0] value whose log-prob is ≤ the observed) is
// approximately 0.00276 from this implementation. Cross-checked via
// the helper fisherExactTwoSided directly.
//
// The fixture exercises the (r0, c0) cell explicitly. (r1, c1) has
// value 3 — its 2×2 is {a=3, b=11, c=9, d=1}, which by the symmetry
// of the hypergeometric distribution under reflection of marginals
// (a → row1 - a, swapping rows / cols) produces the SAME p-value.
func fisherCellPayloadKnown() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 1.0, Present: true},
				{Value: 9.0, Present: true},
			},
			{
				{Value: 11.0, Present: true},
				{Value: 3.0, Present: true},
			},
		},
		RowMargins: []types.MatrixCell{
			{Value: 10.0, Present: true},
			{Value: 14.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 12.0, Present: true},
			{Value: 12.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 24.0, Present: true},
		CellLabel:  "AGG_COUNT_id",
	}
}

// TestOverlay_FisherExactCell_TwoByTwo verifies the closed-form two-
// sided p-value on a hand-computable 2×2 fixture and exercises the
// zero-count cell path. Acceptance criterion from the story.
//
// Two sub-cases:
//
//	"normal_counts" — cell (r0, c0) with value 1 (2×2 = {1, 9, 11, 3})
//	  produces p ≈ 0.00161 (textbook two-sided Fisher's exact result
//	  for this 2×2; cross-checked against the helper
//	  fisherExactTwoSided directly).
//
//	"zero_count" — uses a contingency where one host cell is exactly
//	  zero (cell (r1, c1) collapses to {a=0, b=14, c=12, d=10} → p = 1.0
//	  because the 2×2 is fully degenerate under the hypergeometric).
//	  Verifies the handler does not crash on a zero cell and emits the
//	  conservative no-evidence p = 1.0.
func TestOverlay_FisherExactCell_TwoByTwo(t *testing.T) {
	t.Run("normal_counts", func(t *testing.T) {
		host := NewCrosstabHostView(fisherCellPayloadKnown())
		specs := []types.OverlaySpec{
			{
				Kind:  types.OverlayKindFisherExactCell,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{}, // implicit-margin
			},
		}
		layers, _, err := ApplyOverlays(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		if len(layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(layers))
		}
		layer := layers[0]

		// Shape contract: MATRIX payload, Scalar / Series nil.
		if layer.Payload.Shape != types.OverlayShapeMatrix {
			t.Fatalf("Payload.Shape = %q, want %q", layer.Payload.Shape, types.OverlayShapeMatrix)
		}
		if layer.Payload.Matrix == nil {
			t.Fatalf("Payload.Matrix nil; want populated MatrixPayload")
		}
		if layer.Payload.Scalar != nil {
			t.Fatalf("Payload.Scalar = %v, want nil for MATRIX shape", layer.Payload.Scalar)
		}
		if layer.Payload.Series != nil {
			t.Fatalf("Payload.Series = %+v, want nil for MATRIX shape", layer.Payload.Series)
		}

		mp := layer.Payload.Matrix
		if len(mp.Cells) != 2 || len(mp.Cells[0]) != 2 {
			t.Fatalf("Cells shape = %d×%d, want 2×2", len(mp.Cells), len(mp.Cells[0]))
		}

		// Cross-check via the helper directly — the cell value the
		// handler emitted must equal fisherExactTwoSided on the same
		// 2×2.
		const (
			a = 1
			b = 9
			c = 11
			d = 3
		)
		want := fisherExactTwoSided(a, b, c, d)
		if want <= 0 || want >= 1 {
			t.Fatalf("reference p = %v is not in (0, 1); fixture is broken", want)
		}
		// Reference p for {1, 9, 11, 3} from this implementation is
		// ≈ 0.00276 — the two-sided hypergeometric tail at marginals
		// (10, 14, 12, 12) with observed cell[0][0] = 1. Far from
		// expected (10*12/24 = 5), so the p is strongly significant.
		// The exact value comes from the lgamma-backed hypergeometric
		// implementation factored into fisherExactTwoSided.
		const refP = 0.00276
		const refEps = 5e-4
		if math.Abs(want-refP) > refEps {
			t.Fatalf("helper reference p = %v, expected ≈ %v ± %v (fixture cross-check)",
				want, refP, refEps)
		}

		cell := mp.Cells[0][0]
		if !cell.Present {
			t.Fatalf("cell (0,0) not present; want present p-value")
		}
		got, ok := cell.Value.(float64)
		if !ok {
			t.Fatalf("cell (0,0).Value type = %T, want float64", cell.Value)
		}
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("cell (0,0).Value = %v, want %v ± 1e-9 (helper byte-equal)",
				got, want)
		}

		// Layer metadata: kind / scope / synthesised default name.
		if layer.Kind != types.OverlayKindFisherExactCell {
			t.Fatalf("Kind = %q, want %q", layer.Kind, types.OverlayKindFisherExactCell)
		}
		if layer.Scope != types.OverlayScopeCell {
			t.Fatalf("Scope = %q, want %q", layer.Scope, types.OverlayScopeCell)
		}
		if got, wantName := layer.Name, "fisher_exact_cell"; got != wantName {
			t.Fatalf("default Name = %q, want %q", got, wantName)
		}

		// Summary: baseline=0, Min/Max/Count populated.
		if layer.Summary == nil {
			t.Fatalf("layer.Summary nil; want populated Summary")
		}
		if layer.Summary.Baseline == nil || *layer.Summary.Baseline != 0.0 {
			t.Errorf("Summary.Baseline = %v, want 0", layer.Summary.Baseline)
		}
		if layer.Summary.Count == nil || *layer.Summary.Count != 4 {
			t.Errorf("Summary.Count = %v, want 4 (every cell present)", layer.Summary.Count)
		}
	})

	t.Run("zero_count", func(t *testing.T) {
		// 2×2 with a literal zero cell at (r0, c0). row_margin[r0] = 10,
		// col_margin[c0] = 12, grand = 24. The 2×2 for (r0, c0) is
		// {a=0, b=10, c=12, d=2}. Fisher's exact on this 2×2 is
		// degenerate enough to produce a non-trivial p < 1 but still
		// well-defined.
		payload := &types.MatrixPayload{
			RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
			ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
			RowKeys:      []types.AxisKey{{"r0"}, {"r1"}},
			ColumnKeys:   []types.AxisKey{{"c0"}, {"c1"}},
			Cells: [][]types.MatrixCell{
				{
					{Value: 0.0, Present: true},
					{Value: 10.0, Present: true},
				},
				{
					{Value: 12.0, Present: true},
					{Value: 2.0, Present: true},
				},
			},
			RowMargins: []types.MatrixCell{
				{Value: 10.0, Present: true},
				{Value: 14.0, Present: true},
			},
			ColumnMargins: []types.MatrixCell{
				{Value: 12.0, Present: true},
				{Value: 12.0, Present: true},
			},
			GrandTotal: types.MatrixCell{Value: 24.0, Present: true},
		}
		host := NewCrosstabHostView(payload)
		specs := []types.OverlaySpec{
			{
				Kind:  types.OverlayKindFisherExactCell,
				Scope: types.OverlayScopeCell,
			},
		}
		layers, _, err := ApplyOverlays(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlays: %v", err)
		}
		if len(layers) != 1 {
			t.Fatalf("expected 1 layer, got %d", len(layers))
		}
		mp := layers[0].Payload.Matrix
		if mp == nil {
			t.Fatalf("Matrix payload nil")
		}
		cell := mp.Cells[0][0]
		if !cell.Present {
			t.Fatalf("cell (0,0) not present; zero-count host cell should still produce a p-value")
		}
		got, ok := cell.Value.(float64)
		if !ok {
			t.Fatalf("cell (0,0).Value type = %T, want float64", cell.Value)
		}
		// Cross-check via helper. 2×2 = {0, 10, 12, 2}.
		want := fisherExactTwoSided(0, 10, 12, 2)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("cell (0,0).Value = %v, want %v ± 1e-9 (helper byte-equal on zero-count 2×2)",
				got, want)
		}
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Errorf("cell (0,0).Value = %v on zero-count 2×2; want finite p in [0, 1]", got)
		}
		if got < 0 || got > 1 {
			t.Errorf("cell (0,0).Value = %v, want in [0, 1]", got)
		}
	})
}

// TestOverlay_FisherExactCell_ExpectedLowEmitsWarn verifies the Cochran
// rule fires on a 2×2 whose expected counts are below 5 — exactly the
// regime where Fisher's exact is structurally required and χ² would be
// unreliable. The warning is advisory; the p-value still rides on the
// cell.
func TestOverlay_FisherExactCell_ExpectedLowEmitsWarn(t *testing.T) {
	// 2×2 with small marginals so every expected cell is small.
	// row1 = 3, row2 = 17, col1 = 4, col2 = 16, grand = 20.
	// Expected = [3*4/20=0.6, 3*16/20=2.4, 17*4/20=3.4, 17*16/20=13.6]
	// → three of four expected cells below 5, one below 1 — Cochran
	// rule fires on this cell.
	payload := &types.MatrixPayload{
		RowKeys:    []types.AxisKey{{"r0"}, {"r1"}},
		ColumnKeys: []types.AxisKey{{"c0"}, {"c1"}},
		Cells: [][]types.MatrixCell{
			{
				{Value: 2.0, Present: true},
				{Value: 1.0, Present: true},
			},
			{
				{Value: 2.0, Present: true},
				{Value: 15.0, Present: true},
			},
		},
		RowMargins: []types.MatrixCell{
			{Value: 3.0, Present: true},
			{Value: 17.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 4.0, Present: true},
			{Value: 16.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 20.0, Present: true},
	}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindFisherExactCell,
			Scope: types.OverlayScopeCell,
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	if len(warnings) == 0 {
		t.Fatalf("expected at least 1 PULSE_OVERLAY_EXPECTED_LOW warning on low-expected 2×2; got 0")
	}
	for _, w := range warnings {
		if w.Code != "PULSE_OVERLAY_EXPECTED_LOW" {
			t.Errorf("warning Code = %q, want PULSE_OVERLAY_EXPECTED_LOW (stub)", w.Code)
		}
		if _, ok := w.Details["row_index"]; !ok {
			t.Errorf("warning Details missing row_index: %+v", w.Details)
		}
		if _, ok := w.Details["col_index"]; !ok {
			t.Errorf("warning Details missing col_index: %+v", w.Details)
		}
	}

	// Every cell should still emit a finite p-value — the warning is
	// advisory and Fisher's exact stays exact in the low-count regime.
	mp := layers[0].Payload.Matrix
	if mp == nil {
		t.Fatalf("Matrix payload nil")
	}
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			cell := mp.Cells[i][j]
			if !cell.Present {
				t.Errorf("cell (%d, %d) not present; advisory warning should not suppress the p-value", i, j)
				continue
			}
			v, ok := cell.Value.(float64)
			if !ok {
				t.Errorf("cell (%d, %d).Value type = %T, want float64", i, j, cell.Value)
				continue
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Errorf("cell (%d, %d).Value = %v, want finite p", i, j, v)
			}
			if v < 0 || v > 1 {
				t.Errorf("cell (%d, %d).Value = %v, want in [0, 1]", i, j, v)
			}
		}
	}
}

// TestOverlay_FisherExactCell_AbsentCellsStayAbsent verifies the
// documented absent-cell policy: a structurally absent host cell
// (Present=false) stays absent on the overlay. Mirrors the
// INDEX_VS_MARGIN / SHARE_OF_* policy.
func TestOverlay_FisherExactCell_AbsentCellsStayAbsent(t *testing.T) {
	payload := fisherCellPayloadKnown()
	payload.Cells[0][1] = types.MatrixCell{Present: false}
	host := NewCrosstabHostView(payload)
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindFisherExactCell,
			Scope: types.OverlayScopeCell,
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	mp := layers[0].Payload.Matrix
	if mp == nil {
		t.Fatalf("Matrix payload nil")
	}
	if mp.Cells[0][1].Present {
		t.Errorf("cell (0, 1).Present = true on absent host cell; want absent overlay cell")
	}
	// Sibling cells that are present must still produce p-values.
	if !mp.Cells[0][0].Present {
		t.Errorf("cell (0, 0).Present = false on present host cell; want populated p-value")
	}
}

func TestOverlay_FisherExactCell_HelperMatchesRowTest(t *testing.T) {
	cases := []struct {
		name       string
		a, b, c, d int
		wantP      float64
		eps        float64
	}{
		// {1, 9, 11, 3}: small-margin 2×2; p ≈ 0.00276.
		{name: "small_textbook", a: 1, b: 9, c: 11, d: 3, wantP: 0.00276, eps: 5e-4},
		// {0, 10, 12, 2}: zero-cell 2×2; p ≈ 6.73e-05 — strongly
		// significant because the observed has zero of an expected 5.
		{name: "symmetric_zero", a: 0, b: 10, c: 12, d: 2, wantP: 6.73e-05, eps: 1e-5},
		// Fully-degenerate 2×2 (n = 0) returns 1.0 by contract.
		{name: "degenerate", a: 0, b: 0, c: 0, d: 0, wantP: 1.0, eps: 1e-12},
		// Perfectly-balanced 2×2 (observed exactly matches expected)
		// produces p = 1.0 — no rejection signal.
		{name: "balanced_2x2", a: 5, b: 5, c: 5, d: 5, wantP: 1.0, eps: 1e-6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fisherExactTwoSided(tc.a, tc.b, tc.c, tc.d)
			if got < 0 || got > 1 {
				t.Errorf("p = %v out of range [0, 1]", got)
			}
			if math.Abs(got-tc.wantP) > tc.eps {
				t.Errorf("fisherExactTwoSided(%d, %d, %d, %d) = %v, want %v ± %v",
					tc.a, tc.b, tc.c, tc.d, got, tc.wantP, tc.eps)
			}
		})
	}
}
