package window

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// foldLess is a key order whose ties are NOT just equal strings (case
// folds), so a test can tell a tie judged by the key order from one
// judged byte-wise.
func foldLess(a, b string) bool { return strings.ToLower(a) < strings.ToLower(b) }

// byLen orders keys by length — a stand-in for a grouper rank that
// disagrees with byte order ("10" after "9").
func byLen(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// TestApplyKeyed_RankTiesFollowKeyOrder: WIN_RANK / WIN_DENSE_RANK judge
// a tie through the same comparator that sorted the partition, so two
// keys the key order ties share a rank.
func TestApplyKeyed_RankTiesFollowKeyOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		typ  types.WindowType
		want map[string]int64
	}{
		{types.WIN_RANK, map[string]int64{"a": 1, "A": 1, "b": 3}},
		{types.WIN_DENSE_RANK, map[string]int64{"a": 1, "A": 1, "b": 2}},
	} {
		rows := []map[string]any{{"k": "b"}, {"k": "A"}, {"k": "a"}}
		w := &types.Window{Type: tc.typ, OrderBy: []types.OrderKey{{Field: "k"}}, Label: "r"}
		if err := ApplyKeyed(context.Background(), rows, []*types.Window{w}, nil, KeyOrders{"k": foldLess}); err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if got := r["r"].(int64); got != tc.want[r["k"].(string)] {
				t.Errorf("%s %v = %d, want %d", tc.typ, r["k"], got, tc.want[r["k"].(string)])
			}
		}
	}
}

// TestSortKeyed_KeyOrderAndFallbacks: a column the orders name sorts by
// its KeyLess (reversed under Desc, nulls still last); a column they do
// not name, and a nil orders, keep the byte-wise CompareKey order.
func TestSortKeyed_KeyOrderAndFallbacks(t *testing.T) {
	t.Parallel()

	keys := func(rows []map[string]any, col string) []any {
		out := make([]any, len(rows))
		for i, r := range rows {
			out[i] = r[col]
		}
		return out
	}
	mk := func() []map[string]any {
		return []map[string]any{{"k": "10", "o": "10"}, {"k": nil, "o": nil}, {"k": "9", "o": "9"}, {"k": "100", "o": "100"}}
	}
	orders := KeyOrders{"k": byLen}
	for _, tc := range []struct {
		name string
		key  types.OrderKey
		ord  KeyOrders
		want []any
	}{
		{"keyed asc", types.OrderKey{Field: "k"}, orders, []any{"9", "10", "100", nil}},
		{"keyed desc", types.OrderKey{Field: "k", Desc: true}, orders, []any{"100", "10", "9", nil}},
		{"unnamed column byte-wise", types.OrderKey{Field: "o"}, orders, []any{"10", "100", "9", nil}},
		{"nil orders byte-wise", types.OrderKey{Field: "k"}, nil, []any{"10", "100", "9", nil}},
	} {
		rows := mk()
		SortKeyed(rows, []types.OrderKey{tc.key}, tc.ord)
		col := tc.key.Field
		got := keys(rows, col)
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}
