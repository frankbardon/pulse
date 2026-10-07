package descriptor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// groupsSchema: a 5-entry categorical, a 3-member set, a packed_bool,
// a numeric and a date column.
func groupsSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vs ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vs {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("a", "b", "c", "d", "e")},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict("x", "y", "z")},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "num", Type: encoding.FieldTypeF64},
		{Name: "day", Type: encoding.FieldTypeDate},
	}}
}

func dateRangesGroup(t *testing.T, params any) *types.Group {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return &types.Group{Type: types.GROUP_DATE_RANGES, Field: "day", Params: b}
}

// TestEstimateGroups: the MaxGroups estimator wraps
// vectors.EstimateBuckets, adds GROUP_DATE_RANGES (ranges + the
// unmatched bucket), clamps by the record count (except the fan-out
// GROUP_SET_PER_ELEMENT) and yields nothing for an ungrouped request or
// an unknown-cardinality grouper.
func TestEstimateGroups(t *testing.T) {
	schema := groupsSchema(t)
	inst := UnscopedInstanceSnapshot(&ExtensionsSnapshot{
		RangeTables: []descriptor.RangeTableMeta{{Name: "fiscal", RangeCount: 4}},
	})
	threeRanges := map[string]any{"ranges": []map[string]string{
		{"label": "q1", "start": "2024-01-01", "end": "2024-03-31"},
		{"label": "q2", "start": "2024-04-01", "end": "2024-06-30"},
		{"label": "q3", "start": "2024-07-01", "end": "2024-09-30"},
	}}
	for _, c := range []struct {
		name    string
		g       *types.Group
		records int64
		want    int64
		known   bool
	}{
		{"category dictionary", &types.Group{Type: types.GROUP_CATEGORY, Field: "cat"}, -1, 5, true},
		{"category clamped by records", &types.Group{Type: types.GROUP_CATEGORY, Field: "cat"}, 3, 3, true},
		{"category records above dictionary", &types.Group{Type: types.GROUP_CATEGORY, Field: "cat"}, 1000, 5, true},
		{"include list", &types.Group{Type: types.GROUP_CATEGORY, Field: "cat", Include: []string{"a", "b"}}, -1, 2, true},
		{"packed_bool", &types.Group{Type: types.GROUP_CATEGORY, Field: "flag"}, -1, 2, true},
		{"quantile bins", &types.Group{Type: types.GROUP_QUANTILE, Field: "num", Interval: 7}, -1, 7, true},
		{"set per element dictionary", &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, -1, 3, true},
		{"set per element not clamped (fan-out)", &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, 1, 3, true},
		{"date ranges inline + unmatched", dateRangesGroup(t, threeRanges), -1, 4, true},
		{"date ranges inline clamped", dateRangesGroup(t, threeRanges), 2, 2, true},
		{"date ranges table + unmatched", dateRangesGroup(t, map[string]string{"table": "fiscal"}), -1, 5, true},
		{"date ranges unknown table", dateRangesGroup(t, map[string]string{"table": "nope"}), -1, 0, false},
		{"date ranges no params", &types.Group{Type: types.GROUP_DATE_RANGES, Field: "day"}, -1, 0, false},
		{"GROUP_RANGE unknown", &types.Group{Type: types.GROUP_RANGE, Field: "num", Interval: 10}, -1, 0, false},
		{"GROUP_DATE unknown", &types.Group{Type: types.GROUP_DATE, Field: "day"}, -1, 0, false},
		{"GROUP_ROUNDED unknown", &types.Group{Type: types.GROUP_ROUNDED, Field: "num"}, -1, 0, false},
		{"GROUP_SET_VALUE unknown", &types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}, -1, 0, false},
		{"category over a numeric field unknown", &types.Group{Type: types.GROUP_CATEGORY, Field: "num"}, -1, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, known := EstimateGroups([]*types.Group{c.g}, schema, inst, c.records)
			if got != c.want || known != c.known {
				t.Fatalf("EstimateGroups = %d, %v; want %d, %v", got, known, c.want, c.known)
			}
		})
	}
	if _, known := EstimateGroups(nil, schema, inst, 10); known {
		t.Fatal("an ungrouped request has a group estimate")
	}
	if _, known := EstimateGroups([]*types.Group{dateRangesGroup(t, map[string]string{"table": "fiscal"})}, schema, nil, -1); known {
		t.Fatal("a table-backed grouper without an instance snapshot has an estimate")
	}
}

// TestRequestLimitFindings_MaxGroupsPossible: a categorical whose
// dictionary exceeds MaxGroups is a Possible finding — never a
// pre-flight refusal — and the record count clamps it away.
func TestRequestLimitFindings_MaxGroupsPossible(t *testing.T) {
	schema := groupsSchema(t)
	l := limits.Defaults()
	l.MaxGroups = 4
	req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}}
	fs := RequestLimitFindings(req, schema, nil, l, 100)
	want := limits.Finding{Limit: limits.MaxGroups, Configured: 4, Estimated: 5, Grade: limits.Possible}
	if len(fs) != 1 || fs[0] != want {
		t.Fatalf("findings = %+v, want [%+v]", fs, want)
	}
	if err := LimitRefusal(req, schema, nil, l); err != nil {
		t.Fatalf("a possible finding refused the pre-flight: %v", err)
	}
	if fs := RequestLimitFindings(req, schema, nil, l, 4); len(fs) != 0 {
		t.Fatalf("records 4 clamp the estimate to the limit; findings = %+v", fs)
	}
	l.MaxGroups = limits.Unlimited
	if fs := RequestLimitFindings(req, schema, nil, l, 100); len(fs) != 0 {
		t.Fatalf("unlimited: findings = %+v", fs)
	}
}

// TestPredict_MaxGroupsClampedByHeaderCount: predict clamps the group
// estimate by the cohort's header record count — a 5-entry dictionary
// over 2 records is at most 2 buckets, so MaxGroups=3 has no finding,
// while MaxGroups=1 is a possible one estimated at 2.
func TestPredict_MaxGroupsClampedByHeaderCount(t *testing.T) {
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d", "e"} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: d},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write([]byte{0, 1}) // two records
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "cat", Label: "n"}},
	}
	predictWith := func(n int64) *descriptor.PredictResult {
		l := limits.Defaults()
		l.MaxGroups = n
		env := Predict(bytes.NewReader(buf.Bytes()), req, &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithLimits(l)})
		res := env.Data.(*descriptor.PredictResult)
		if !res.Valid {
			t.Fatalf("MaxGroups=%d: invalid predict %+v (errors %+v)", n, res, env.Errors)
		}
		return res
	}
	if res := predictWith(3); res.LimitFindings != nil {
		t.Fatalf("estimate not clamped by the 2-record header count: %+v", res.LimitFindings)
	}
	want := []descriptor.LimitFinding{{Limit: "max_groups", Configured: 1, Estimated: 2, Grade: descriptor.LimitGradePossible}}
	if res := predictWith(1); !reflect.DeepEqual(res.LimitFindings, want) {
		t.Fatalf("findings = %+v, want %+v", res.LimitFindings, want)
	}
}
