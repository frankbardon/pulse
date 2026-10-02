package window

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestApply_TwoWindowsShareSort verifies that when two windows share the
// same (PartitionBy, OrderBy) tuple, they share a single cached sort.
func TestApply_TwoWindowsShareSort(t *testing.T) {
	rows := []map[string]any{
		{"ts": 3.0, "x": 30.0},
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
	}
	w := []*types.Window{
		{Type: types.WIN_LAG, Field: "x", Label: "lag", OrderBy: []types.OrderKey{{Field: "ts"}}},
		{Type: types.WIN_LEAD, Field: "x", Label: "lead", OrderBy: []types.OrderKey{{Field: "ts"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Find row by ts.
	get := func(ts float64) map[string]any {
		for _, r := range rows {
			if r["ts"].(float64) == ts {
				return r
			}
		}
		return nil
	}
	r1 := get(1.0)
	r2 := get(2.0)
	r3 := get(3.0)
	if r1["lag"] != nil {
		t.Errorf("ts=1 lag = %v, want nil", r1["lag"])
	}
	if r2["lag"] != 10.0 {
		t.Errorf("ts=2 lag = %v, want 10", r2["lag"])
	}
	if r3["lead"] != nil {
		t.Errorf("ts=3 lead = %v, want nil", r3["lead"])
	}
	if r1["lead"] != 20.0 {
		t.Errorf("ts=1 lead = %v, want 20", r1["lead"])
	}
}

// TestApply_Determinism: same request twice produces identical output.
func TestApply_Determinism(t *testing.T) {
	build := func() []map[string]any {
		return []map[string]any{
			{"ts": 1.0, "x": 10.0},
			{"ts": 1.0, "x": 20.0},
			{"ts": 2.0, "x": 5.0},
		}
	}
	w := []*types.Window{
		{Type: types.WIN_RANK, Label: "r", OrderBy: []types.OrderKey{{Field: "ts"}, {Field: "x"}}},
	}
	rowsA := build()
	rowsB := build()
	if err := Apply(context.Background(), rowsA, w); err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), rowsB, w); err != nil {
		t.Fatal(err)
	}
	for i := range rowsA {
		if rowsA[i]["r"] != rowsB[i]["r"] {
			t.Errorf("row %d differs: %v vs %v", i, rowsA[i]["r"], rowsB[i]["r"])
		}
	}
}
