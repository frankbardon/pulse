package window

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// 3-row trailing window (preceding=2, following=0).
func TestMovingAvg_Trailing3(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 3.0, "x": 30.0},
		{"ts": 4.0, "x": 40.0},
		{"ts": 5.0, "x": 50.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_MOVING_AVG, Field: "x", Label: "ma3",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Preceding: ptrInt(2), Following: ptrInt(0)},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// row 0: only [10] → 10
	// row 1: [10,20] → 15
	// row 2: [10,20,30] → 20
	// row 3: [20,30,40] → 30
	// row 4: [30,40,50] → 40
	want := []float64{10.0, 15.0, 20.0, 30.0, 40.0}
	for i, r := range rows {
		got := r["ma3"].(float64)
		if math.Abs(got-want[i]) > 1e-9 {
			t.Errorf("row %d: ma3 = %v, want %v", i, got, want[i])
		}
	}
}
