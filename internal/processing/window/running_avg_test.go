package window

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestRunningAvg_Cumulative(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 3.0, "x": 30.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_RUNNING_AVG, Field: "x", Label: "avg",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Following: ptrInt(0)},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// 10 / 1 = 10; (10+20)/2 = 15; (10+20+30)/3 = 20.
	want := []float64{10.0, 15.0, 20.0}
	for i, r := range rows {
		if r["avg"] != want[i] {
			t.Errorf("row %d: avg = %v, want %v", i, r["avg"], want[i])
		}
	}
}
