package window

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestLag_Default(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	rows := []map[string]any{}
	w := []*types.Window{
		{Type: types.WIN_LAG, Field: "x", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply on empty: %v", err)
	}
}

// TestLag_CopyObserver: ApplyObserved reports each WIN_LAG / WIN_LEAD
// write with its source row (nil out of partition), after the write,
// and the rows are written exactly as ApplyKeyed writes them; a
// non-copy window is never observed.
func TestLag_CopyObserver(t *testing.T) {
	t.Parallel()

	mk := func() []map[string]any {
		return []map[string]any{{"ts": 1.0, "x": 10.0}, {"ts": 2.0, "x": 20.0}, {"ts": 3.0, "x": 30.0}}
	}
	w := []*types.Window{
		{Type: types.WIN_LAG, Field: "x", Label: "lag", OrderBy: []types.OrderKey{{Field: "ts"}}},
		{Type: types.WIN_LEAD, Field: "x", Label: "lead", OrderBy: []types.OrderKey{{Field: "ts"}}},
		{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "ts"}}},
	}
	rows := mk()
	type seen struct {
		dst, src    float64 // ts of dst / src (0 = nil src)
		label, fld  string
		dstHasLabel bool
	}
	var got []seen
	obs := func(dst, src map[string]any, label, field string) {
		s := seen{dst: dst["ts"].(float64), label: label, fld: field}
		_, s.dstHasLabel = dst[label]
		if src != nil {
			s.src = src["ts"].(float64)
		}
		got = append(got, s)
	}
	if err := ApplyObserved(context.Background(), rows, w, nil, nil, obs); err != nil {
		t.Fatal(err)
	}
	want := []seen{
		{1, 0, "lag", "x", true}, {2, 1, "lag", "x", true}, {3, 2, "lag", "x", true},
		{1, 2, "lead", "x", true}, {2, 3, "lead", "x", true}, {3, 0, "lead", "x", true},
	}
	if len(got) != len(want) {
		t.Fatalf("observed %d copies, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("copy %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	plain := mk()
	if err := ApplyKeyed(context.Background(), plain, w, nil, nil); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(rows)
	b, _ := json.Marshal(plain)
	if string(a) != string(b) {
		t.Errorf("observed rows %s differ from unobserved %s", a, b)
	}
}
