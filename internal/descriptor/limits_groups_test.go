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
	t.Parallel()

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
		{"numeric ranges inline + unmatched", &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "num", Params: json.RawMessage(`{"ranges":[{"label":"a","end":1},{"label":"b","start":1}]}`)}, -1, 3, true},
		{"numeric ranges table + unmatched", &types.Group{Type: types.GROUP_NUMERIC_RANGES, Field: "num", Params: json.RawMessage(`{"table":"fiscal"}`)}, -1, 5, true},
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

// TestEstimateGroups_MultiGrouperProduct: two or more groupers mint
// composite keys, so the MaxGroups bound is the PRODUCT of every
// grouper's own bound — clamped by records unless a grouper fans out,
// unknown when any grouper is (never Groups[0] alone).
func TestEstimateGroups_MultiGrouperProduct(t *testing.T) {
	t.Parallel()

	schema := groupsSchema(t)
	inst := UnscopedInstanceSnapshot(&ExtensionsSnapshot{
		RangeTables: []descriptor.RangeTableMeta{{Name: "fiscal", RangeCount: 4}},
	})
	cat := &types.Group{Type: types.GROUP_CATEGORY, Field: "cat"}
	flag := &types.Group{Type: types.GROUP_CATEGORY, Field: "flag"}
	tags := &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}
	quant := &types.Group{Type: types.GROUP_QUANTILE, Field: "num", Interval: 3}
	rng := &types.Group{Type: types.GROUP_RANGE, Field: "num", Interval: 10}
	fiscal := dateRangesGroup(t, map[string]string{"table": "fiscal"})
	for _, c := range []struct {
		name    string
		groups  []*types.Group
		records int64
		want    int64
		known   bool
	}{
		{"category x boolean", []*types.Group{cat, flag}, -1, 10, true},
		{"three positions", []*types.Group{cat, flag, quant}, -1, 30, true},
		{"product clamped by records", []*types.Group{cat, flag}, 7, 7, true},
		{"records above the product", []*types.Group{cat, flag}, 1000, 10, true},
		{"fan-out position escapes the clamp", []*types.Group{cat, tags}, 4, 15, true},
		{"date ranges position", []*types.Group{flag, fiscal}, -1, 10, true},
		{"unknown inner position", []*types.Group{cat, rng}, -1, 0, false},
		{"unknown outer position", []*types.Group{rng, cat}, -1, 0, false},
		{"nil position", []*types.Group{cat, nil}, -1, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, known := EstimateGroups(c.groups, schema, inst, c.records)
			if got != c.want || known != c.known {
				t.Fatalf("EstimateGroups = %d, %v; want %d, %v", got, known, c.want, c.known)
			}
		})
	}
	// The finding carries the product (Possible: an upper bound).
	l := limits.Defaults()
	l.MaxGroups = 8
	fs := RequestLimitFindings(&types.Request{Groups: []*types.Group{cat, flag}}, schema, inst, l, LimitInputs{Records: 100, JoinRightRows: -1})
	want := limits.Finding{Limit: limits.MaxGroups, Configured: 8, Estimated: 10, Grade: limits.Possible}
	if len(fs) != 1 || fs[0] != want {
		t.Fatalf("findings = %+v, want [%+v]", fs, want)
	}
}

// TestFanOutMultiplicity (U35 #267): the most buckets one record can
// land in — the product of every GROUP_SET_PER_ELEMENT position's key
// bound (its set dictionary, or its include list), 1 when no grouper
// fans out; a set field without a dictionary falls back to the type's
// member ceiling.
func TestFanOutMultiplicity(t *testing.T) {
	t.Parallel()

	schema := groupsSchema(t)
	schema.Fields = append(schema.Fields,
		encoding.Field{Name: "more", Type: encoding.FieldTypeSetU8, Dictionary: func() *encoding.Dictionary {
			d := encoding.NewDictionary()
			for _, v := range []string{"p", "q", "r", "s"} {
				if _, err := d.Add(v); err != nil {
					t.Fatal(err)
				}
			}
			return d
		}()},
		encoding.Field{Name: "bare", Type: encoding.FieldTypeSetU16})
	cat := &types.Group{Type: types.GROUP_CATEGORY, Field: "cat"}
	tags := &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}
	more := &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "more"}
	for _, c := range []struct {
		name   string
		groups []*types.Group
		want   int64
	}{
		{"ungrouped", nil, 1},
		{"no fan-out", []*types.Group{cat}, 1},
		{"set dictionary", []*types.Group{tags}, 3},
		{"include narrows", []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags", Include: []string{"x"}}}, 1},
		{"beside a plain grouper", []*types.Group{cat, tags}, 3},
		{"two fan-out positions multiply", []*types.Group{tags, more}, 12},
		{"no dictionary: the type ceiling", []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "bare"}}, 16},
		{"nil position", []*types.Group{nil, tags}, 3},
	} {
		if got := FanOutMultiplicity(c.groups, schema); got != c.want {
			t.Errorf("%s: FanOutMultiplicity = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestRequestLimitFindings_MaxGroupsPossible: a categorical whose
// dictionary exceeds MaxGroups is a Possible finding — never a
// pre-flight refusal — and the record count clamps it away.
func TestRequestLimitFindings_MaxGroupsPossible(t *testing.T) {
	t.Parallel()

	schema := groupsSchema(t)
	l := limits.Defaults()
	l.MaxGroups = 4
	req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}}
	fs := RequestLimitFindings(req, schema, nil, l, LimitInputs{Records: 100, JoinRightRows: -1})
	want := limits.Finding{Limit: limits.MaxGroups, Configured: 4, Estimated: 5, Grade: limits.Possible}
	if len(fs) != 1 || fs[0] != want {
		t.Fatalf("findings = %+v, want [%+v]", fs, want)
	}
	if err := LimitRefusal(req, schema, nil, l, LimitInputs{Records: -1, JoinRightRows: -1}); err != nil {
		t.Fatalf("a possible finding refused the pre-flight: %v", err)
	}
	if fs := RequestLimitFindings(req, schema, nil, l, LimitInputs{Records: 4, JoinRightRows: -1}); len(fs) != 0 {
		t.Fatalf("records 4 clamp the estimate to the limit; findings = %+v", fs)
	}
	l.MaxGroups = limits.Unlimited
	if fs := RequestLimitFindings(req, schema, nil, l, LimitInputs{Records: 100, JoinRightRows: -1}); len(fs) != 0 {
		t.Fatalf("unlimited: findings = %+v", fs)
	}
}

// TestPredict_MaxGroupsClampedByHeaderCount: predict clamps the group
// estimate by the cohort's header record count — a 5-entry dictionary
// over 2 records is at most 2 buckets, so MaxGroups=3 has no finding,
// while MaxGroups=1 is a possible one estimated at 2.
func TestPredict_MaxGroupsClampedByHeaderCount(t *testing.T) {
	t.Parallel()

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
