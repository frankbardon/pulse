package window

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// EWMA with alpha=0.5: s_0 = x_0 = 10; s_1 = 0.5*20+0.5*10=15; s_2 = 0.5*30+0.5*15=22.5.
func TestEWMA_Recurrence(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 3.0, "x": 30.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_EWMA, Field: "x", Label: "e",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows"},
			Params:  json.RawMessage(`{"alpha": 0.5}`),
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []float64{10.0, 15.0, 22.5}
	for i, r := range rows {
		got := r["e"].(float64)
		if math.Abs(got-want[i]) > 1e-9 {
			t.Errorf("row %d: e = %v, want %v", i, got, want[i])
		}
	}
}

// alpha = 1.0 → identity (each row's EWMA is the row's value).
func TestEWMA_AlphaOneIsIdentity(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 7.0},
		{"ts": 2.0, "x": 13.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_EWMA, Field: "x", Label: "e",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows"},
			Params:  json.RawMessage(`{"alpha": 1}`),
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["e"] != 7.0 {
		t.Errorf("rows[0].e = %v, want 7.0", rows[0]["e"])
	}
	if rows[1]["e"] != 13.0 {
		t.Errorf("rows[1].e = %v, want 13.0", rows[1]["e"])
	}
}

func TestEWMA_LeadingNullsThenSeed(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": nil},
		{"ts": 2.0, "x": 100.0},
		{"ts": 3.0, "x": 200.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_EWMA, Field: "x", Label: "e",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows"},
			Params:  json.RawMessage(`{"alpha": 0.5}`),
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rows[0]["e"] != nil {
		t.Errorf("rows[0].e = %v, want nil (leading null)", rows[0]["e"])
	}
	if rows[1]["e"] != 100.0 {
		t.Errorf("rows[1].e (seed) = %v, want 100", rows[1]["e"])
	}
	got := rows[2]["e"].(float64)
	if math.Abs(got-150.0) > 1e-9 {
		t.Errorf("rows[2].e = %v, want 150", got)
	}
}

func TestEWMA_BadAlphaError(t *testing.T) {
	rows := []map[string]any{{"ts": 1.0, "x": 1.0}}
	w := []*types.Window{
		{
			Type: types.WIN_EWMA, Field: "x", Label: "e",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows"},
			Params:  json.RawMessage(`{"alpha": 2}`),
		},
	}
	err := Apply(context.Background(), rows, w)
	if err == nil {
		t.Fatal("expected error for alpha > 1")
	}
}
