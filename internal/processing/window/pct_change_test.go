package window

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestPctChange_Default(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 100.0},
		{"ts": 2.0, "x": 110.0},
		{"ts": 3.0, "x": 99.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_PCT_CHANGE, Field: "x", Label: "pct",
			OrderBy: []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["pct"] != nil {
		t.Errorf("rows[0].pct = %v, want nil", rows[0]["pct"])
	}
	got1 := rows[1]["pct"].(float64)
	want1 := 10.0 / 100.0
	if math.Abs(got1-want1) > 1e-9 {
		t.Errorf("rows[1].pct = %v, want %v", got1, want1)
	}
	got2 := rows[2]["pct"].(float64)
	want2 := -11.0 / 110.0
	if math.Abs(got2-want2) > 1e-9 {
		t.Errorf("rows[2].pct = %v, want %v", got2, want2)
	}
}

func TestPctChange_DenominatorZeroYieldsNil(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 0.0},
		{"ts": 2.0, "x": 5.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_PCT_CHANGE, Field: "x", Label: "pct",
			OrderBy: []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[1]["pct"] != nil {
		t.Errorf("denominator=0: pct = %v, want nil", rows[1]["pct"])
	}
}

func TestPctChange_Periods2(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 100.0},
		{"ts": 2.0, "x": 50.0},
		{"ts": 3.0, "x": 200.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_PCT_CHANGE, Field: "x", Label: "pct",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Params:  json.RawMessage(`{"periods": 2}`),
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["pct"] != nil || rows[1]["pct"] != nil {
		t.Errorf("expected nil for first 2 rows, got %v %v", rows[0]["pct"], rows[1]["pct"])
	}
	got := rows[2]["pct"].(float64)
	want := (200.0 - 100.0) / 100.0
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("rows[2].pct = %v, want %v", got, want)
	}
}
