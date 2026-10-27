package processing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Request.Sort and window ORDER BY on a grouper key column follow that
// grouper's intrinsic key order (groupKeyOrders) — day_of_week
// Monday-first, GROUP_DATE_RANGES labels in supplied order, numeric
// buckets by value — exactly as TEST_TREND's order_by does, never
// byte-wise. An ISO key and every non-grouper column order as before.

var isoWeek = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// sortArms answers req on the buffered arm (processRecords) and the
// streaming arm (Process over a streamable grouped request, the shared
// FinalizeGroupedStream tail) and returns both responses.
func sortArms(t *testing.T, schema *encoding.Schema, req *types.Request, records []*Record, exts *ExtensionRegistry) (buffered, streamed *types.Response) {
	t.Helper()
	var err error
	if buffered, err = NewProcessorWithExtensions(schema, exts).processRecords(context.Background(), req, records); err != nil {
		t.Fatalf("buffered: %v", err)
	}
	if streamed, err = NewProcessorWithExtensions(schema, exts).Process(context.Background(), req, NewSliceIterator(records)); err != nil {
		t.Fatalf("streaming: %v", err)
	}
	return buffered, streamed
}

func checkSortedKeys(t *testing.T, schema *encoding.Schema, groups []*types.Group, col string, records []*Record, exts *ExtensionRegistry, asc []string) {
	t.Helper()
	for _, desc := range []bool{false, true} {
		req := &types.Request{
			Groups:       groups,
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
			Sort:         []types.OrderKey{{Field: col, Desc: desc}},
		}
		want := asc
		if desc {
			want = reversed(asc)
		}
		buf, str := sortArms(t, schema, req, records, exts)
		if got := orderedKeys(buf.Data, col); !sliceEqual(got, want) {
			t.Errorf("desc=%v buffered order = %v, want %v", desc, got, want)
		}
		if got := orderedKeys(str.Data, col); !sliceEqual(got, want) {
			t.Errorf("desc=%v streaming order = %v, want %v", desc, got, want)
		}
	}
}

// TestRequestSort_DayOfWeekKeyFollowsGrouperOrder: sort on a GROUP_DATE
// day_of_week key column is Monday -> Sunday ascending, Sunday ->
// Monday descending, on the buffered and streaming arms alike.
func TestRequestSort_DayOfWeekKeyFollowsGrouperOrder(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	groups := []*types.Group{{Type: types.GROUP_DATE, Field: "enrolled",
		Params: json.RawMessage(`{"component":"day_of_week"}`)}}
	checkSortedKeys(t, schema, groups, "enrolled", weekRecords(schema), nil, isoWeek)
}

// TestRequestSort_DateRangesKeyFollowsSuppliedOrder: inline ranges and
// a named range table sort in supplied order (launch, growth, steady),
// not alphabetically.
func TestRequestSort_DateRangesKeyFollowsSuppliedOrder(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	records := []*Record{
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 12, 1), "score": 3}), // steady
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 2, 15), "score": 1}), // launch
		NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 6, 1), "score": 2}),  // growth
	}
	want := []string{"launch", "growth", "steady"}
	t.Run("inline", func(t *testing.T) {
		groups := []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: phaseRangesParams(t, "")}}
		checkSortedKeys(t, schema, groups, "enrolled", records, nil, want)
	})
	t.Run("table", func(t *testing.T) {
		groups := []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "enrolled", Params: tableParams(t, "fiscal")}}
		checkSortedKeys(t, schema, groups, "enrolled", records, fiscalTableRegistry(), want)
	})
}

// TestRequestSort_NumericBucketKeyFollowsValue: GROUP_ROUNDED keys sort
// by value ("5", "9.5"... "10"), not as strings ("10" before "5").
func TestRequestSort_NumericBucketKeyFollowsValue(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	var records []*Record
	for _, v := range []float64{10, 5, 15} {
		records = append(records, NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 1, 15), "score": v}))
	}
	groups := []*types.Group{{Type: types.GROUP_ROUNDED, Field: "score", Interval: 5}}
	checkSortedKeys(t, schema, groups, "score", records, nil, []string{"5", "10", "15"})
}

// TestRequestSort_ISODateKeyUnchanged: an ISO day key (the grouper
// ranks none) sorts byte-wise — chronologically — exactly as before.
func TestRequestSort_ISODateKeyUnchanged(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	groups := []*types.Group{{Type: types.GROUP_DATE, Field: "enrolled", Params: json.RawMessage(`{"component":"day"}`)}}
	want := []string{"2024-01-15", "2024-01-16", "2024-01-17", "2024-01-18", "2024-01-19", "2024-01-20", "2024-01-21"}
	checkSortedKeys(t, schema, groups, "enrolled", weekRecords(schema), nil, want)
}

// windowOverWeek runs one window over the day_of_week grouped rows
// (mean score 1 Monday .. 7 Sunday) and returns the window column keyed
// by weekday.
func windowOverWeek(t *testing.T, w *types.Window) map[string]any {
	t.Helper()
	schema := dateSchema()
	req := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "enrolled",
			Params: json.RawMessage(`{"component":"day_of_week"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"}},
		Windows:      []*types.Window{w},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(weekRecords(schema)))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	out := make(map[string]any, len(resp.Data))
	for _, row := range resp.Data {
		out[row["enrolled"].(string)] = row[w.Label]
	}
	return out
}

// runningFrame is the cumulative frame a running window requires.
func runningFrame() *types.FrameSpec {
	zero := 0
	return &types.FrameSpec{Mode: "rows", Following: &zero}
}

func asF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return -1
}

// TestWindowOrderBy_DayOfWeekKeyFollowsGrouperOrder: WIN_LAG,
// WIN_RUNNING_SUM, WIN_RANK and WIN_DENSE_RANK ordered by a day_of_week
// key walk Monday -> Sunday (descending reverses), never Friday-first.
func TestWindowOrderBy_DayOfWeekKeyFollowsGrouperOrder(t *testing.T) {
	t.Parallel()

	asc := []types.OrderKey{{Field: "enrolled"}}
	desc := []types.OrderKey{{Field: "enrolled", Desc: true}}

	lag := windowOverWeek(t, &types.Window{Type: types.WIN_LAG, Field: "mean", OrderBy: asc, Label: "prev"})
	if lag["Monday"] != nil {
		t.Errorf("WIN_LAG Monday = %v, want null (first in order)", lag["Monday"])
	}
	for i := 1; i < 7; i++ {
		if got := asF(lag[isoWeek[i]]); got != float64(i) {
			t.Errorf("WIN_LAG %s = %v, want %d", isoWeek[i], lag[isoWeek[i]], i)
		}
	}

	run := windowOverWeek(t, &types.Window{Type: types.WIN_RUNNING_SUM, Field: "mean", OrderBy: asc, Label: "cum", Frame: runningFrame()})
	for i := 0; i < 7; i++ {
		if got, want := asF(run[isoWeek[i]]), float64((i+1)*(i+2)/2); got != want {
			t.Errorf("WIN_RUNNING_SUM %s = %v, want %g", isoWeek[i], run[isoWeek[i]], want)
		}
	}
	runDesc := windowOverWeek(t, &types.Window{Type: types.WIN_RUNNING_SUM, Field: "mean", OrderBy: desc, Label: "cum", Frame: runningFrame()})
	if got := asF(runDesc["Sunday"]); got != 7 {
		t.Errorf("desc WIN_RUNNING_SUM Sunday = %v, want 7 (first under desc)", runDesc["Sunday"])
	}

	for _, typ := range []types.WindowType{types.WIN_RANK, types.WIN_DENSE_RANK} {
		rank := windowOverWeek(t, &types.Window{Type: typ, OrderBy: asc, Label: "r"})
		for i := 0; i < 7; i++ {
			if got := asF(rank[isoWeek[i]]); got != float64(i+1) {
				t.Errorf("%s %s = %v, want %d", typ, isoWeek[i], rank[isoWeek[i]], i+1)
			}
		}
		rankDesc := windowOverWeek(t, &types.Window{Type: typ, OrderBy: desc, Label: "r"})
		if got := asF(rankDesc["Sunday"]); got != 1 {
			t.Errorf("desc %s Sunday = %v, want 1", typ, rankDesc["Sunday"])
		}
	}
}

// TestWindowRank_GrouperKeyTiesFollowComparator: on a site x day_of_week
// composite, WIN_RANK / WIN_DENSE_RANK ordered by the weekday key tie the
// two sites' same weekday and rank weekdays Monday-first: RANK
// 1,1,3,3,... and DENSE_RANK 1,1,2,2,...
func TestWindowRank_GrouperKeyTiesFollowComparator(t *testing.T) {
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
	for _, tc := range []struct {
		typ  types.WindowType
		rank func(day int) float64
	}{
		{types.WIN_RANK, func(d int) float64 { return float64(2*d + 1) }},
		{types.WIN_DENSE_RANK, func(d int) float64 { return float64(d + 1) }},
	} {
		req := &types.Request{
			Groups: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "site"},
				{Type: types.GROUP_DATE, Field: "enrolled", Params: json.RawMessage(`{"component":"day_of_week"}`)},
			},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
			Windows:      []*types.Window{{Type: tc.typ, OrderBy: []types.OrderKey{{Field: "enrolled"}}, Label: "r"}},
		}
		resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(records))
		if err != nil {
			t.Fatalf("%s Process: %v", tc.typ, err)
		}
		if len(resp.Data) != 14 {
			t.Fatalf("%s rows = %d, want 14", tc.typ, len(resp.Data))
		}
		for _, row := range resp.Data {
			day := -1
			for i, d := range isoWeek {
				if row["enrolled"] == d {
					day = i
				}
			}
			if got, want := asF(row["r"]), tc.rank(day); got != want {
				t.Errorf("%s %v/%v = %v, want %g", tc.typ, row["site"], row["enrolled"], row["r"], want)
			}
		}
	}
}

// TestWindowOrderBy_NumericBucketKeyFollowsValue: WIN_RANK over
// GROUP_ROUNDED keys "5", "10", "15" rank by
// value (1, 2, 3), not as strings ("10" first).
func TestWindowOrderBy_NumericBucketKeyFollowsValue(t *testing.T) {
	t.Parallel()

	schema := dateSchema()
	var records []*Record
	for _, v := range []float64{10, 5, 15} {
		records = append(records, NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 1, 15), "score": v}))
	}
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_ROUNDED, Field: "score", Interval: 5}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		Windows:      []*types.Window{{Type: types.WIN_RANK, OrderBy: []types.OrderKey{{Field: "score"}}, Label: "r"}},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want := map[string]float64{"5": 1, "10": 2, "15": 3}
	for _, row := range resp.Data {
		k := row["score"].(string)
		if got := asF(row["r"]); got != want[k] {
			t.Errorf("WIN_RANK %s = %v, want %g", k, row["r"], want[k])
		}
	}
}

// TestRequestSort_GroupedMatricesFollowGrouperKeyOrder: a grouped
// run's per-bucket matrices render over the sorted Data order
// (sortGroupedRows), so a sort on a GROUP_ROUNDED key orders them by
// bucket value on the buffered and streaming arms alike.
func TestRequestSort_GroupedMatricesFollowGrouperKeyOrder(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}
	var records []*Record
	for _, v := range []float64{10, 5, 15} {
		for j := 0; j < 3; j++ {
			r := NewRecord(schema, map[string]float64{"x": v + float64(j)*0.5, "y": float64(j)})
			r.SetMergePosition(0, 0, len(records))
			records = append(records, r)
		}
	}
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_ROUNDED, Field: "x", Interval: 5}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"x", "y"}}},
		Sort:         []types.OrderKey{{Field: "x"}},
	}
	buf, str := sortArms(t, schema, req, records, nil)
	want := []string{"5", "10", "15"}
	for name, resp := range map[string]*types.Response{"buffered": buf, "streaming": str} {
		if got := orderedKeys(resp.Data, "x"); !sliceEqual(got, want) {
			t.Errorf("%s Data order = %v, want %v", name, got, want)
		}
		if len(resp.Matrices) != 3 {
			t.Fatalf("%s matrices = %d, want 3", name, len(resp.Matrices))
		}
		for i, m := range resp.Matrices {
			if len(m.GroupKey) != 1 || m.GroupKey[0] != want[i] {
				t.Errorf("%s matrix %d group_key = %v, want [%s]", name, i, m.GroupKey, want[i])
			}
		}
	}
}
