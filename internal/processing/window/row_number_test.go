package window

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestRowNumber_Single(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0},
		{"ts": 2.0},
		{"ts": 3.0},
	}
	w := []*types.Window{
		{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "ts"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	expected := []int64{1, 2, 3}
	for i, r := range rows {
		if r["rn"] != expected[i] {
			t.Errorf("row %d: rn = %v (%T), want %d", i, r["rn"], r["rn"], expected[i])
		}
	}
}

func TestRowNumber_Partitioned(t *testing.T) {
	rows := []map[string]any{
		{"region": "us", "ts": 2.0},
		{"region": "us", "ts": 1.0},
		{"region": "eu", "ts": 2.0},
		{"region": "eu", "ts": 1.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_ROW_NUMBER, Label: "rn",
			PartitionBy: []string{"region"},
			OrderBy:     []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Each region: ts=1 → rn=1, ts=2 → rn=2.
	for _, r := range rows {
		ts := r["ts"].(float64)
		want := int64(1)
		if ts == 2.0 {
			want = 2
		}
		if r["rn"] != want {
			t.Errorf("region=%v ts=%v: rn = %v, want %d", r["region"], ts, r["rn"], want)
		}
	}
}
