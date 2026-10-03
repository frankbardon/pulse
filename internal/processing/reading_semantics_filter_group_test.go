package processing

import (
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// The tests in this file pin the behaviours the FILTER_* / GROUP_*
// guidance (internal/descriptor/purposes_filterers.go,
// purposes_groupers.go) and the op-filter-* / op-group-* skills state as
// fact: which filters keep or drop a missing value, inclusive bounds,
// that GROUP_ROUNDED rounds DOWN, GROUP_RANGE's "low-high" key, and that
// GROUP_QUANTILE splits by rank so tied values can straddle buckets. A
// change here must change that prose with it.

// Missing values: the keep-list, range and contains filters drop them;
// the block-list filters (FILTER_EXCLUDE, FILTER_SET_CONTAINS_NONE) keep
// them.
func TestReading_FilterNullKeepDrop(t *testing.T) {
	num := numericSchema()
	nullNum := NewRecordWithNulls(num, map[string]float64{"score": 0}, map[string]bool{"score": true})
	for _, c := range []struct {
		ft     types.FiltererType
		values []string
		want   bool
	}{
		{types.FILTER_INCLUDE, []string{"0"}, false},
		{types.FILTER_EXCLUDE, []string{"0"}, true},
		{types.FILTER_RANGE, []string{"-1", "1"}, false},
	} {
		if got, _ := makeFilterFunc(t, c.ft, "score", c.values, "", num)(nullNum); got != c.want {
			t.Errorf("%s on a missing value keeps=%v, want %v", c.ft, got, c.want)
		}
	}

	set := makeSetTestSchema(t)
	nullSet := makeNullSetRecord(set)
	for _, c := range []struct {
		ft   types.FiltererType
		want bool
	}{
		{types.FILTER_SET_CONTAINS_ANY, false},
		{types.FILTER_SET_CONTAINS_ALL, false},
		{types.FILTER_SET_CONTAINS_NONE, true},
		{types.FILTER_SET_EQUALS, false},
	} {
		if got, _ := makeFilterFunc(t, c.ft, "tags", []string{"VISA"}, "", set)(nullSet); got != c.want {
			t.Errorf("%s on a missing set keeps=%v, want %v", c.ft, got, c.want)
		}
	}
}

// FILTER_RANGE keeps both end points.
func TestReading_FilterRangeInclusive(t *testing.T) {
	s := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_RANGE, "score", []string{"10", "20"}, "", s)
	for v, want := range map[float64]bool{9.999: false, 10: true, 20: true, 20.001: false} {
		if got, _ := fn(NewRecord(s, map[string]float64{"score": v})); got != want {
			t.Errorf("FILTER_RANGE [10,20] keeps %v = %v, want %v", v, got, want)
		}
	}
}

// FILTER_TRUE drops a missing value in both modes; FILTER_FALSE drops it
// in strict mode but keeps it in truthy mode (missing counts as false).
func TestReading_FilterBoolMissing(t *testing.T) {
	s := boolSchema()
	null := NewRecordWithNulls(s, map[string]float64{"flag": 0}, map[string]bool{"flag": true})
	for _, c := range []struct {
		ft     types.FiltererType
		values []string
		want   bool
	}{
		{types.FILTER_TRUE, nil, false},
		{types.FILTER_TRUE, []string{"truthy"}, false},
		{types.FILTER_FALSE, nil, false},
		{types.FILTER_FALSE, []string{"truthy"}, true},
	} {
		fn, err := buildFilter(t, c.ft, "flag", c.values, s)
		if err != nil {
			t.Fatalf("%s %v: %v", c.ft, c.values, err)
		}
		if got, _ := fn(null); got != c.want {
			t.Errorf("%s %v on a missing value keeps=%v, want %v", c.ft, c.values, got, c.want)
		}
	}
}

// groupSizes runs g over values of field "score" and returns bucket
// key -> row count.
func groupSizes(t *testing.T, gt types.GroupType, interval float64, values []float64) map[string]int {
	t.Helper()
	s := numericSchema()
	g := makeGrouper(t, gt, "score", interval, s)
	groups, err := g.Group(makeRecords(s, "score", values), "score")
	if err != nil {
		t.Fatalf("%s Group: %v", gt, err)
	}
	out := map[string]int{}
	for k, rs := range groups {
		out[k] = len(rs)
	}
	return out
}

func sameSizes(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// GROUP_ROUNDED rounds DOWN to a multiple of Interval (floor), never to
// the nearest: 19 -> 10, -3 -> -10.
func TestReading_GroupRoundedFloors(t *testing.T) {
	got := groupSizes(t, types.GROUP_ROUNDED, 10, []float64{19, 10, -3})
	want := map[string]int{"10": 2, "-10": 1}
	if !sameSizes(got, want) {
		t.Errorf("GROUP_ROUNDED interval 10 = %v, want %v (floor, not nearest)", got, want)
	}
}

// GROUP_RANGE keys a bin "low-high"; the low edge is in the bin, the
// high edge starts the next one.
func TestReading_GroupRangeKeyHalfOpen(t *testing.T) {
	got := groupSizes(t, types.GROUP_RANGE, 10, []float64{10, 19.5, 20})
	want := map[string]int{"10-20": 2, "20-30": 1}
	if !sameSizes(got, want) {
		t.Errorf("GROUP_RANGE interval 10 = %v, want %v", got, want)
	}
}

// GROUP_QUANTILE splits by rank into equal-count buckets, so equal
// values can land in different buckets.
func TestReading_GroupQuantileTiesSplit(t *testing.T) {
	s := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "score", 2, s)
	groups, err := g.Group(makeRecords(s, "score", []float64{5, 5, 5, 5}), "score")
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	keys := make([]string, 0, len(groups))
	for k, rs := range groups {
		keys = append(keys, k)
		if len(rs) != 2 {
			t.Errorf("bucket %s holds %d rows, want 2 (equal counts by rank)", k, len(rs))
		}
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "B1" || keys[1] != "B2" {
		t.Errorf("four equal values in 2 quantiles -> buckets %v, want [B1 B2]", keys)
	}
}

// GROUP_DATE with no component groups by month.
func TestReading_GroupDateDefaultsToMonth(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{{Name: "d", Type: encoding.FieldTypeDate}}}
	g := makeGrouper(t, types.GROUP_DATE, "d", 0, s)
	// 19800 = 2024-03-18 (epoch days).
	key, err := g.(StreamableGrouper).KeyFor(NewRecord(s, map[string]float64{"d": 19800}))
	if err != nil || key != "2024-03" {
		t.Errorf("GROUP_DATE default key = %q, %v; want 2024-03", key, err)
	}
}
