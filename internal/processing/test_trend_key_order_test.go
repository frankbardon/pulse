package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// weekRecords is one record per weekday, Monday 2024-01-15 through
// Sunday 2024-01-21, scored 1 (Monday) .. 7 (Sunday) — a series that
// rises strictly in ISO weekday order.
func weekRecords(schema *encoding.Schema) []*Record {
	out := make([]*Record, 0, 7)
	for i := 0; i < 7; i++ {
		out = append(out, NewRecord(schema, map[string]float64{
			"enrolled": epochDays(2024, 1, 15+i),
			"score":    float64(i + 1),
		}))
	}
	return out
}

func trendOverGroups(t *testing.T, schema *encoding.Schema, groups []*types.Group, orderBy []types.OrderKey, records []*Record) []*types.TestResult {
	t.Helper()
	req := &types.Request{
		Groups:       groups,
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"}},
		PostTests:    []*types.Test{{Type: types.TEST_TREND, Field: "mean", OrderBy: orderBy}},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.PostTests) == 0 || resp.PostTests[0] == nil {
		t.Fatalf("no post-test result: %+v", resp.PostTests)
	}
	return resp.PostTests
}

func trendS(t *testing.T, res *types.TestResult) float64 {
	t.Helper()
	if res.Error != nil {
		t.Fatalf("trend slot failed: %+v", res.Error)
	}
	return res.Details["s"].(float64)
}

// TestTrendPost_DayOfWeekKeyFollowsGrouperOrder: an order_by naming a
// GROUP_DATE day_of_week key column trends Monday -> Sunday (the
// grouper's key order), not alphabetically: a series rising 1..7 from
// Monday is the monotone S = +21, tau = +1. Descending reverses it.
func TestTrendPost_DayOfWeekKeyFollowsGrouperOrder(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	groups := []*types.Group{{Type: types.GROUP_DATE, Field: "enrolled",
		Params: json.RawMessage(`{"component":"day_of_week"}`)}}
	res := trendOverGroups(t, schema, groups, []types.OrderKey{{Field: "enrolled"}}, weekRecords(schema))[0]
	if s := trendS(t, res); s != 21 {
		t.Errorf("S = %g, want +21 (Monday-first)", s)
	}
	if tau := res.Details["tau"].(float64); tau != 1 {
		t.Errorf("tau = %g, want 1", tau)
	}
	if res.Statistic <= 0 {
		t.Errorf("Z = %g, want positive", res.Statistic)
	}
	desc := trendOverGroups(t, schema, groups, []types.OrderKey{{Field: "enrolled", Desc: true}}, weekRecords(schema))[0]
	if s := trendS(t, desc); s != -21 {
		t.Errorf("desc S = %g, want -21 (Sunday-first)", s)
	}
}

// TestTrendPost_DateRangesKeyFollowsSuppliedOrder: an order_by naming a
// GROUP_DATE_RANGES label column trends in the ranges' supplied order
// (launch, growth, steady), not alphabetically (growth, launch, steady).
func TestTrendPost_DateRangesKeyFollowsSuppliedOrder(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 2, 15), "score": 1}), // launch
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 6, 1), "score": 2}),  // growth
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 12, 1), "score": 3}), // steady
	}
	groups := []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: phaseRangesParams(t, "")}}
	res := trendOverGroups(t, schema, groups, []types.OrderKey{{Field: "enrolled"}}, records)[0]
	// Alphabetical would be growth(2) launch(1) steady(3): S = +1.
	if s := trendS(t, res); s != 3 {
		t.Errorf("S = %g, want +3 (supplied order)", s)
	}
}

// TestTrendPost_DayOfWeekKeyOrderPerPartition: on a composite grouped
// request each partition's trend (the partitioned post-test path)
// orders its day_of_week key column Monday-first too.
func TestTrendPost_DayOfWeekKeyOrderPerPartition(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "site", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "enrolled", Type: encoding.FieldTypeDate},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}
	var records []*Record
	for _, site := range []string{"north", "south"} {
		id := dictIDOrAdd(schema, "site", site)
		for i := 0; i < 7; i++ {
			records = append(records, NewRecord(schema, map[string]float64{
				"site": float64(id), "enrolled": epochDays(2024, 1, 15+i), "score": float64(i + 1),
			}))
		}
	}
	groups := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "site"},
		{Type: types.GROUP_DATE, Field: "enrolled", Params: json.RawMessage(`{"component":"day_of_week"}`)},
	}
	results := trendOverGroups(t, schema, groups, []types.OrderKey{{Field: "enrolled"}}, records)
	if len(results) != 2 {
		t.Fatalf("partitioned results = %d, want 2", len(results))
	}
	for _, res := range results {
		if s := trendS(t, res); s != 21 {
			t.Errorf("partition %v S = %g, want +21", res.GroupKey, s)
		}
	}
}

// TestTrendPost_CrosstabAxisKeyFollowsGrouperOrder: a crosstab post-test
// over cell rows orders a day_of_week axis key Monday-first too.
func TestTrendPost_CrosstabAxisKeyFollowsGrouperOrder(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "site", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "enrolled", Type: encoding.FieldTypeDate},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}
	id := dictIDOrAdd(schema, "site", "north")
	var records []*Record
	for i := 0; i < 7; i++ {
		records = append(records, NewRecord(schema, map[string]float64{
			"site": float64(id), "enrolled": epochDays(2024, 1, 15+i), "score": float64(i + 1),
		}))
	}
	resp, err := NewProcessor(schema).RunCrosstab(context.Background(), &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_DATE, Field: "enrolled", Params: json.RawMessage(`{"component":"day_of_week"}`)}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
			Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"},
		},
		PostTests: []*types.Test{{Type: types.TEST_TREND, Field: "mean", OrderBy: []types.OrderKey{{Field: "enrolled"}}}},
	}, records)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	if len(resp.PostTests) != 1 || resp.PostTests[0] == nil {
		t.Fatalf("post tests = %+v", resp.PostTests)
	}
	if s := trendS(t, resp.PostTests[0]); s != 21 {
		t.Errorf("S = %g, want +21 (Monday-first)", s)
	}
}

// TestTrendPost_ISODateKeyUnchanged: an ISO-keyed date grouper column
// (no intrinsic rank beyond its string order) trends exactly as before —
// the grouper hook leaves an unranked key on the byte-wise comparator.
func TestTrendPost_ISODateKeyUnchanged(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	groups := []*types.Group{{Type: types.GROUP_DATE, Field: "enrolled",
		Params: json.RawMessage(`{"component":"day"}`)}}
	res := trendOverGroups(t, schema, groups, []types.OrderKey{{Field: "enrolled"}}, weekRecords(schema))[0]
	if s := trendS(t, res); s != 21 {
		t.Errorf("S = %g, want +21 (chronological ISO days)", s)
	}
}

// TestTrendPost_NumericBucketKeyFollowsValue: a GROUP_ROUNDED key column
// ("5", "10", "15") trends by bucket value, not as strings ("10", "15",
// "5", which would give S = -1).
func TestTrendPost_NumericBucketKeyFollowsValue(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	var records []*Record
	for _, v := range []float64{5, 10, 15} {
		records = append(records, NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 1, 15), "score": v}))
	}
	groups := []*types.Group{{Type: types.GROUP_ROUNDED, Field: "score", Interval: 5}}
	res := trendOverGroups(t, schema, groups, []types.OrderKey{{Field: "score"}}, records)[0]
	if s := trendS(t, res); s != 3 {
		t.Errorf("S = %g, want +3 (bucket value order)", s)
	}
}

// TestTrendPost_NamedRangeTableKeyFollowsSuppliedOrder: a
// GROUP_DATE_RANGES `table:` source resolves through the extension hook
// before its key order is read, so a named table's labels trend in the
// table's supplied order too.
func TestTrendPost_NamedRangeTableKeyFollowsSuppliedOrder(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 2, 15), "score": 1}), // launch
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 6, 1), "score": 2}),  // growth
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 12, 1), "score": 3}), // steady
	}
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: tableParams(t, "fiscal")}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"}},
		PostTests:    []*types.Test{{Type: types.TEST_TREND, Field: "mean", OrderBy: []types.OrderKey{{Field: "enrolled"}}}},
	}
	resp, err := NewProcessorWithExtensions(schema, fiscalTableRegistry()).Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if s := trendS(t, resp.PostTests[0]); s != 3 {
		t.Errorf("S = %g, want +3 (table's supplied order)", s)
	}
}
