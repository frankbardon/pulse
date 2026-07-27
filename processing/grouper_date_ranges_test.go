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
	schema := numericSchema() // "score" is f64, not date
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	_, err := factory(&types.Group{
		Type:   types.GROUP_DATE_RANGES,
		Field:  "score",
		Params: phaseRangesParams(t, ""),
	}, schema)
	if got := codeOf(t, err); got != errors.PROCESSING_CONFIG {
		t.Errorf("non-date field error code = %v, want PROCESSING_CONFIG", got)
	}
}

func TestGrouper_DateRanges_EmptyRanges(t *testing.T) {
	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	cases := []json.RawMessage{
		nil,
		json.RawMessage(`{}`),
		json.RawMessage(`{"ranges":[]}`),
	}
	for _, params := range cases {
		_, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: params}, schema)
		if got := codeOf(t, err); got != errors.PULSE_RANGE_EMPTY {
			t.Errorf("params %s: error code = %v, want PULSE_RANGE_EMPTY", params, got)
		}
	}
}

func TestGrouper_DateRanges_UnmatchedLabelCollision(t *testing.T) {
	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE_RANGES]
	params := json.RawMessage(`{"ranges":[{"label":"launch","start":"2024-01-01"}],"unmatched_label":"launch"}`)
	_, err := factory(&types.Group{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: params}, schema)
	if got := codeOf(t, err); got != errors.PROCESSING_CONFIG {
		t.Errorf("collision error code = %v, want PROCESSING_CONFIG", got)
	}
}
