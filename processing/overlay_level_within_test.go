package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

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
