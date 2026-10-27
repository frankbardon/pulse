package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// phaseRanges is the shared inline range set used across the
// GROUP_DATE_RANGES tests: three contiguous, non-overlapping labeled
// spans in a deliberately non-alphabetical author order so the
// supplied-order assertions are meaningful.
func phaseRangesParams(t *testing.T, unmatched string) json.RawMessage {
	t.Helper()
	body := map[string]any{
		"ranges": []map[string]any{
			{"label": "launch", "start": "2024-01-01", "end": "2024-03-31"},
			{"label": "growth", "start": "2024-04-01", "end": "2024-09-30"},
			{"label": "steady", "start": "2024-10-01"},
		},
	}
	if unmatched != "" {
		body["unmatched_label"] = unmatched
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func makeDateRangesGrouper(t *testing.T, params json.RawMessage, schema *encoding.Schema) Grouper {
	t.Helper()
	factory, ok := grouperRegistry[types.GROUP_DATE_RANGES]
	if !ok {
		t.Fatalf("no grouper registered for GROUP_DATE_RANGES")
	}
	g, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: params}, schema)
	if err != nil {
		t.Fatalf("create grouper: %v", err)
	}
	return g
}

func TestGrouper_DateRanges_Labeling(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	g := makeDateRangesGrouper(t, phaseRangesParams(t, ""), schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 2, 15), // launch
		epochDays(2024, 3, 31), // launch (inclusive upper edge)
		epochDays(2024, 4, 1),  // growth (inclusive lower edge)
		epochDays(2024, 12, 1), // steady (open upper)
		epochDays(2023, 6, 1),  // unmatched (before every range)
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int{"launch": 2, "growth": 1, "steady": 1, "unmatched": 1}
	if len(groups) != len(want) {
		t.Fatalf("group count = %d, want %d (%v)", len(groups), len(want), groupKeys(groups))
	}
	for label, n := range want {
		if len(groups[label]) != n {
			t.Errorf("bucket %q count = %d, want %d", label, len(groups[label]), n)
		}
	}
}

func TestGrouper_DateRanges_UnmatchedLabelOverride(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	g := makeDateRangesGrouper(t, phaseRangesParams(t, "pre_launch"), schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2023, 6, 1),  // pre_launch
		epochDays(2024, 2, 15), // launch
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups["pre_launch"]) != 1 {
		t.Errorf("pre_launch count = %d, want 1", len(groups["pre_launch"]))
	}
	if _, ok := groups["unmatched"]; ok {
		t.Errorf("default unmatched bucket should not exist when overridden")
	}
}

func TestGrouper_DateRanges_ComponentsSuppliedOrder(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	g := makeDateRangesGrouper(t, phaseRangesParams(t, ""), schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 12, 1), // steady
		epochDays(2024, 2, 15), // launch
		epochDays(2023, 6, 1),  // unmatched
	})
	if _, err := g.Group(records, "enrolled"); err != nil {
		t.Fatalf("group: %v", err)
	}
	mg, ok := g.(MetaGrouper)
	if !ok {
		t.Fatalf("grouper does not implement MetaGrouper")
	}
	comp, err := mg.Components()
	if err != nil {
		t.Fatalf("components: %v", err)
	}
	if comp["n_ranges"] != 3 {
		t.Errorf("n_ranges = %v, want 3", comp["n_ranges"])
	}
	if comp["unmatched_label"] != "unmatched" {
		t.Errorf("unmatched_label = %v, want unmatched", comp["unmatched_label"])
	}
	buckets, ok := comp["buckets"].([]map[string]any)
	if !ok {
		t.Fatalf("buckets wrong type: %T", comp["buckets"])
	}
	// Supplied order: launch, growth, steady, then unmatched last.
	wantKeys := []string{"launch", "growth", "steady", "unmatched"}
	if len(buckets) != len(wantKeys) {
		t.Fatalf("bucket count = %d, want %d", len(buckets), len(wantKeys))
	}
	for i, wk := range wantKeys {
		if buckets[i]["key"] != wk {
			t.Errorf("buckets[%d].key = %v, want %q", i, buckets[i]["key"], wk)
		}
	}
	// growth has zero rows but still emits (a configured range).
	if buckets[1]["count"] != 0 {
		t.Errorf("growth count = %v, want 0", buckets[1]["count"])
	}
}

func TestGrouper_DateRanges_KeyForMergeableParity(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	g := makeDateRangesGrouper(t, phaseRangesParams(t, ""), schema)
	sg, ok := g.(StreamableGrouper)
	if !ok {
		t.Fatalf("grouper does not implement StreamableGrouper")
	}

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 2, 15),
		epochDays(2024, 5, 20),
		epochDays(2025, 1, 1),
		epochDays(2020, 1, 1),
	})

	// Group() (buffered) and per-record KeyFor must agree bucket-for-bucket.
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	perKey := map[string]int{}
	for _, r := range records {
		k, err := sg.KeyFor(r)
		if err != nil {
			t.Fatalf("KeyFor: %v", err)
		}
		perKey[k]++
	}
	for k, n := range perKey {
		if len(groups[k]) != n {
			t.Errorf("bucket %q: Group=%d KeyFor=%d — streamable parity broken", k, len(groups[k]), n)
		}
	}
	if len(perKey) != len(groups) {
		t.Errorf("KeyFor produced %d buckets, Group produced %d", len(perKey), len(groups))
	}
}

func TestGrouper_DateRanges_NullDate(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	g := makeDateRangesGrouper(t, phaseRangesParams(t, ""), schema)
	sg := g.(StreamableGrouper)

	records := makeRecordsWithNulls(schema, "enrolled", []float64{
		epochDays(2024, 2, 15),
		0,
	}, []int{1})

	// KeyFor on the null row returns the ErrGrouperKeyNull sentinel.
	if _, err := sg.KeyFor(records[1]); err != ErrGrouperKeyNull {
		t.Errorf("KeyFor(null) error = %v, want ErrGrouperKeyNull", err)
	}
	// Group() drops the null row silently.
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	total := 0
	for _, b := range groups {
		total += len(b)
	}
	if total != 1 {
		t.Errorf("non-null bucketed rows = %d, want 1", total)
	}
}

func TestGrouper_DateRanges_NonDateField(t *testing.T) {
	t.Parallel()

	schema := numericSchema() // "score" is f64, not date
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	_, err := factory(&types.Group{
		Type:   types.GROUP_DATE_RANGES,
		Field:  "score",
		Params: phaseRangesParams(t, ""),
	}, schema)
	if got := codeOf(t, err); got != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Errorf("non-date field error code = %v, want PULSE_FIELD_TYPE_UNSUPPORTED", got)
	}
}

// TestGrouper_DateRanges_NoSource asserts that naming neither an inline
// `ranges` array nor a `table` is the ambiguous-source error (exactly one
// source is required). An explicitly-empty `ranges` array counts as "no
// inline source" for source selection.
func TestGrouper_DateRanges_NoSource(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	cases := []json.RawMessage{
		nil,
		json.RawMessage(`{}`),
		json.RawMessage(`{"ranges":[]}`),
	}
	for _, params := range cases {
		_, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: params}, schema)
		if got := codeOf(t, err); got != errors.PULSE_RANGE_SOURCE_AMBIGUOUS {
			t.Errorf("params %s: error code = %v, want PULSE_RANGE_SOURCE_AMBIGUOUS", params, got)
		}
	}
}

func TestGrouper_DateRanges_UnmatchedLabelCollision(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	params := json.RawMessage(`{"ranges":[{"label":"launch","start":"2024-01-01"}],"unmatched_label":"launch"}`)
	_, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: params}, schema)
	if got := codeOf(t, err); got != errors.PROCESSING_CONFIG {
		t.Errorf("collision error code = %v, want PROCESSING_CONFIG", got)
	}
}

// TestGrouper_DateRanges_KeyRankAndInclude: the keyOrderer rank is the
// supplied index, unmatched last, any other key unranked; an include
// list drops non-member rows at KeyFor and restricts the Components
// buckets to its members — a zero-count included range still emits —
// in include order.
func TestGrouper_DateRanges_KeyRankAndInclude(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	ko := makeDateRangesGrouper(t, phaseRangesParams(t, ""), schema).(keyOrderer)
	for key, want := range map[string]float64{"launch": 0, "growth": 1, "steady": 2, "unmatched": 3} {
		if r, ok := ko.keyRank(key); !ok || r != want {
			t.Errorf("keyRank(%q) = %v, %v; want %v", key, r, ok, want)
		}
	}
	if _, ok := ko.keyRank("nope"); ok {
		t.Error("keyRank ranks a label the grouper never mints")
	}

	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	g, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: phaseRangesParams(t, ""),
		Include: []string{"unmatched", "growth", "launch", "nope"}}, schema)
	if err != nil {
		t.Fatal(err)
	}
	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 12, 1), // steady: not included
		epochDays(2024, 2, 15), // launch
		epochDays(2023, 6, 1),  // unmatched
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(groups["launch"]) != 1 || len(groups["unmatched"]) != 1 {
		t.Fatalf("groups = %v, want launch and unmatched only", groupKeys(groups))
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, b := range comp["buckets"].([]map[string]any) {
		keys = append(keys, b["key"].(string))
	}
	if want := []string{"unmatched", "growth", "launch"}; !equalStrings(keys, want) {
		t.Errorf("components buckets = %v, want %v", keys, want)
	}
}

// TestGrouper_Date_KeyRank: day_of_week ranks Monday..Sunday; every
// other component, and a key the component never mints, is unranked
// (alphabetical, byte-identically).
func TestGrouper_Date_KeyRank(t *testing.T) {
	t.Parallel()

	mk := func(component string) keyOrderer {
		g, err := grouperRegistry[types.GROUP_DATE](&types.Group{Type: types.GROUP_DATE, Field: "enrolled",
			Params: json.RawMessage(`{"component":"` + component + `"}`)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return g.(keyOrderer)
	}
	dow := mk("day_of_week")
	for i, d := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		if r, ok := dow.keyRank(d); !ok || r != float64(i) {
			t.Errorf("day_of_week keyRank(%q) = %v, %v; want %d", d, r, ok, i)
		}
	}
	// hour_of_day's zero-padded keys sort numerically as strings, so
	// it — like every ISO-keyed component — needs no rank.
	for _, tc := range []struct {
		g   keyOrderer
		key string
	}{{dow, "monday"}, {mk("hour_of_day"), "07"}, {mk("day"), "2026-03-02"}, {mk("month"), "2026-03"}} {
		if _, ok := tc.g.keyRank(tc.key); ok {
			t.Errorf("keyRank(%q) ranked", tc.key)
		}
	}
}
