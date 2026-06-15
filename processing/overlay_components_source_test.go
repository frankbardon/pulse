// E3-S7 reader-migration regression gate: the four stat-test parity
// overlay handlers MUST read `(mean, variance, n)` from
// Response.Components.Crosstab.CellComponents[r][c] (MATRIX arm) or
// the corresponding row-shape map (SERIES arm) and MUST NOT reach for
// the typed `WelfordTriple` payload on MatrixCell.Value / row value
// columns. These tests pin the migrated source so a refactor that
// reintroduces the WelfordTriple type-assertion would fail here.
//
// Scope:
//
//   - For each of the four kinds (OVERLAY_T_CELL, OVERLAY_Z_CELL,
//     OVERLAY_T_VS_REF, OVERLAY_Z_VS_REF) build a response whose target
//     cell / row carries a WelfordTriple in the legacy smuggle slot
//     but NO components-source triple. The handler MUST NOT consume
//     the legacy triple — the cell is treated as non-addressable and
//     skipped silently (matrix arm) or routes through the scalar
//     fallback (which itself returns false for a non-numeric
//     WelfordTriple value). The resulting overlay layer carries no
//     finite p-value for the (r0, c0) cell / first series entry.
//
//   - The scalar+Params additive contract is exercised separately by
//     the existing TestApplyTCell_ScalarKnownAnswer /
//     TestApplyZCell_ScalarKnownAnswer / TestApplyTVsRef_ScalarKnown
//     Answer / TestApplyZVsRef_ScalarKnownAnswer tests — no need to
//     re-cover the additive path here.
package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// makeSingleCellLegacyTripleResponse builds a 1×1 MATRIX-shape Response
// whose cell carries a WelfordTriple in the legacy MatrixCell.Value
// smuggle slot but NO Response.Components.Crosstab.CellComponents
// entry. After the E3-S7 migration the parity handlers must NOT
// consume the legacy triple.
func makeSingleCellLegacyTripleResponse(triple WelfordTriple) *types.Response {
	cells := [][]types.MatrixCell{{{Value: triple, Present: true}}}
	return &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        cells,
			},
		},
		// Components intentionally absent — the migrated handler must
		// not reach for the typed WelfordTriple smuggled in
		// MatrixCell.Value.
	}
}

// makeSeriesResponseLegacyTriples builds a Response.Data series whose
// rows carry typed WelfordTriple values in the value column slot but
// no components-source map shape. The migrated SERIES handlers must
// not consume the legacy typed payload.
func makeSeriesResponseLegacyTriples(keys []string, triples []WelfordTriple) *types.Response {
	if len(keys) != len(triples) {
		panic("makeSeriesResponseLegacyTriples: length mismatch")
	}
	rows := make([]map[string]any, len(keys))
	for i, k := range keys {
		rows[i] = map[string]any{
			"region": k,
			"welch":  triples[i], // typed WelfordTriple value — legacy smuggle
		}
	}
	return &types.Response{Data: rows}
}

// TestApplyTCell_LegacyTripleNotConsumed asserts the MATRIX-arm Welch
// handler does NOT consume a typed WelfordTriple from MatrixCell.Value
// when Response.Components.Crosstab.CellComponents is absent. The
// resulting (0, 0) cell carries no finite p-value — the migration
// source is the universal Components surface and a stale typed-payload
// smuggle does not reach the math.
func TestApplyTCell_LegacyTripleNotConsumed(t *testing.T) {
	triple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeSingleCellLegacyTripleResponse(triple)
	ref := makeSingleCellLegacyTripleResponse(refTriple)
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, _, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	if layer.Payload.Matrix == nil {
		t.Fatal("applyTCell: nil matrix payload")
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if cell.Present {
		// The handler must not have consumed the smuggled triple. If
		// it had, a finite p-value would land in the cell.
		if v, ok := cell.Value.(float64); ok && !math.IsNaN(v) {
			t.Errorf("applyTCell consumed legacy WelfordTriple smuggle: cell value = %v (want absent)", v)
		}
	}
}

// TestApplyZCell_LegacyTripleNotConsumed is the OVERLAY_Z_CELL sibling
// regression gate — the MATRIX-arm two-sample-z handler MUST NOT
// consume a typed WelfordTriple from MatrixCell.Value when the
// components-source map is absent.
func TestApplyZCell_LegacyTripleNotConsumed(t *testing.T) {
	triple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeSingleCellLegacyTripleResponse(triple)
	ref := makeSingleCellLegacyTripleResponse(refTriple)
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layer, _, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if layer.Payload.Matrix == nil {
		t.Fatal("applyZCell: nil matrix payload")
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if cell.Present {
		if v, ok := cell.Value.(float64); ok && !math.IsNaN(v) {
			t.Errorf("applyZCell consumed legacy WelfordTriple smuggle: cell value = %v (want absent)", v)
		}
	}
}

// TestApplyTVsRef_LegacyTripleNotConsumed is the SERIES-arm Welch
// regression gate — the handler MUST NOT consume a typed WelfordTriple
// from the row's value column when the components-source map shape is
// absent. The series builds entries only for rows with a
// scalar-or-triple-map value; a typed WelfordTriple is neither, so the
// rows are silently dropped and no entries land.
func TestApplyTVsRef_LegacyTripleNotConsumed(t *testing.T) {
	triple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeSeriesResponseLegacyTriples([]string{"north", "south"}, []WelfordTriple{triple, triple})
	ref := makeSeriesResponseLegacyTriples([]string{"north", "south"}, []WelfordTriple{refTriple, refTriple})
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	layer, _, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	if layer.Payload.Series == nil {
		t.Fatal("applyTVsRef: nil series payload")
	}
	if len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("applyTVsRef consumed legacy WelfordTriple smuggle: %d entries emitted (want 0)", len(layer.Payload.Series.Entries))
	}
}

// TestApplyZVsRef_LegacyTripleNotConsumed is the OVERLAY_Z_VS_REF
// sibling regression gate — the SERIES-arm two-sample-z handler MUST
// NOT consume a typed WelfordTriple from the row's value column.
func TestApplyZVsRef_LegacyTripleNotConsumed(t *testing.T) {
	triple := WelfordTriple{Mean: 10, Variance: 4, N: 10}
	refTriple := WelfordTriple{Mean: 9, Variance: 4, N: 10}
	target := makeSeriesResponseLegacyTriples([]string{"north", "south"}, []WelfordTriple{triple, triple})
	ref := makeSeriesResponseLegacyTriples([]string{"north", "south"}, []WelfordTriple{refTriple, refTriple})
	spec := composeSpecSeriesRef(types.OverlayKindZVsRef, nil)
	layer, _, err := applyZVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZVsRef: %v", err)
	}
	if layer.Payload.Series == nil {
		t.Fatal("applyZVsRef: nil series payload")
	}
	if len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("applyZVsRef consumed legacy WelfordTriple smuggle: %d entries emitted (want 0)", len(layer.Payload.Series.Entries))
	}
}

// TestApplyTCell_ComponentsTripleSourcePrimary asserts the MATRIX-arm
// Welch handler reads `(mean, variance, n)` from the components-source
// map at Response.Components.Crosstab.CellComponents[r][c] — even when
// MatrixCell.Value carries a divergent legacy triple. The components
// source is canonical post-E3-S7; the legacy triple smuggle is not
// consulted.
//
// We populate CellComponents with one (mean, variance, n) tuple and
// MatrixCell.Value with a DIFFERENT WelfordTriple. The handler must
// emit a p-value derived from the components map, not from the
// stale typed smuggle.
func TestApplyTCell_ComponentsTripleSourcePrimary(t *testing.T) {
	// Components map carries the canonical (10, 4, 10) and (9, 4, 10)
	// — same shape the parity tests pin to a known answer.
	target := &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				// Legacy smuggle carries a wildly divergent triple to
				// catch any handler that still reaches for the typed
				// payload.
				Cells: [][]types.MatrixCell{{{Value: WelfordTriple{Mean: 99, Variance: 99, N: 99}, Present: true}}},
			},
		},
		Components: &types.ResponseComponents{
			Crosstab: &types.CrosstabComponents{
				CellComponents: [][]map[string]any{
					{{"n": int(10), "n_null": 0, "mean": 10.0, "variance": 4.0}},
				},
			},
		},
	}
	ref := &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        [][]types.MatrixCell{{{Value: WelfordTriple{Mean: 88, Variance: 88, N: 88}, Present: true}}},
			},
		},
		Components: &types.ResponseComponents{
			Crosstab: &types.CrosstabComponents{
				CellComponents: [][]map[string]any{
					{{"n": int(10), "n_null": 0, "mean": 9.0, "variance": 4.0}},
				},
			},
		},
	}
	spec := composeSpecMatrixRef(types.OverlayKindTCell, nil)
	layer, _, err := applyTCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTCell: %v", err)
	}
	if layer.Payload.Matrix == nil {
		t.Fatal("applyTCell: nil matrix payload")
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatal("applyTCell: cell absent — components source not consumed")
	}
	p, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("applyTCell: cell value not float64: %T", cell.Value)
	}
	// Components source: (10, 4, 10) vs (9, 4, 10) → p ≈ 0.2783.
	// Legacy smuggle: (99, 99, 99) vs (88, 88, 88) → very different p.
	approxEqual(t, "p-value (components source)", p, 0.2783, 1e-3)
}

// TestApplyZCell_ComponentsTripleSourcePrimary is the OVERLAY_Z_CELL
// sibling regression gate — confirms the components-source map is
// canonical, not MatrixCell.Value's typed smuggle.
func TestApplyZCell_ComponentsTripleSourcePrimary(t *testing.T) {
	target := &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        [][]types.MatrixCell{{{Value: WelfordTriple{Mean: 99, Variance: 99, N: 99}, Present: true}}},
			},
		},
		Components: &types.ResponseComponents{
			Crosstab: &types.CrosstabComponents{
				CellComponents: [][]map[string]any{
					{{"n": int(10), "n_null": 0, "mean": 10.0, "variance": 4.0}},
				},
			},
		},
	}
	ref := &types.Response{
		Crosstab: &types.CrosstabResult{
			Matrix: &types.MatrixPayload{
				RowHeader:    types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				ColumnHeader: types.AxisHeader{Types: []string{"GROUP_CATEGORY"}},
				RowKeys:      []types.AxisKey{{"r0"}},
				ColumnKeys:   []types.AxisKey{{"c0"}},
				Cells:        [][]types.MatrixCell{{{Value: WelfordTriple{Mean: 88, Variance: 88, N: 88}, Present: true}}},
			},
		},
		Components: &types.ResponseComponents{
			Crosstab: &types.CrosstabComponents{
				CellComponents: [][]map[string]any{
					{{"n": int(10), "n_null": 0, "mean": 9.0, "variance": 4.0}},
				},
			},
		},
	}
	spec := composeSpecMatrixRef(types.OverlayKindZCell, nil)
	layer, _, err := applyZCell(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyZCell: %v", err)
	}
	if layer.Payload.Matrix == nil {
		t.Fatal("applyZCell: nil matrix payload")
	}
	cell := layer.Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatal("applyZCell: cell absent — components source not consumed")
	}
	p, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("applyZCell: cell value not float64: %T", cell.Value)
	}
	// Components source: (10, 4, 10) vs (9, 4, 10) → z ≈ 1.118, p ≈ 0.2636.
	approxEqual(t, "p-value (components source)", p, 0.2636, 1e-3)
}
