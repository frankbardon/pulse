package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestCompareRows_SharedComparator: a post-test order_by orders with the
// window / Request.Sort comparator — a present null and a missing key
// both trail under Asc AND Desc, and a decimal aggregate cell orders by
// value. Before, a present nil tied against every row (its own
// compareAny knew only float64 / int / string) and every decimal
// aggregate compared equal.
func TestCompareRows_SharedComparator(t *testing.T) {
	dec := func(m int64) decimalAggResult {
		return decimalAggResult{Value: encoding.NewDecimal128FromInt(m), Scale: 2}
	}
	for _, desc := range []bool{false, true} {
		keys := []types.OrderKey{{Field: "x", Desc: desc}}
		for _, null := range []map[string]any{{"x": nil}, {}} {
			if c := compareRows(map[string]any{"x": 1.0}, null, keys); c >= 0 {
				t.Errorf("desc=%v: value vs %v = %d, want < 0 (null last)", desc, null, c)
			}
			if c := compareRows(null, map[string]any{"x": 1.0}, keys); c <= 0 {
				t.Errorf("desc=%v: %v vs value = %d, want > 0 (null last)", desc, null, c)
			}
		}
		want := -1
		if desc {
			want = 1
		}
		if c := compareRows(map[string]any{"x": dec(-325)}, map[string]any{"x": dec(1050)}, keys); c != want {
			t.Errorf("desc=%v: decimal -3.25 vs 10.50 = %d, want %d", desc, c, want)
		}
	}
}
