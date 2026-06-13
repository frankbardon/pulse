package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// nestedRowAxisPayload returns a 4 × 2 host MatrixPayload whose row
// axis is 2-deep ([brand, region]) and whose column axis is 1-deep
// ([segment]). The 4 leaf rows partition into 2 parent prefixes:
//
//	   c0   c1   | row_margin
//	A,N  1    2  |  3
//	A,S  3    4  |  7
//	B,N  5    6  | 11
//	B,S  7    8  | 15
//
// Row margins are the per-leaf-row sums. Parent-prefix-row sums are:
//
//	A: cells (A,N,*) + (A,S,*) = (1+2+3+4) = 10 → spans 4 cells
//	B: cells (B,N,*) + (B,S,*) = (5+6+7+8) = 26 → spans 4 cells
//
// Used by the E2-S11 Level / Within tests to drive nested-axis prefix-
// denominator dispatch.
func nestedRowAxisPayload() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader: types.AxisHeader{
			Fields: []string{"brand", "region"},
			Types:  []string{"GROUP_CATEGORY", "GROUP_CATEGORY"},
		},
		ColumnHeader: types.AxisHeader{
			Fields: []string{"segment"},
			Types:  []string{"GROUP_CATEGORY"},
		},
		RowKeys: []types.AxisKey{
			{"A", "N"},
			{"A", "S"},
			{"B", "N"},
			{"B", "S"},
		},
		ColumnKeys: []types.AxisKey{
			{"c0"},
			{"c1"},
		},
		Cells: [][]types.MatrixCell{
			{{Value: 1.0, Present: true}, {Value: 2.0, Present: true}},
			{{Value: 3.0, Present: true}, {Value: 4.0, Present: true}},
			{{Value: 5.0, Present: true}, {Value: 6.0, Present: true}},
			{{Value: 7.0, Present: true}, {Value: 8.0, Present: true}},
		},
		RowMargins: []types.MatrixCell{
			{Value: 3.0, Present: true},
			{Value: 7.0, Present: true},
			{Value: 11.0, Present: true},
			{Value: 15.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 16.0, Present: true},
			{Value: 20.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 36.0, Present: true},
		CellLabel:  "AGG_SUM_amount",
	}
}

// nestedColumnAxisPayload returns a 2 × 4 host MatrixPayload whose
// column axis is 2-deep ([segment, channel]) and whose row axis is
// 1-deep ([brand]). The 4 leaf columns partition into 2 parent prefixes:
//
//	          R,X  R,Y  W,X  W,Y  | row_margin
//	  A        1    2    3    4   |  10
//	  B        5    6    7    8   |  26
//	col_margin 6    8   10   12   |  36 (grand)
//
// Parent column prefixes:
//
//	R: cells (A,R,X)+(A,R,Y)+(B,R,X)+(B,R,Y) = (1+2+5+6) = 14
//	W: cells (A,W,X)+(A,W,Y)+(B,W,X)+(B,W,Y) = (3+4+7+8) = 22
//
// Within a fixed row + parent-column-prefix bucket:
//
//	  (A, R): (A,R,X)+(A,R,Y) = 3
//	  (A, W): (A,W,X)+(A,W,Y) = 7
//	  (B, R): (B,R,X)+(B,R,Y) = 11
//	  (B, W): (B,W,X)+(B,W,Y) = 15
//
// Used by the E2-S11 Within=1 test (SHARE_OF_ROW with Within fixing
// the parent column prefix produces row sums equal to 1.0 within each
// (leafRow, parentColPrefix) bucket).
func nestedColumnAxisPayload() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader: types.AxisHeader{
			Fields: []string{"brand"},
			Types:  []string{"GROUP_CATEGORY"},
		},
		ColumnHeader: types.AxisHeader{
			Fields: []string{"segment", "channel"},
			Types:  []string{"GROUP_CATEGORY", "GROUP_CATEGORY"},
		},
		RowKeys: []types.AxisKey{
			{"A"},
			{"B"},
		},
		ColumnKeys: []types.AxisKey{
			{"R", "X"},
			{"R", "Y"},
			{"W", "X"},
			{"W", "Y"},
		},
		Cells: [][]types.MatrixCell{
			{
				{Value: 1.0, Present: true}, {Value: 2.0, Present: true},
				{Value: 3.0, Present: true}, {Value: 4.0, Present: true},
			},
			{
				{Value: 5.0, Present: true}, {Value: 6.0, Present: true},
				{Value: 7.0, Present: true}, {Value: 8.0, Present: true},
			},
		},
		RowMargins: []types.MatrixCell{
			{Value: 10.0, Present: true},
			{Value: 26.0, Present: true},
		},
		ColumnMargins: []types.MatrixCell{
			{Value: 6.0, Present: true},
			{Value: 8.0, Present: true},
			{Value: 10.0, Present: true},
			{Value: 12.0, Present: true},
		},
		GrandTotal: types.MatrixCell{Value: 36.0, Present: true},
		CellLabel:  "AGG_SUM_amount",
	}
}

// TestOverlay_ShareOfRow_NestedAxisLevel1 — 2-deep nested row axis
// crosstab, OVERLAY_SHARE_OF_ROW{Level:1} produces cell-aligned shares
// whose row sums equal 1.0 within each parent-prefix-row bucket
// (sum of all cells under the same brand parent prefix). Level=1
// truncates the row axis to first (rowDepth - 1) = 1 grouper = brand
// only. Each cell's denominator = sum of cells under same brand
// prefix (across all leaf rows and all columns).
//
// Expected denominators (per E2-S11 prefix-bucket math):
//
//	cell (A,N,c0) = 1 ; denom = brand_A_sum = 1+2+3+4 = 10  → share = 0.1
//	cell (A,N,c1) = 2 ; denom = 10                          → share = 0.2
//	cell (A,S,c0) = 3 ; denom = 10                          → share = 0.3
//	cell (A,S,c1) = 4 ; denom = 10                          → share = 0.4
//	  sum of A-brand shares = 1.0 ✓
//
//	cell (B,N,c0) = 5 ; denom = brand_B_sum = 5+6+7+8 = 26  → share ≈ 0.1923
//	cell (B,N,c1) = 6 ; denom = 26                          → share ≈ 0.2308
//	cell (B,S,c0) = 7 ; denom = 26                          → share ≈ 0.2692
//	cell (B,S,c1) = 8 ; denom = 26                          → share ≈ 0.3077
//	  sum of B-brand shares = 1.0 ✓
func TestOverlay_ShareOfRow_NestedAxisLevel1(t *testing.T) {
	host := NewCrosstabHostView(nestedRowAxisPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
			Level: 1, // truncate row axis to parent prefix (brand only)
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	matrix := layers[0].Payload.Matrix
	if matrix == nil {
		t.Fatalf("matrix payload nil")
	}

	const eps = 1e-9
	// Expected shares per cell. Denominators are the brand-prefix sums
	// (10 for brand=A, 26 for brand=B).
	expected := [][]float64{
		{1.0 / 10.0, 2.0 / 10.0},
		{3.0 / 10.0, 4.0 / 10.0},
		{5.0 / 26.0, 6.0 / 26.0},
		{7.0 / 26.0, 8.0 / 26.0},
	}

	for i, row := range matrix.Cells {
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
		}
	}

	// Row sums within each brand-prefix bucket sum to 1.0.
	brandSums := map[string]float64{}
	for i, row := range matrix.Cells {
		brand := nestedRowAxisPayload().RowKeys[i][0].(string)
		for _, cell := range row {
			if cell.Present {
				v, _ := cell.Value.(float64)
				brandSums[brand] += v
			}
		}
	}
	for brand, sum := range brandSums {
		if math.Abs(sum-1.0) > eps {
			t.Fatalf("brand %q prefix-bucket share sum = %v, want 1.0", brand, sum)
		}
	}
}

// TestOverlay_ShareOfRow_Within1_ColumnAxis — 2-deep nested COLUMN axis
// crosstab + 1-deep row axis. OVERLAY_SHARE_OF_ROW{Within:1} fixes the
// column-axis prefix at first (colDepth - 1) = 1 grouper = segment
// only. Each cell's denominator = sum of cells in same row AND same
// segment parent prefix (folded across leaf-column grouper).
//
// Expected denominators (per E2-S11 prefix-bucket math) using
// nestedColumnAxisPayload:
//
//	cell (A, R, X) = 1 ; denom = (A,R,*) sum = 1+2 = 3  → share = 1/3
//	cell (A, R, Y) = 2 ; denom = 3                      → share = 2/3
//	  sum within (A, parent=R) = 1.0 ✓
//
//	cell (A, W, X) = 3 ; denom = (A,W,*) sum = 3+4 = 7  → share = 3/7
//	cell (A, W, Y) = 4 ; denom = 7                      → share = 4/7
//	  sum within (A, parent=W) = 1.0 ✓
//
//	cell (B, R, X) = 5 ; denom = 5+6 = 11               → share = 5/11
//	cell (B, R, Y) = 6 ; denom = 11                     → share = 6/11
//	cell (B, W, X) = 7 ; denom = 7+8 = 15               → share = 7/15
//	cell (B, W, Y) = 8 ; denom = 15                     → share = 8/15
func TestOverlay_ShareOfRow_Within1_ColumnAxis(t *testing.T) {
	host := NewCrosstabHostView(nestedColumnAxisPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindShareOfRow,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
			Within: 1, // fix column axis at parent-prefix depth
		},
	}
	layers, _, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	matrix := layers[0].Payload.Matrix
	if matrix == nil {
		t.Fatalf("matrix payload nil")
	}

	const eps = 1e-9
	expected := [][]float64{
		{1.0 / 3.0, 2.0 / 3.0, 3.0 / 7.0, 4.0 / 7.0},
		{5.0 / 11.0, 6.0 / 11.0, 7.0 / 15.0, 8.0 / 15.0},
	}

	for i, row := range matrix.Cells {
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
		}
	}

	// Per-bucket sum check: cells sharing (leafRow, parentColPrefix)
	// sum to 1.0.
	bucketSums := map[string]float64{}
	payload := nestedColumnAxisPayload()
	for i, row := range matrix.Cells {
		brand := payload.RowKeys[i][0].(string)
		for j, cell := range row {
			if !cell.Present {
				continue
			}
			parentSeg := payload.ColumnKeys[j][0].(string)
			v, _ := cell.Value.(float64)
			bucketSums[brand+"|"+parentSeg] += v
		}
	}
	for key, sum := range bucketSums {
		if math.Abs(sum-1.0) > eps {
			t.Fatalf("bucket %q share sum = %v, want 1.0", key, sum)
		}
	}
}

// TestOverlay_ShareOfRow_LevelZero_ByteIdenticalToBaseline — Level=0,
// Within=0 (the zero defaults) produces byte-equal output to a request
// without Level / Within at all. Regression guard for the default
// path: the E1 / E2-S1..S9 overlay-handler byte-identity contract
// must survive the slot addition.
//
// Comparison: marshal both response Overlays slices to JSON and assert
// byte equality. JSON equality is the strictest renderer-facing check
// because envelopes ship as JSON.
func TestOverlay_ShareOfRow_LevelZero_ByteIdenticalToBaseline(t *testing.T) {
	host := NewCrosstabHostView(synthIndexMarginPayload())

	baselineSpec := types.OverlaySpec{
		Kind:  types.OverlayKindShareOfRow,
		Scope: types.OverlayScopeCell,
		Ref: types.OverlayRef{
			Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
		},
	}
	zeroSpec := types.OverlaySpec{
		Kind:  types.OverlayKindShareOfRow,
		Scope: types.OverlayScopeCell,
		Ref: types.OverlayRef{
			Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
		},
		Level:  0,
		Within: 0,
	}

	baseLayers, _, err := ApplyOverlays([]types.OverlaySpec{baselineSpec}, host)
	if err != nil {
		t.Fatalf("baseline ApplyOverlays: %v", err)
	}
	zeroLayers, _, err := ApplyOverlays([]types.OverlaySpec{zeroSpec}, host)
	if err != nil {
		t.Fatalf("zero-default ApplyOverlays: %v", err)
	}

	baseBytes, err := json.Marshal(baseLayers)
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}
	zeroBytes, err := json.Marshal(zeroLayers)
	if err != nil {
		t.Fatalf("marshal zero-default: %v", err)
	}
	if string(baseBytes) != string(zeroBytes) {
		t.Fatalf("zero-default Level=0/Within=0 must be byte-identical to no-Level baseline\n got: %s\nwant: %s",
			string(zeroBytes), string(baseBytes))
	}
}
