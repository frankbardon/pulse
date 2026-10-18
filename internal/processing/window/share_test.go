package window

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// lift is (x / sx) / (b / sb) in float64 arithmetic, each step rounded —
// never an exact untyped-constant expression.
func lift(x, sx, b, sb float64) float64 {
	fx, fb := x/sx, b/sb
	return fx / fb
}

func shareApply(t *testing.T, rows []map[string]any, w *types.Window) {
	t.Helper()
	if err := Apply(context.Background(), rows, []*types.Window{w}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func wantCells(t *testing.T, rows []map[string]any, label string, want []any) {
	t.Helper()
	for i, r := range rows {
		got := r[label]
		if want[i] == nil {
			if got != nil {
				t.Errorf("row %d: %s = %v, want null", i, label, got)
			}
			continue
		}
		g, ok := got.(float64)
		if !ok || math.Float64bits(g) != math.Float64bits(want[i].(float64)) {
			t.Errorf("row %d: %s = %v, want %v", i, label, got, want[i])
		}
	}
}

// Non-cumulative: x / Σx per partition, no order_by needed; the hand
// computation divides in the same order the kernel does.
func TestShare_RowShareOfPartitionTotal(t *testing.T) {
	rows := []map[string]any{
		{"g": "a", "x": 10.0},
		{"g": "a", "x": 30.0},
		{"g": "b", "x": 5.0},
		{"g": "a", "x": 60.0},
		{"g": "b", "x": 15.0},
	}
	shareApply(t, rows, &types.Window{Type: types.WIN_SHARE, Field: "x", Label: "s", PartitionBy: []string{"g"}})
	wantCells(t, rows, "s", []any{10.0 / 100, 30.0 / 100, 5.0 / 20, 60.0 / 100, 15.0 / 20})
}

// Cumulative: running Σx in order_by order over Σx; rows are not
// reordered and the last share of each partition is exactly 1.
func TestShare_Cumulative(t *testing.T) {
	rows := []map[string]any{
		{"ts": 3.0, "x": 30.0},
		{"ts": 1.0, "x": 10.0},
		{"ts": 2.0, "x": 20.0},
		{"ts": 4.0, "x": 40.0},
	}
	shareApply(t, rows, &types.Window{
		Type: types.WIN_SHARE, Field: "x", Label: "cs",
		OrderBy: []types.OrderKey{{Field: "ts"}}, Params: json.RawMessage(`{"cumulative": true}`),
	})
	wantCells(t, rows, "cs", []any{60.0 / 100, 10.0 / 100, 30.0 / 100, 1.0})

	// cumulative false is the default, spelled out.
	rows2 := []map[string]any{{"ts": 1.0, "x": 1.0}, {"ts": 2.0, "x": 3.0}}
	shareApply(t, rows2, &types.Window{
		Type: types.WIN_SHARE, Field: "x", Label: "s",
		OrderBy: []types.OrderKey{{Field: "ts"}}, Params: json.RawMessage(`{"cumulative": false}`),
	})
	wantCells(t, rows2, "s", []any{0.25, 0.75})
}

// Nulls add nothing: the own-row share is null, the cumulative share
// carries the running figure (null before the first present value).
func TestShare_NullsSkipped(t *testing.T) {
	mk := func() []map[string]any {
		return []map[string]any{
			{"ts": 1.0, "x": nil},
			{"ts": 2.0, "x": 25.0},
			{"ts": 3.0},
			{"ts": 4.0, "x": math.NaN()},
			{"ts": 5.0, "x": 75.0},
		}
	}
	rows := mk()
	shareApply(t, rows, &types.Window{Type: types.WIN_SHARE, Field: "x", Label: "s", OrderBy: []types.OrderKey{{Field: "ts"}}})
	wantCells(t, rows, "s", []any{nil, 0.25, nil, nil, 0.75})

	rows = mk()
	shareApply(t, rows, &types.Window{
		Type: types.WIN_SHARE, Field: "x", Label: "cs",
		OrderBy: []types.OrderKey{{Field: "ts"}}, Params: json.RawMessage(`{"cumulative": true}`),
	})
	wantCells(t, rows, "cs", []any{nil, 0.25, 0.25, 0.25, 1.0})
}

// A zero total (all zeros, or values cancelling) and an all-null
// partition read null on every row — never 0, never NaN / Inf.
func TestShare_ZeroTotalIsNull(t *testing.T) {
	for name, xs := range map[string][]any{
		"zeros":      {0.0, 0.0, 0.0},
		"cancelling": {5.0, -5.0, 0.0},
		"all null":   {nil, nil, nil},
	} {
		for _, cum := range []string{`{"cumulative": false}`, `{"cumulative": true}`} {
			rows := make([]map[string]any, len(xs))
			for i, x := range xs {
				rows[i] = map[string]any{"ts": float64(i), "x": x}
			}
			shareApply(t, rows, &types.Window{
				Type: types.WIN_SHARE, Field: "x", Label: "s",
				OrderBy: []types.OrderKey{{Field: "ts"}}, Params: json.RawMessage(cum),
			})
			for i, r := range rows {
				if v, present := r["s"]; !present || v != nil {
					t.Errorf("%s %s row %d: s = %v (present %v), want null", name, cum, i, v, present)
				}
			}
		}
	}
}

// WIN_LIFT is WIN_SHARE(field) / WIN_SHARE(base), bit for bit, on both
// forms; a zero base share and a missing side read null.
func TestLift_IsShareOverShare(t *testing.T) {
	mk := func() []map[string]any {
		return []map[string]any{
			{"g": "a", "ts": 1.0, "d": 7.0, "n": 10.0},
			{"g": "a", "ts": 2.0, "d": 2.0, "n": 0.0},
			{"g": "a", "ts": 3.0, "d": nil, "n": 30.0},
			{"g": "a", "ts": 4.0, "d": 1.0, "n": 60.0},
			{"g": "b", "ts": 1.0, "d": 3.0, "n": 1.0},
			{"g": "b", "ts": 2.0, "d": 3.0, "n": nil},
		}
	}
	for _, cum := range []bool{false, true} {
		params := `{"cumulative": false}`
		if cum {
			params = `{"cumulative": true}`
		}
		rows := mk()
		ws := []*types.Window{
			{Type: types.WIN_SHARE, Field: "d", Label: "sd", PartitionBy: []string{"g"}, OrderBy: []types.OrderKey{{Field: "ts"}}, Params: json.RawMessage(params)},
			{Type: types.WIN_SHARE, Field: "n", Label: "sn", PartitionBy: []string{"g"}, OrderBy: []types.OrderKey{{Field: "ts"}}, Params: json.RawMessage(params)},
			{Type: types.WIN_LIFT, Field: "d", Label: "lift", PartitionBy: []string{"g"}, OrderBy: []types.OrderKey{{Field: "ts"}},
				Params: json.RawMessage(strings.Replace(params, "}", `, "base": "n"}`, 1))},
		}
		if err := Apply(context.Background(), rows, ws); err != nil {
			t.Fatal(err)
		}
		for i, r := range rows {
			sd, okd := r["sd"].(float64)
			sn, okn := r["sn"].(float64)
			if !okd || !okn || sn == 0 {
				if r["lift"] != nil {
					t.Errorf("cum=%v row %d: lift = %v, want null (sd %v, sn %v)", cum, i, r["lift"], r["sd"], r["sn"])
				}
				continue
			}
			if got, ok := r["lift"].(float64); !ok || math.Float64bits(got) != math.Float64bits(sd/sn) {
				t.Errorf("cum=%v row %d: lift = %v, want %v", cum, i, r["lift"], sd/sn)
			}
		}
		// Spot values, by hand: partition a has Σd = 10, Σn = 100.
		if cum {
			wantCells(t, rows[:4], "lift", []any{lift(7, 10, 10, 100), lift(9, 10, 10, 100), lift(9, 10, 40, 100), 1.0})
		} else {
			wantCells(t, rows[:4], "lift", []any{lift(7, 10, 10, 100), nil, nil, lift(1, 10, 60, 100)})
		}
	}
}

// The casebook step 13 shape on decile rows: ordered by mean score
// descending, the cumulative share of defaults, of loans and the
// cumulative lift — the second row reads 978/2149 ÷ 3600/18000.
func TestShare_DecileGainsTable(t *testing.T) {
	defaults := []float64{610, 368, 308, 261, 176, 142, 123, 92, 46, 23}
	var rows []map[string]any
	for i := len(defaults) - 1; i >= 0; i-- { // input in ascending risk
		rows = append(rows, map[string]any{"mean_pd": 1 - float64(i)/10, "defaults": defaults[i], "loans": 1800.0})
	}
	order := []types.OrderKey{{Field: "mean_pd", Desc: true}}
	ws := []*types.Window{
		{Type: types.WIN_SHARE, Field: "defaults", Label: "cum_share_defaults", OrderBy: order, Params: json.RawMessage(`{"cumulative": true}`)},
		{Type: types.WIN_SHARE, Field: "loans", Label: "cum_share_loans", OrderBy: order, Params: json.RawMessage(`{"cumulative": true}`)},
		{Type: types.WIN_LIFT, Field: "defaults", Label: "cum_lift", OrderBy: order, Params: json.RawMessage(`{"cumulative": true, "base": "loans"}`)},
	}
	if err := Apply(context.Background(), rows, ws); err != nil {
		t.Fatal(err)
	}
	d9 := rows[len(rows)-2] // the second-riskiest decile
	if d9["cum_share_defaults"] != 978.0/2149 || d9["cum_share_loans"] != 3600.0/18000 {
		t.Fatalf("D9 shares = %v, %v", d9["cum_share_defaults"], d9["cum_share_loans"])
	}
	if want := lift(978, 2149, 3600, 18000); d9["cum_lift"] != want {
		t.Fatalf("D9 cumulative lift = %v, want %v", d9["cum_lift"], want)
	}
	last := rows[0] // the lowest-risk decile closes every curve at 1
	if last["cum_share_defaults"] != 1.0 || last["cum_share_loans"] != 1.0 || last["cum_lift"] != 1.0 {
		t.Fatalf("closing row = %v", last)
	}
}

// Every factory refusal is PULSE_WINDOW_INVALID with the rule sentence.
func TestShare_FactoryRefusals(t *testing.T) {
	order := []types.OrderKey{{Field: "ts"}}
	cases := []struct {
		name string
		w    types.Window
		want string
	}{
		{"share frame", types.Window{Type: types.WIN_SHARE, Field: "x", OrderBy: order, Frame: &types.FrameSpec{Mode: "rows"}}, "WIN_SHARE: frame is not allowed"},
		{"share cumulative no order", types.Window{Type: types.WIN_SHARE, Field: "x", Params: json.RawMessage(`{"cumulative": true}`)}, "WIN_SHARE: order_by is required when params.cumulative is true"},
		{"share no field", types.Window{Type: types.WIN_SHARE}, "WIN_SHARE: field is required"},
		{"share malformed", types.Window{Type: types.WIN_SHARE, Field: "x", Params: json.RawMessage(`{"cumulative": "yes"}`)}, "WIN_SHARE: malformed params"},
		{"lift no base", types.Window{Type: types.WIN_LIFT, Field: "x"}, "WIN_LIFT: params.base is required"},
		{"lift frame", types.Window{Type: types.WIN_LIFT, Field: "x", Params: json.RawMessage(`{"base": "b"}`), Frame: &types.FrameSpec{Mode: "rows"}}, "WIN_LIFT: frame is not allowed"},
		{"lift cumulative no order", types.Window{Type: types.WIN_LIFT, Field: "x", Params: json.RawMessage(`{"base": "b", "cumulative": true}`)}, "WIN_LIFT: order_by is required"},
	}
	for _, tc := range cases {
		f, _ := Lookup(tc.w.Type)
		_, err := f(&tc.w, WindowOptions{})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WINDOW_INVALID || !strings.HasPrefix(ce.Message, tc.want) {
			t.Errorf("%s: err = %v, want PULSE_WINDOW_INVALID %q", tc.name, err, tc.want)
		}
	}
	// Valid: non-cumulative needs no order_by.
	for _, w := range []types.Window{
		{Type: types.WIN_SHARE, Field: "x"},
		{Type: types.WIN_LIFT, Field: "x", Params: json.RawMessage(`{"base": "b"}`)},
	} {
		f, _ := Lookup(w.Type)
		if _, err := f(&w, WindowOptions{}); err != nil {
			t.Errorf("%s without order_by refused: %v", w.Type, err)
		}
	}
}
