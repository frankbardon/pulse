package window

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestRunningSum_Cumulative(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 3.0, "x": 30.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_RUNNING_SUM, Field: "x", Label: "cum",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Following: ptrInt(0)}, // unbounded preceding to current row
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []float64{10.0, 30.0, 60.0}
	for i, r := range rows {
		if r["cum"] != want[i] {
			t.Errorf("row %d: cum = %v, want %v", i, r["cum"], want[i])
		}
	}
}

func TestRunningSum_NullsSkipped(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": nil},
		{"ts": 3.0, "x": 30.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_RUNNING_SUM, Field: "x", Label: "cum",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Following: ptrInt(0)},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []float64{10.0, 10.0, 40.0}
	for i, r := range rows {
		if r["cum"] != want[i] {
			t.Errorf("row %d: cum = %v, want %v", i, r["cum"], want[i])
		}
	}
}

func TestRunningSum_AllNullFrameYieldsNil(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": nil},
		{"ts": 2.0, "x": nil},
	}
	w := []*types.Window{
		{
			Type: types.WIN_RUNNING_SUM, Field: "x", Label: "cum",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Following: ptrInt(0)},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for i, r := range rows {
		if r["cum"] != nil {
			t.Errorf("row %d: cum = %v, want nil", i, r["cum"])
		}
	}
}
