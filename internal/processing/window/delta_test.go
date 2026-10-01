package window

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// deltaTolerance is the f64 comparison bar for point differences. Subtraction
// of two decimal literals is not exact in binary floating point (97.9 - 90.0
// is 7.900000000000006), so the tests compare within tolerance rather than
// with ==.
const deltaTolerance = 1e-9

// wantCell is an expected output cell: either nil, or a float within tolerance.
type wantCell struct {
	null  bool
	value float64
}

func nullCell() wantCell         { return wantCell{null: true} }
func valCell(v float64) wantCell { return wantCell{value: v} }

func checkDeltaCells(t *testing.T, rows []map[string]any, label string, want []wantCell) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("row count = %d, want %d", len(rows), len(want))
	}
	for i, w := range want {
		got := rows[i][label]
		if w.null {
			if got != nil {
				t.Errorf("rows[%d][%q] = %v, want nil", i, label, got)
			}
			continue
		}
		f, ok := got.(float64)
		if !ok {
			t.Errorf("rows[%d][%q] = %v (%T), want float64 %v", i, label, got, got, w.value)
			continue
		}
		if math.Abs(f-w.value) > deltaTolerance {
			t.Errorf("rows[%d][%q] = %v, want %v", i, label, f, w.value)
		}
	}
}

// TestDelta_Compute is the table-driven body covering the six acceptance cases
// from the requester plus the empty / single-row / null-bearing / order-sensitive
// cases the contributor guide requires.
func TestDelta_Compute(t *testing.T) {
	cases := []struct {
		name        string
		rows        []map[string]any
		partitionBy []string
		orderBy     []types.OrderKey
		params      json.RawMessage
		want        []wantCell
	}{
		{
			// Acceptance 1: point difference, not ratio. A ratio would be
			// 7.9/90 = 0.0878; the delta is 7.9.
			name: "point difference not ratio",
			rows: []map[string]any{
				{"ts": 1.0, "x": 90.0},
				{"ts": 2.0, "x": 97.9},
			},
			orderBy: []types.OrderKey{{Field: "ts"}},
			want:    []wantCell{nullCell(), valCell(7.9)},
		},
		{
			// Acceptance 2: THE divergence from WIN_PCT_CHANGE. A zero prior
			// is a legitimate delta and must NOT be nulled.
			name: "zero prior is a real delta",
			rows: []map[string]any{
				{"ts": 1.0, "x": 0.0},
				{"ts": 2.0, "x": 12.5},
			},
			orderBy: []types.OrderKey{{Field: "ts"}},
			want:    []wantCell{nullCell(), valCell(12.5)},
		},
		{
			// Acceptance 3: negative deltas survive — no abs, no clamp.
			name: "negative delta survives",
			rows: []map[string]any{
				{"ts": 1.0, "x": 97.9},
				{"ts": 2.0, "x": 90.0},
			},
			orderBy: []types.OrderKey{{Field: "ts"}},
			want:    []wantCell{nullCell(), valCell(-7.9)},
		},
		{
			// Acceptance 4: partition isolation. The first row of partition B
			// must be null, NOT 50-20=30 against the tail of partition A.
			name: "partition isolation",
			rows: []map[string]any{
				{"g": "a", "ts": 1.0, "x": 10.0},
				{"g": "a", "ts": 2.0, "x": 20.0},
				{"g": "b", "ts": 1.0, "x": 50.0},
				{"g": "b", "ts": 2.0, "x": 55.0},
			},
			partitionBy: []string{"g"},
			orderBy:     []types.OrderKey{{Field: "ts"}},
			want: []wantCell{
				nullCell(), valCell(10.0),
				nullCell(), valCell(5.0),
			},
		},
		{
			// Acceptance 5: periods=2 compares two rows back; the first two
			// rows of EACH partition are null.
			name: "periods 2 across two partitions",
			rows: []map[string]any{
				{"g": "a", "ts": 1.0, "x": 100.0},
				{"g": "a", "ts": 2.0, "x": 50.0},
				{"g": "a", "ts": 3.0, "x": 200.0},
				{"g": "b", "ts": 1.0, "x": 1.0},
				{"g": "b", "ts": 2.0, "x": 2.0},
				{"g": "b", "ts": 3.0, "x": 4.0},
			},
			partitionBy: []string{"g"},
			orderBy:     []types.OrderKey{{Field: "ts"}},
			params:      json.RawMessage(`{"periods": 2}`),
			want: []wantCell{
				nullCell(), nullCell(), valCell(100.0),
				nullCell(), nullCell(), valCell(3.0),
			},
		},
		{
			// Contributor guide: single-row partition emits one null and no error.
			name: "single row partition",
			rows: []map[string]any{
				{"ts": 1.0, "x": 42.0},
			},
			orderBy: []types.OrderKey{{Field: "ts"}},
			want:    []wantCell{nullCell()},
		},
		{
			// Contributor guide: null-bearing input. A missing current OR prior
			// value nulls the row; the row AFTER the hole is also null because
			// its prior is the hole.
			name: "null bearing values",
			rows: []map[string]any{
				{"ts": 1.0, "x": 10.0},
				{"ts": 2.0, "x": nil},
				{"ts": 3.0, "x": 30.0},
				{"ts": 4.0, "x": 45.0},
			},
			orderBy: []types.OrderKey{{Field: "ts"}},
			want:    []wantCell{nullCell(), nullCell(), nullCell(), valCell(15.0)},
		},
		{
			// Contributor guide: a field absent from the row map behaves the
			// same as an explicit null.
			name: "missing field key",
			rows: []map[string]any{
				{"ts": 1.0, "x": 10.0},
				{"ts": 2.0},
				{"ts": 3.0, "x": 30.0},
			},
			orderBy: []types.OrderKey{{Field: "ts"}},
			want:    []wantCell{nullCell(), nullCell(), nullCell()},
		},
		{
			// Contributor guide: order-sensitive. Rows arrive out of order and
			// descending; the operator must difference in ORDER BY order, and
			// the output lands back on the originating row.
			name: "order sensitive descending",
			rows: []map[string]any{
				// input order is ts 2, 3, 1 — evaluation order is ts 3, 2, 1
				{"ts": 2.0, "x": 20.0},
				{"ts": 3.0, "x": 35.0},
				{"ts": 1.0, "x": 10.0},
			},
			orderBy: []types.OrderKey{{Field: "ts", Desc: true}},
			want: []wantCell{
				valCell(-15.0), // ts=2: 20 - 35
				nullCell(),     // ts=3: first in descending order
				valCell(-10.0), // ts=1: 10 - 20
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := []*types.Window{
				{
					Type:        types.WIN_DELTA,
					Field:       "x",
					Label:       "d",
					PartitionBy: tc.partitionBy,
					OrderBy:     tc.orderBy,
					Params:      tc.params,
				},
			}
			if err := Apply(context.Background(), tc.rows, w); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			checkDeltaCells(t, tc.rows, "d", tc.want)
		})
	}
}

// TestDelta_ZeroPriorIsNotNulled is acceptance case 2 stated on its own so the
// falsification run (re-adding `|| prev == 0` to delta.go) has a single test to
// point at. WIN_PCT_CHANGE nulls a zero prior because the division is
// undefined; subtraction has no such case.
func TestDelta_ZeroPriorIsNotNulled(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 0.0},
		{"ts": 2.0, "x": 12.5},
	}
	w := []*types.Window{
		{
			Type: types.WIN_DELTA, Field: "x", Label: "d",
			OrderBy: []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := rows[1]["d"].(float64)
	if !ok {
		t.Fatalf("zero prior: rows[1].d = %v (%T), want float64 12.5 — a zero prior is a legitimate delta, not a null", rows[1]["d"], rows[1]["d"])
	}
	if math.Abs(got-12.5) > deltaTolerance {
		t.Errorf("zero prior: rows[1].d = %v, want 12.5", got)
	}
}

// TestDelta_IsNotAPctChange pins the arithmetic against the operator it mirrors:
// the same inputs must produce the difference, never the ratio.
func TestDelta_IsNotAPctChange(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 90.0},
		{"ts": 2.0, "x": 97.9},
	}
	w := []*types.Window{
		{
			Type: types.WIN_DELTA, Field: "x", Label: "d",
			OrderBy: []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := rows[1]["d"].(float64)
	if math.Abs(got-7.9) > deltaTolerance {
		t.Errorf("rows[1].d = %v, want 7.9", got)
	}
	ratio := (97.9 - 90.0) / 90.0
	if math.Abs(got-ratio) <= deltaTolerance {
		t.Errorf("rows[1].d = %v is the pct_change ratio %v, not the point difference", got, ratio)
	}
}

// TestDelta_FrameRejected is acceptance case 6: WIN_DELTA has no frame, so a
// Frame on the window is a configuration error.
func TestDelta_FrameRejected(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_DELTA, Field: "x", Label: "d",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Preceding: ptrInt(1), Following: ptrInt(0)},
		},
	}
	err := Apply(context.Background(), rows, w)
	if err == nil {
		t.Fatal("Apply with a frame: got nil error, want PROCESSING_CONFIG")
	}
	assertProcessingConfig(t, err)
}

// TestDelta_PeriodsValidation covers the params contract: default 1, malformed
// JSON and periods < 1 are configuration errors.
func TestDelta_PeriodsValidation(t *testing.T) {
	cases := []struct {
		name    string
		params  json.RawMessage
		wantErr bool
	}{
		{"no params defaults to 1", nil, false},
		{"explicit 1", json.RawMessage(`{"periods": 1}`), false},
		{"explicit 3", json.RawMessage(`{"periods": 3}`), false},
		{"zero rejected", json.RawMessage(`{"periods": 0}`), true},
		{"negative rejected", json.RawMessage(`{"periods": -2}`), true},
		{"malformed rejected", json.RawMessage(`{"periods": "two"}`), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := []map[string]any{
				{"ts": 1.0, "x": 10.0},
				{"ts": 2.0, "x": 20.0},
			}
			w := []*types.Window{
				{
					Type: types.WIN_DELTA, Field: "x", Label: "d",
					OrderBy: []types.OrderKey{{Field: "ts"}},
					Params:  tc.params,
				},
			}
			err := Apply(context.Background(), rows, w)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("params %s: got nil error, want PROCESSING_CONFIG", tc.params)
				}
				assertProcessingConfig(t, err)
				return
			}
			if err != nil {
				t.Fatalf("params %s: %v", tc.params, err)
			}
		})
	}
}

// TestDelta_EmptyRows covers the contributor guide's empty case: no rows means
// no work and no error.
func TestDelta_EmptyRows(t *testing.T) {
	var rows []map[string]any
	w := []*types.Window{
		{
			Type: types.WIN_DELTA, Field: "x", Label: "d",
			OrderBy: []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply over zero rows: %v", err)
	}
}

// TestDelta_Registered asserts the init() registration landed under the
// constant, not a copy-pasted neighbour's.
func TestDelta_Registered(t *testing.T) {
	if _, ok := Lookup(types.WIN_DELTA); !ok {
		t.Fatal("WIN_DELTA is not registered in the window registry")
	}
}

// TestDelta_DefaultLabel pins the output column name when Label is empty.
func TestDelta_DefaultLabel(t *testing.T) {
	rows := []map[string]any{
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 25.0},
	}
	w := []*types.Window{
		{
			Type: types.WIN_DELTA, Field: "x",
			OrderBy: []types.OrderKey{{Field: "ts"}},
		},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := rows[1]["WIN_DELTA_x"].(float64)
	if !ok {
		t.Fatalf("rows[1][WIN_DELTA_x] = %v (%T), want float64", rows[1]["WIN_DELTA_x"], rows[1]["WIN_DELTA_x"])
	}
	if math.Abs(got-15.0) > deltaTolerance {
		t.Errorf("rows[1][WIN_DELTA_x] = %v, want 15", got)
	}
}

func assertProcessingConfig(t *testing.T, err error) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v (%T) is not a *errors.CodedError", err, err)
	}
	if ce.Code != errors.PROCESSING_CONFIG {
		t.Errorf("error code = %s, want %s", ce.Code, errors.PROCESSING_CONFIG)
	}
}
