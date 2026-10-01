package window

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestLag_Default(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 3.0, "x": 30.0},
	}
	w := []*types.Window{
		{Type: types.WIN_LAG, Field: "x", Label: "x_lag", OrderBy: []types.OrderKey{{Field: "ts"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["x_lag"] != nil {
		t.Errorf("rows[0].x_lag = %v, want nil", rows[0]["x_lag"])
	}
	if rows[1]["x_lag"] != 10.0 {
		t.Errorf("rows[1].x_lag = %v, want 10.0", rows[1]["x_lag"])
	}
	if rows[2]["x_lag"] != 20.0 {
		t.Errorf("rows[2].x_lag = %v, want 20.0", rows[2]["x_lag"])
	}
}

func TestLag_Offset2(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 3.0, "x": 30.0},
		{"ts": 4.0, "x": 40.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_LAG, Field: "x", Label: "x_lag2",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Params:  json.RawMessage(`{"offset": 2}`),
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["x_lag2"] != nil || rows[1]["x_lag2"] != nil {
		t.Errorf("rows 0,1 lag2 should be nil")
	}
	if rows[2]["x_lag2"] != 10.0 {
		t.Errorf("rows[2].x_lag2 = %v, want 10.0", rows[2]["x_lag2"])
	}
	if rows[3]["x_lag2"] != 20.0 {
		t.Errorf("rows[3].x_lag2 = %v, want 20.0", rows[3]["x_lag2"])
	}
}

func TestLag_Default_Value(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_LAG, Field: "x", Label: "x_lag",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Params:  json.RawMessage(`{"default": 0}`),
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["x_lag"] != float64(0) {
		t.Errorf("rows[0].x_lag = %v (%T), want 0", rows[0]["x_lag"], rows[0]["x_lag"])
	}
}

func TestLag_Partitioned(t *testing.T) {
	rows := []map[string]any{
		{"region": "us", "ts": 1.0, "x": 10.0},
		{"region": "us", "ts": 2.0, "x": 11.0},
		{"region": "eu", "ts": 1.0, "x": 50.0},
		{"region": "eu", "ts": 2.0, "x": 51.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_LAG, Field: "x", Label: "lag",
			PartitionBy: []string{"region"},
			OrderBy:     []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Find rows by (region, ts) — Apply does not reorder rows in place, only mutates them.
	for _, r := range rows {
		region := r["region"].(string)
		ts := r["ts"].(float64)
		switch {
		case region == "us" && ts == 1.0:
			if r["lag"] != nil {
				t.Errorf("us@1 lag = %v, want nil", r["lag"])
			}
		case region == "us" && ts == 2.0:
			if r["lag"] != 10.0 {
				t.Errorf("us@2 lag = %v, want 10", r["lag"])
			}
		case region == "eu" && ts == 1.0:
			if r["lag"] != nil {
				t.Errorf("eu@1 lag = %v, want nil", r["lag"])
			}
		case region == "eu" && ts == 2.0:
			if r["lag"] != 50.0 {
				t.Errorf("eu@2 lag = %v, want 50", r["lag"])
			}
		}
	}
}

func TestLead_Default(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 3.0, "x": 30.0},
	}
	w := []*types.Window{
		{Type: types.WIN_LEAD, Field: "x", Label: "lead", OrderBy: []types.OrderKey{{Field: "ts"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["lead"] != 20.0 {
		t.Errorf("rows[0].lead = %v, want 20.0", rows[0]["lead"])
	}
	if rows[1]["lead"] != 30.0 {
		t.Errorf("rows[1].lead = %v, want 30.0", rows[1]["lead"])
	}
	if rows[2]["lead"] != nil {
		t.Errorf("rows[2].lead = %v, want nil", rows[2]["lead"])
	}
}

func TestLag_EmptyPartitions(t *testing.T) {
	rows := []map[string]any{}
	w := []*types.Window{
		{Type: types.WIN_LAG, Field: "x", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply on empty: %v", err)
	}
}
