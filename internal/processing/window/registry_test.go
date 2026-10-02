package window

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestApply_NoWindowsNoOp(t *testing.T) {
	rows := []map[string]any{{"x": 1.0}, {"x": 2.0}}
	if err := Apply(context.Background(), rows, nil); err != nil {
		t.Fatalf("Apply with no windows returned error: %v", err)
	}
	if len(rows[0]) != 1 || len(rows[1]) != 1 {
		t.Errorf("rows mutated unexpectedly: %+v", rows)
	}
}

func TestApply_EmptyRowsNoOp(t *testing.T) {
	rows := []map[string]any{}
	w := []*types.Window{
		{Type: types.WIN_LAG, Field: "x", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply with empty rows returned error: %v", err)
	}
}

func TestApply_UnknownTypeError(t *testing.T) {
	rows := []map[string]any{{"x": 1.0}}
	w := []*types.Window{
		{Type: types.WindowType("WIN_NOPE"), Field: "x", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	err := Apply(context.Background(), rows, w)
	if err == nil {
		t.Fatal("expected error for unknown window type")
	}
}

func TestWindowLabel(t *testing.T) {
	cases := []struct {
		name string
		w    *types.Window
		want string
	}{
		{"explicit label", &types.Window{Type: types.WIN_LAG, Field: "rev", Label: "lag1"}, "lag1"},
		{"with field", &types.Window{Type: types.WIN_LAG, Field: "rev"}, "WIN_LAG_rev"},
		{"no field", &types.Window{Type: types.WIN_RANK}, "WIN_RANK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowLabel(tc.w)
			if got != tc.want {
				t.Errorf("windowLabel = %q, want %q", got, tc.want)
			}
		})
	}
}
