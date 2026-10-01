package window

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRank_WithGaps: values 10, 20, 20, 30 → ranks 1, 2, 2, 4.
func TestRank_WithGaps(t *testing.T) {
	rows := []map[string]any{
		{"x": 10.0},
		{"x": 20.0},
		{"x": 20.0},
		{"x": 30.0},
	}
	w := []*types.Window{
		{Type: types.WIN_RANK, Label: "r", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []int64{1, 2, 2, 4}
	for i, r := range rows {
		if r["r"] != want[i] {
			t.Errorf("row %d: rank = %v, want %d", i, r["r"], want[i])
		}
	}
}

// TestDenseRank_NoGaps: values 10, 20, 20, 30 → dense ranks 1, 2, 2, 3.
func TestDenseRank_NoGaps(t *testing.T) {
	rows := []map[string]any{
		{"x": 10.0},
		{"x": 20.0},
		{"x": 20.0},
		{"x": 30.0},
	}
	w := []*types.Window{
		{Type: types.WIN_DENSE_RANK, Label: "dr", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []int64{1, 2, 2, 3}
	for i, r := range rows {
		if r["dr"] != want[i] {
			t.Errorf("row %d: dr = %v, want %d", i, r["dr"], want[i])
		}
	}
}

func TestRank_Partitioned(t *testing.T) {
	rows := []map[string]any{
		{"region": "us", "x": 10.0},
		{"region": "us", "x": 20.0},
		{"region": "eu", "x": 5.0},
		{"region": "eu", "x": 5.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_RANK, Label: "r",
			PartitionBy: []string{"region"},
			OrderBy:     []types.OrderKey{{Field: "x"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// us: 10→1, 20→2; eu: tied 5,5 → both 1.
	for _, r := range rows {
		region := r["region"].(string)
		x := r["x"].(float64)
		var want int64
		switch {
		case region == "us" && x == 10.0:
			want = 1
		case region == "us" && x == 20.0:
			want = 2
		case region == "eu":
			want = 1
		}
		if r["r"] != want {
			t.Errorf("region=%s x=%v: rank = %v, want %d", region, x, r["r"], want)
		}
	}
}

func TestRank_Descending(t *testing.T) {
	rows := []map[string]any{
		{"x": 5.0},
		{"x": 10.0},
		{"x": 7.0},
	}
	w := []*types.Window{
		{Type: types.WIN_RANK, Label: "r", OrderBy: []types.OrderKey{{Field: "x", Desc: true}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, r := range rows {
		x := r["x"].(float64)
		var want int64
		switch x {
		case 10.0:
			want = 1
		case 7.0:
			want = 2
		case 5.0:
			want = 3
		}
		if r["r"] != want {
			t.Errorf("x=%v: rank = %v, want %d", x, r["r"], want)
		}
	}
}
