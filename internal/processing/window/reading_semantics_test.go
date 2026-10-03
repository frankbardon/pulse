package window

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// The tests in this file pin the behaviours the WIN_* guidance
// (internal/descriptor/purposes_windows.go, interpretations_windows.go)
// and the op-win-* skills state as fact: null handling, tie handling,
// sign conventions and degenerate inputs. A change here must change that
// prose with it.

func applyOne(t *testing.T, rows []map[string]any, w *types.Window) {
	t.Helper()
	if err := Apply(context.Background(), rows, []*types.Window{w}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// WIN_PCT_CHANGE is a fraction of the earlier value (0.5 = 50%), and on a
// negative earlier value the sign follows (cur - prev) / prev: a rise from
// -10 to -5 reads -0.5.
func TestPctChange_FractionAndNegativeBase(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 100.0},
		{"ts": 2.0, "x": 150.0},
		{"ts": 3.0, "x": -10.0},
		{"ts": 4.0, "x": -5.0},
	}
	applyOne(t, rows, &types.Window{
		Type: types.WIN_PCT_CHANGE, Field: "x", Label: "p",
		OrderBy: []types.OrderKey{{Field: "ts"}},
	})
	if got := rows[1]["p"].(float64); math.Abs(got-0.5) > 1e-12 {
		t.Errorf("100 -> 150: pct = %v, want 0.5 (a fraction, not 50)", got)
	}
	if got := rows[3]["p"].(float64); math.Abs(got-(-0.5)) > 1e-12 {
		t.Errorf("-10 -> -5: pct = %v, want -0.5 (sign flips on a negative base)", got)
	}
}

// A missing value on either side nulls WIN_PCT_CHANGE and WIN_DELTA; a
// zero earlier value nulls only WIN_PCT_CHANGE.
func TestPctChangeDelta_NullAndZeroBase(t *testing.T) {
	for _, tc := range []struct {
		typ      types.WindowType
		wantZero any
	}{
		{types.WIN_PCT_CHANGE, nil},
		{types.WIN_DELTA, 5.0},
	} {
		rows := []map[string]any{
			{"ts": 1.0, "x": 0.0},
			{"ts": 2.0, "x": 5.0},
			{"ts": 3.0, "x": nil},
			{"ts": 4.0, "x": 7.0},
		}
		applyOne(t, rows, &types.Window{Type: tc.typ, Field: "x", Label: "v", OrderBy: []types.OrderKey{{Field: "ts"}}})
		if rows[1]["v"] != tc.wantZero {
			t.Errorf("%s: zero base = %v, want %v", tc.typ, rows[1]["v"], tc.wantZero)
		}
		if rows[2]["v"] != nil || rows[3]["v"] != nil {
			t.Errorf("%s: a null on either side = %v / %v, want nil / nil", tc.typ, rows[2]["v"], rows[3]["v"])
		}
	}
}

// A WIN_MOVING_AVG frame holding only missing values reads null (not NaN);
// otherwise the mean runs over the values present, so an edge window or a
// gap averages fewer rows.
func TestMovingAvg_AllNullFrameIsNullAndGapsShrinkTheMean(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": nil},
		{"ts": 2.0, "x": nil},
		{"ts": 3.0, "x": 30.0},
		{"ts": 4.0, "x": nil},
	}
	applyOne(t, rows, &types.Window{
		Type: types.WIN_MOVING_AVG, Field: "x", Label: "m",
		OrderBy: []types.OrderKey{{Field: "ts"}},
		Frame:   &types.FrameSpec{Mode: "rows", Preceding: ptrInt(1), Following: ptrInt(0)},
	})
	if rows[1]["m"] != nil {
		t.Errorf("all-null frame: m = %v, want nil", rows[1]["m"])
	}
	if rows[3]["m"] != 30.0 {
		t.Errorf("one value in frame: m = %v, want 30 (mean of the values present)", rows[3]["m"])
	}
}

// WIN_EWMA leaves a missing row null but carries the running state over
// it: the next value is blended with the state as if the gap were one step.
func TestEWMA_MidNullCarriesState(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 100.0},
		{"ts": 2.0, "x": nil},
		{"ts": 3.0, "x": 200.0},
	}
	applyOne(t, rows, &types.Window{
		Type: types.WIN_EWMA, Field: "x", Label: "e",
		OrderBy: []types.OrderKey{{Field: "ts"}},
		Frame:   &types.FrameSpec{Mode: "rows"},
		Params:  json.RawMessage(`{"alpha": 0.25}`),
	})
	if rows[1]["e"] != nil {
		t.Errorf("mid null: e = %v, want nil", rows[1]["e"])
	}
	if got := rows[2]["e"].(float64); math.Abs(got-125.0) > 1e-9 {
		t.Errorf("after the gap: e = %v, want 125 (0.25*200 + 0.75*100)", got)
	}
}

// Rows with a missing order_by value sort last and tie with each other,
// so they share the last rank; rank 1 is the first row in order_by order.
func TestRank_NullKeysShareTheLastRank(t *testing.T) {
	for _, tc := range []struct {
		typ  types.WindowType
		want map[any]int64
	}{
		{types.WIN_RANK, map[any]int64{3.0: 1, 7.0: 2, nil: 3}},
		{types.WIN_DENSE_RANK, map[any]int64{3.0: 1, 7.0: 2, nil: 3}},
	} {
		rows := []map[string]any{
			{"x": nil},
			{"x": 7.0},
			{"x": nil},
			{"x": 3.0},
		}
		applyOne(t, rows, &types.Window{Type: tc.typ, Label: "r", OrderBy: []types.OrderKey{{Field: "x"}}})
		for _, r := range rows {
			if r["r"] != tc.want[r["x"]] {
				t.Errorf("%s x=%v: rank = %v, want %d", tc.typ, r["x"], r["r"], tc.want[r["x"]])
			}
		}
	}
	// Under desc the values reverse but the nulls still trail.
	rows := []map[string]any{{"x": nil}, {"x": 7.0}, {"x": 3.0}}
	applyOne(t, rows, &types.Window{Type: types.WIN_RANK, Label: "r", OrderBy: []types.OrderKey{{Field: "x", Desc: true}}})
	want := map[any]int64{7.0: 1, 3.0: 2, nil: 3}
	for _, r := range rows {
		if r["r"] != want[r["x"]] {
			t.Errorf("desc x=%v: rank = %v, want %d", r["x"], r["r"], want[r["x"]])
		}
	}
}
