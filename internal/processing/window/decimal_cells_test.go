package window

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// decimalCellStub is a decimal cell as the engine writes it onto a row
// (a decimal aggregate result, a record-row decimal): an opaque struct
// that orders by its value (SortValuer).
type decimalCellStub struct{ v ScaledDecimal }

func (c decimalCellStub) SortValue() any { return c.v }

// TestValueWindows_DecimalCellsReadAsFloat64 (#790): every window that
// reads a numeric field value (cellFloat) reads a decimal cell as its
// float64 value — the same figures, bit for bit, as over the float64
// column holding those values — and a decimal cell whose SortValue is
// null (an undefined decimal figure) as a null. Before, the struct read
// null on every row: WIN_SHARE, WIN_LIFT and the running windows
// returned null over a decimal aggregation, without a warning.
func TestValueWindows_DecimalCellsReadAsFloat64(t *testing.T) {
	t.Parallel()
	mantissas := []int64{1050, -325, 250, 10000, 0, 4775}
	const scale = 2
	rows := func(decimal bool) []map[string]any {
		out := make([]map[string]any, 0, len(mantissas)+1)
		for i, m := range mantissas {
			d := encoding.NewDecimal128FromInt(m)
			var x any = d.Float64(scale)
			if decimal {
				x = decimalCellStub{ScaledDecimal{Value: d, Scale: scale}}
			}
			out = append(out, map[string]any{"t": float64(i), "g": float64(i % 2), "x": x, "b": float64(i + 1)})
		}
		var undefined any // a null decimal figure
		if decimal {
			undefined = nilSortValue{}
		}
		return append(out, map[string]any{"t": float64(len(mantissas)), "g": 0.0, "x": undefined, "b": 2.0})
	}
	byT := []types.OrderKey{{Field: "t"}}
	part := []string{"g"}
	windows := []*types.Window{
		{Type: types.WIN_SHARE, Field: "x", Label: "share", PartitionBy: part},
		{Type: types.WIN_SHARE, Field: "x", Label: "cshare", OrderBy: byT, Params: json.RawMessage(`{"cumulative": true}`)},
		{Type: types.WIN_LIFT, Field: "x", Label: "lift", Params: json.RawMessage(`{"base": "b"}`)},
		{Type: types.WIN_RUNNING_SUM, Field: "x", Label: "rsum", OrderBy: byT, Frame: &types.FrameSpec{Mode: "rows", Following: ptrInt(0)}},
		{Type: types.WIN_RUNNING_AVG, Field: "x", Label: "ravg", OrderBy: byT, Frame: &types.FrameSpec{Mode: "rows", Following: ptrInt(0)}},
		{Type: types.WIN_MOVING_AVG, Field: "x", Label: "mavg", OrderBy: byT,
			Frame: &types.FrameSpec{Mode: "rows", Preceding: ptrInt(2), Following: ptrInt(0)}},
		{Type: types.WIN_DELTA, Field: "x", Label: "delta", OrderBy: byT},
		{Type: types.WIN_PCT_CHANGE, Field: "x", Label: "pct", OrderBy: byT},
		{Type: types.WIN_EWMA, Field: "x", Label: "ewma", OrderBy: byT, Frame: &types.FrameSpec{Mode: "rows"},
			Params: json.RawMessage(`{"alpha": 0.5}`)},
		{Type: types.WIN_DISTRIBUTED_LAG, Field: "x", Label: "dlag", OrderBy: byT,
			Params: json.RawMessage(`{"kernel": "geometric", "max_lag": 2, "decay": 0.5}`)},
		{Type: types.WIN_FORMULA, Label: "f", Params: json.RawMessage(`{"expression": "x * 2"}`)},
	}
	dec, flt := rows(true), rows(false)
	if err := Apply(context.Background(), dec, windows); err != nil {
		t.Fatalf("decimal Apply: %v", err)
	}
	if err := Apply(context.Background(), flt, windows); err != nil {
		t.Fatalf("float64 Apply: %v", err)
	}
	for _, w := range windows {
		defined := 0
		for i := range dec {
			d, f := dec[i][w.Label], flt[i][w.Label]
			if f == nil {
				if d != nil {
					t.Errorf("%s row %d: decimal %v, float64 null", w.Type, i, d)
				}
				continue
			}
			ff, fok := f.(float64)
			df, dok := d.(float64)
			if !fok || !dok || math.Float64bits(df) != math.Float64bits(ff) {
				t.Errorf("%s row %d: decimal %v (%T), float64 %v", w.Type, i, d, d, f)
				continue
			}
			defined++
		}
		if defined == 0 {
			t.Errorf("%s: no defined figure over the float64 column — the case proves nothing", w.Type)
		}
	}
}
