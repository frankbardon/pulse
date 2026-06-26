package processing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeGrouper(t *testing.T, groupType types.GroupType, field string, interval float64, schema *encoding.Schema) Grouper {
	t.Helper()
	factory, ok := grouperRegistry[groupType]
	if !ok {
		t.Fatalf("no grouper registered for %s", groupType)
	}
	g, err := factory(&types.Group{Type: groupType, Field: field, Interval: interval}, schema)
	if err != nil {
		t.Fatalf("create grouper: %v", err)
	}
	return g
}

func TestGrouper_Category_NumericField(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	records := makeRecords(schema, "age", []float64{25, 30, 25, 30, 25})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2", len(groups))
	}
	if len(groups["25"]) != 3 {
		t.Errorf("group 25 count = %d, want 3", len(groups["25"]))
	}
	if len(groups["30"]) != 2 {
		t.Errorf("group 30 count = %d, want 2", len(groups["30"]))
	}
}

func TestGrouper_Category_CategoricalField(t *testing.T) {
	schema := categoricalSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "brand", 0, schema)

	records := make([]*Record, 5)
	records[0] = NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	records[1] = NewRecord(schema, map[string]float64{"brand": 1, "score": 20})
	records[2] = NewRecord(schema, map[string]float64{"brand": 0, "score": 30})
	records[3] = NewRecord(schema, map[string]float64{"brand": 2, "score": 40})
	records[4] = NewRecord(schema, map[string]float64{"brand": 1, "score": 50})

	groups, err := g.Group(records, "brand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Keys should be resolved strings, not IDs
	if len(groups) != 3 {
		t.Fatalf("group count = %d, want 3", len(groups))
	}
	if len(groups["Apple"]) != 2 {
		t.Errorf("Apple group count = %d, want 2", len(groups["Apple"]))
	}
	if len(groups["Samsung"]) != 2 {
		t.Errorf("Samsung group count = %d, want 2", len(groups["Samsung"]))
	}
	if len(groups["Google"]) != 1 {
		t.Errorf("Google group count = %d, want 1", len(groups["Google"]))
	}
}

func TestGrouper_Category_Empty(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	groups, err := g.Group([]*Record{}, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("group count = %d, want 0", len(groups))
	}
}

func TestGrouper_Category_NullHandling(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_CATEGORY, "age", 0, schema)

	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"age": 25})
	records[1] = NewRecordWithNulls(schema, map[string]float64{"age": 0}, map[string]bool{"age": true})
	records[2] = NewRecord(schema, map[string]float64{"age": 25})

	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null records should go to a null group or be skipped
	if len(groups["25"]) != 2 {
		t.Errorf("group 25 count = %d, want 2", len(groups["25"]))
	}
}

func TestGrouper_Rounded_Basic(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "age", 10, schema)

	records := makeRecords(schema, "age", []float64{23, 27, 35, 12, 48})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 23,27 => 20; 35 => 30; 12 => 10; 48 => 40
	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["20"]) != 2 {
		t.Errorf("group 20 count = %d, want 2", len(groups["20"]))
	}
	if len(groups["30"]) != 1 {
		t.Errorf("group 30 count = %d, want 1", len(groups["30"]))
	}
	if len(groups["10"]) != 1 {
		t.Errorf("group 10 count = %d, want 1", len(groups["10"]))
	}
	if len(groups["40"]) != 1 {
		t.Errorf("group 40 count = %d, want 1", len(groups["40"]))
	}
}

func TestGrouper_Rounded_IntervalOf5(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 5, schema)

	records := makeRecords(schema, "score", []float64{3, 7, 12, 14, 18})
	groups, err := g.Group(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3 => 0; 7 => 5; 12,14 => 10; 18 => 15
	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
}

func TestGrouper_Rounded_Empty(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_ROUNDED, "score", 10, schema)

	groups, err := g.Group([]*Record{}, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("group count = %d, want 0", len(groups))
	}
}

func TestGrouper_Range_Basic(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "age", 10, schema)

	records := makeRecords(schema, "age", []float64{5, 15, 25, 35, 95})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 5 {
		t.Fatalf("group count = %d, want 5, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["0-10"]) != 1 {
		t.Errorf("group 0-10 count = %d, want 1", len(groups["0-10"]))
	}
	if len(groups["10-20"]) != 1 {
		t.Errorf("group 10-20 count = %d, want 1", len(groups["10-20"]))
	}
	if len(groups["20-30"]) != 1 {
		t.Errorf("group 20-30 count = %d, want 1", len(groups["20-30"]))
	}
	if len(groups["30-40"]) != 1 {
		t.Errorf("group 30-40 count = %d, want 1", len(groups["30-40"]))
	}
	if len(groups["90-100"]) != 1 {
		t.Errorf("group 90-100 count = %d, want 1", len(groups["90-100"]))
	}
}

func TestGrouper_Range_FloatInterval(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "score", 0.5, schema)

	records := makeRecords(schema, "score", []float64{0.1, 0.3, 0.6, 0.8, 1.2})
	groups, err := g.Group(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 3 {
		t.Fatalf("group count = %d, want 3, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["0-0.5"]) != 2 {
		t.Errorf("group 0-0.5 count = %d, want 2", len(groups["0-0.5"]))
	}
	if len(groups["0.5-1"]) != 2 {
		t.Errorf("group 0.5-1 count = %d, want 2", len(groups["0.5-1"]))
	}
	if len(groups["1-1.5"]) != 1 {
		t.Errorf("group 1-1.5 count = %d, want 1", len(groups["1-1.5"]))
	}
}

func TestGrouper_Range_NegativeValues(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "score", 10, schema)

	records := makeRecords(schema, "score", []float64{-5, -15, 5, 15})
	groups, err := g.Group(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["-10-0"]) != 1 {
		t.Errorf("group -10-0 count = %d, want 1", len(groups["-10-0"]))
	}
	if len(groups["-20--10"]) != 1 {
		t.Errorf("group -20--10 count = %d, want 1", len(groups["-20--10"]))
	}
	if len(groups["0-10"]) != 1 {
		t.Errorf("group 0-10 count = %d, want 1", len(groups["0-10"]))
	}
	if len(groups["10-20"]) != 1 {
		t.Errorf("group 10-20 count = %d, want 1", len(groups["10-20"]))
	}
}

func TestGrouper_Range_DefaultInterval(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "age", 0, schema)

	records := makeRecords(schema, "age", []float64{0.5, 1.5, 2.5})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// interval defaults to 1, so bins are 0-1, 1-2, 2-3
	if len(groups) != 3 {
		t.Fatalf("group count = %d, want 3, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["0-1"]) != 1 {
		t.Errorf("group 0-1 count = %d, want 1", len(groups["0-1"]))
	}
	if len(groups["1-2"]) != 1 {
		t.Errorf("group 1-2 count = %d, want 1", len(groups["1-2"]))
	}
	if len(groups["2-3"]) != 1 {
		t.Errorf("group 2-3 count = %d, want 1", len(groups["2-3"]))
	}
}

func TestGrouper_Range_SingleRecord(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "age", 10, schema)

	records := makeRecords(schema, "age", []float64{42})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("group count = %d, want 1, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["40-50"]) != 1 {
		t.Errorf("group 40-50 count = %d, want 1", len(groups["40-50"]))
	}
}

func TestGrouper_Range_Empty(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "age", 10, schema)

	groups, err := g.Group([]*Record{}, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("group count = %d, want 0", len(groups))
	}
}

func TestGrouper_Range_NullHandling(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_RANGE, "age", 10, schema)

	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"age": 25})
	records[1] = NewRecordWithNulls(schema, map[string]float64{"age": 0}, map[string]bool{"age": true})
	records[2] = NewRecord(schema, map[string]float64{"age": 25})

	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only non-null records should be grouped
	if len(groups["20-30"]) != 2 {
		t.Errorf("group 20-30 count = %d, want 2", len(groups["20-30"]))
	}
	// Null record should be skipped entirely
	if len(groups) != 1 {
		t.Errorf("group count = %d, want 1 (nulls skipped), groups: %v", len(groups), groupKeys(groups))
	}
}

func TestGrouper_Quantile_Quartiles(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "age", 4, schema)

	records := makeRecords(schema, "age", []float64{10, 20, 30, 40, 50, 60, 70, 80})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
	for _, key := range []string{"Q1", "Q2", "Q3", "Q4"} {
		if len(groups[key]) != 2 {
			t.Errorf("group %s count = %d, want 2", key, len(groups[key]))
		}
	}
}

func TestGrouper_Quantile_Deciles(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "age", 10, schema)

	values := make([]float64, 20)
	for i := range values {
		values[i] = float64(i + 1)
	}
	records := makeRecords(schema, "age", values)
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 10 {
		t.Fatalf("group count = %d, want 10, groups: %v", len(groups), groupKeys(groups))
	}
	for i := 1; i <= 10; i++ {
		key := "D" + strconv.Itoa(i)
		if len(groups[key]) != 2 {
			t.Errorf("group %s count = %d, want 2", key, len(groups[key]))
		}
	}
}

func TestGrouper_Quantile_DefaultInterval(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "age", 0, schema)

	records := makeRecords(schema, "age", []float64{10, 20, 30, 40, 50, 60, 70, 80})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Default is 4 buckets (quartiles)
	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
	for _, key := range []string{"Q1", "Q2", "Q3", "Q4"} {
		if _, ok := groups[key]; !ok {
			t.Errorf("expected group %s to exist", key)
		}
	}
}

func TestGrouper_Quantile_UnevenDistribution(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "age", 4, schema)

	// 10 records into 4 buckets: can't divide evenly
	records := makeRecords(schema, "age", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}

	// All 10 records should be accounted for
	total := 0
	for _, recs := range groups {
		total += len(recs)
	}
	if total != 10 {
		t.Errorf("total records = %d, want 10", total)
	}
}

func TestGrouper_Quantile_SingleRecord(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "age", 4, schema)

	records := makeRecords(schema, "age", []float64{42})
	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("group count = %d, want 1, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["Q1"]) != 1 {
		t.Errorf("group Q1 count = %d, want 1", len(groups["Q1"]))
	}
}

func TestGrouper_Quantile_Empty(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "age", 4, schema)

	groups, err := g.Group([]*Record{}, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("group count = %d, want 0", len(groups))
	}
}

func TestGrouper_Quantile_NullHandling(t *testing.T) {
	schema := numericSchema()
	g := makeGrouper(t, types.GROUP_QUANTILE, "age", 4, schema)

	records := make([]*Record, 5)
	records[0] = NewRecord(schema, map[string]float64{"age": 10})
	records[1] = NewRecordWithNulls(schema, map[string]float64{"age": 0}, map[string]bool{"age": true})
	records[2] = NewRecord(schema, map[string]float64{"age": 20})
	records[3] = NewRecord(schema, map[string]float64{"age": 30})
	records[4] = NewRecord(schema, map[string]float64{"age": 40})

	groups, err := g.Group(records, "age")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only 4 non-null records; null should be skipped
	total := 0
	for _, recs := range groups {
		total += len(recs)
	}
	if total != 4 {
		t.Errorf("total records = %d, want 4 (null skipped)", total)
	}
}

func TestGrouper_Quantile_KeyPrefixes(t *testing.T) {
	schema := numericSchema()

	tests := []struct {
		name     string
		interval float64
		prefix   string
	}{
		{"quartiles", 4, "Q"},
		{"deciles", 10, "D"},
		{"other", 5, "B"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := makeGrouper(t, types.GROUP_QUANTILE, "age", tc.interval, schema)

			// Use enough records to fill all buckets
			values := make([]float64, int(tc.interval)*2)
			for i := range values {
				values[i] = float64(i + 1)
			}
			records := makeRecords(schema, "age", values)
			groups, err := g.Group(records, "age")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for key := range groups {
				if key[0] != tc.prefix[0] {
					t.Errorf("key %q does not start with prefix %q", key, tc.prefix)
				}
			}
		})
	}
}

func epochDays(year int, month time.Month, day int) float64 {
	return float64(time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func makeDateGrouper(t *testing.T, component string, schema *encoding.Schema) Grouper {
	t.Helper()
	factory, ok := grouperRegistry[types.GROUP_DATE]
	if !ok {
		t.Fatalf("no grouper registered for GROUP_DATE")
	}
	var params json.RawMessage
	if component != "" {
		params = json.RawMessage(`{"component":"` + component + `"}`)
	}
	g, err := factory(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: params}, schema)
	if err != nil {
		t.Fatalf("create grouper: %v", err)
	}
	return g
}

func TestGrouper_Date_Month(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "month", schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 1, 15),
		epochDays(2024, 1, 20),
		epochDays(2024, 2, 10),
		epochDays(2024, 3, 5),
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("group count = %d, want 3, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["2024-01"]) != 2 {
		t.Errorf("group 2024-01 count = %d, want 2", len(groups["2024-01"]))
	}
	if len(groups["2024-02"]) != 1 {
		t.Errorf("group 2024-02 count = %d, want 1", len(groups["2024-02"]))
	}
	if len(groups["2024-03"]) != 1 {
		t.Errorf("group 2024-03 count = %d, want 1", len(groups["2024-03"]))
	}
}

func TestGrouper_Date_Year(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "year", schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2023, 6, 1),
		epochDays(2024, 3, 15),
		epochDays(2024, 9, 20),
		epochDays(2025, 1, 1),
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("group count = %d, want 3, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["2023"]) != 1 {
		t.Errorf("group 2023 count = %d, want 1", len(groups["2023"]))
	}
	if len(groups["2024"]) != 2 {
		t.Errorf("group 2024 count = %d, want 2", len(groups["2024"]))
	}
	if len(groups["2025"]) != 1 {
		t.Errorf("group 2025 count = %d, want 1", len(groups["2025"]))
	}
}

func TestGrouper_Date_Quarter(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "quarter", schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 1, 15), // Q1
		epochDays(2024, 4, 10), // Q2
		epochDays(2024, 7, 20), // Q3
		epochDays(2024, 10, 5), // Q4
		epochDays(2024, 2, 28), // Q1
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 4 {
		t.Fatalf("group count = %d, want 4, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["2024-Q1"]) != 2 {
		t.Errorf("group 2024-Q1 count = %d, want 2", len(groups["2024-Q1"]))
	}
	if len(groups["2024-Q2"]) != 1 {
		t.Errorf("group 2024-Q2 count = %d, want 1", len(groups["2024-Q2"]))
	}
	if len(groups["2024-Q3"]) != 1 {
		t.Errorf("group 2024-Q3 count = %d, want 1", len(groups["2024-Q3"]))
	}
	if len(groups["2024-Q4"]) != 1 {
		t.Errorf("group 2024-Q4 count = %d, want 1", len(groups["2024-Q4"]))
	}
}

func TestGrouper_Date_DayOfWeek(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "day_of_week", schema)

	// 2024-01-15 is a Monday, 2024-01-16 is Tuesday, 2024-01-22 is Monday
	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 1, 15),
		epochDays(2024, 1, 16),
		epochDays(2024, 1, 22),
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["Monday"]) != 2 {
		t.Errorf("group Monday count = %d, want 2", len(groups["Monday"]))
	}
	if len(groups["Tuesday"]) != 1 {
		t.Errorf("group Tuesday count = %d, want 1", len(groups["Tuesday"]))
	}
}

func TestGrouper_Date_Week(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "week", schema)

	// 2024-01-15 is ISO week 3, 2024-01-22 is ISO week 4
	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 1, 15),
		epochDays(2024, 1, 17),
		epochDays(2024, 1, 22),
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["2024-W03"]) != 2 {
		t.Errorf("group 2024-W03 count = %d, want 2", len(groups["2024-W03"]))
	}
	if len(groups["2024-W04"]) != 1 {
		t.Errorf("group 2024-W04 count = %d, want 1", len(groups["2024-W04"]))
	}
}

func TestGrouper_Date_Day(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "day", schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 1, 15),
		epochDays(2024, 1, 15),
		epochDays(2024, 1, 16),
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2, groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["2024-01-15"]) != 2 {
		t.Errorf("group 2024-01-15 count = %d, want 2", len(groups["2024-01-15"]))
	}
	if len(groups["2024-01-16"]) != 1 {
		t.Errorf("group 2024-01-16 count = %d, want 1", len(groups["2024-01-16"]))
	}
}

func TestGrouper_Date_DefaultComponent(t *testing.T) {
	schema := dateSchema()
	factory, ok := grouperRegistry[types.GROUP_DATE]
	if !ok {
		t.Fatalf("no grouper registered for GROUP_DATE")
	}
	// nil params should default to month
	g, err := factory(&types.Group{Type: types.GROUP_DATE, Field: "enrolled"}, schema)
	if err != nil {
		t.Fatalf("create grouper: %v", err)
	}

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 1, 15),
		epochDays(2024, 2, 10),
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("group count = %d, want 2, groups: %v", len(groups), groupKeys(groups))
	}
	if _, ok := groups["2024-01"]; !ok {
		t.Errorf("expected group 2024-01 to exist")
	}
	if _, ok := groups["2024-02"]; !ok {
		t.Errorf("expected group 2024-02 to exist")
	}
}

func TestGrouper_Date_InvalidComponent(t *testing.T) {
	schema := dateSchema()
	factory, ok := grouperRegistry[types.GROUP_DATE]
	if !ok {
		t.Fatalf("no grouper registered for GROUP_DATE")
	}
	params := json.RawMessage(`{"component":"century"}`)
	_, err := factory(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: params}, schema)
	if err == nil {
		t.Fatalf("expected error for invalid component, got nil")
	}
}

func TestGrouper_Date_Empty(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "month", schema)

	groups, err := g.Group([]*Record{}, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("group count = %d, want 0", len(groups))
	}
}

func TestGrouper_Date_NullHandling(t *testing.T) {
	schema := dateSchema()
	g := makeDateGrouper(t, "month", schema)

	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 1, 15)})
	records[1] = NewRecordWithNulls(schema, map[string]float64{"enrolled": 0}, map[string]bool{"enrolled": true})
	records[2] = NewRecord(schema, map[string]float64{"enrolled": epochDays(2024, 1, 20)})

	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("group count = %d, want 1 (nulls skipped), groups: %v", len(groups), groupKeys(groups))
	}
	if len(groups["2024-01"]) != 2 {
		t.Errorf("group 2024-01 count = %d, want 2", len(groups["2024-01"]))
	}
}

// makeFiscalDateGrouper constructs a GROUP_DATE with the given component and
// fiscal_offset; passing component="" defaults to month.
func makeFiscalDateGrouper(t *testing.T, component string, fiscalOffset int, schema *encoding.Schema) Grouper {
	t.Helper()
	factory, ok := grouperRegistry[types.GROUP_DATE]
	if !ok {
		t.Fatalf("no grouper registered for GROUP_DATE")
	}
	if component == "" {
		component = "month"
	}
	params := json.RawMessage(fmt.Sprintf(`{"component":%q,"fiscal_offset":%d}`, component, fiscalOffset))
	g, err := factory(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: params}, schema)
	if err != nil {
		t.Fatalf("create grouper: %v", err)
	}
	return g
}

// TestGrouper_Date_FiscalQuarter_AprilStart pins the canonical UK-style
// fiscal calendar (offset=3 => April-start FY) with end-year labels: a date
// inside April-June 2024 lands in FY2025-Q1, March 2024 lands in the
// preceding FY2024-Q4, and December 2024 (still inside FY2025) lands in
// FY2025-Q3.
func TestGrouper_Date_FiscalQuarter_AprilStart(t *testing.T) {
	schema := dateSchema()
	g := makeFiscalDateGrouper(t, "quarter", 3, schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 4, 1),   // FY2025-Q1 (Apr-Jun)
		epochDays(2024, 6, 30),  // FY2025-Q1
		epochDays(2024, 7, 1),   // FY2025-Q2 (Jul-Sep)
		epochDays(2024, 12, 31), // FY2025-Q3 (Oct-Dec)
		epochDays(2025, 3, 31),  // FY2025-Q4 (Jan-Mar)
		epochDays(2024, 3, 31),  // FY2024-Q4 (preceding fiscal year)
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]int{
		"FY2025-Q1": 2,
		"FY2025-Q2": 1,
		"FY2025-Q3": 1,
		"FY2025-Q4": 1,
		"FY2024-Q4": 1,
	}
	if len(groups) != len(want) {
		t.Fatalf("group count = %d, want %d, groups: %v", len(groups), len(want), groupKeys(groups))
	}
	for k, n := range want {
		if len(groups[k]) != n {
			t.Errorf("group %s count = %d, want %d", k, len(groups[k]), n)
		}
	}
}

// TestGrouper_Date_FiscalYear_OctoberStart pins US-Federal-style FY: Oct 1
// 2023 through Sep 30 2024 is FY2024, Oct 1 2024 flips to FY2025.
func TestGrouper_Date_FiscalYear_OctoberStart(t *testing.T) {
	schema := dateSchema()
	g := makeFiscalDateGrouper(t, "year", 9, schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2023, 10, 1), // FY2024
		epochDays(2024, 9, 30), // FY2024
		epochDays(2024, 10, 1), // FY2025
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups["FY2024"]) != 2 {
		t.Errorf("FY2024 count = %d, want 2", len(groups["FY2024"]))
	}
	if len(groups["FY2025"]) != 1 {
		t.Errorf("FY2025 count = %d, want 1", len(groups["FY2025"]))
	}
}

// TestGrouper_Date_FiscalOffset_NegativeEquivalence asserts that offset=-3
// (3 months earlier) and offset=9 (October start) produce identical
// fiscal-year bucketing. Both should land Oct 1 2024 in FY2025.
func TestGrouper_Date_FiscalOffset_NegativeEquivalence(t *testing.T) {
	schema := dateSchema()
	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2023, 10, 1),
		epochDays(2024, 9, 30),
		epochDays(2024, 10, 1),
	})

	gNeg := makeFiscalDateGrouper(t, "year", -3, schema)
	gPos := makeFiscalDateGrouper(t, "year", 9, schema)

	groupsNeg, err := gNeg.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("neg group: %v", err)
	}
	groupsPos, err := gPos.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("pos group: %v", err)
	}
	for _, k := range []string{"FY2024", "FY2025"} {
		if len(groupsNeg[k]) != len(groupsPos[k]) {
			t.Errorf("key %s: neg=%d pos=%d (should match)", k, len(groupsNeg[k]), len(groupsPos[k]))
		}
	}
}

// TestGrouper_Date_FiscalOffset_ZeroIsCalendar asserts that fiscal_offset=0
// is byte-identical to the no-fiscal_offset path: keys must be bare years
// (no FY prefix) so existing behaviour is preserved.
func TestGrouper_Date_FiscalOffset_ZeroIsCalendar(t *testing.T) {
	schema := dateSchema()
	g := makeFiscalDateGrouper(t, "year", 0, schema)

	records := makeRecords(schema, "enrolled", []float64{
		epochDays(2024, 1, 15),
		epochDays(2025, 6, 1),
	})
	groups, err := g.Group(records, "enrolled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := groups["2024"]; !ok {
		t.Errorf("offset=0 year key should be %q (no FY prefix), got %v", "2024", groupKeys(groups))
	}
	if _, ok := groups["FY2024"]; ok {
		t.Errorf("offset=0 must not emit FY-prefixed key")
	}
}

func TestGrouper_Date_FiscalOffset_OutOfRange(t *testing.T) {
	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE]
	for _, off := range []int{-12, 12, 50, -50} {
		params := json.RawMessage(fmt.Sprintf(`{"component":"year","fiscal_offset":%d}`, off))
		_, err := factory(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: params}, schema)
		if err == nil {
			t.Errorf("expected error for fiscal_offset=%d, got nil", off)
		}
	}
}

func TestGrouper_Date_FiscalOffset_InvalidComponent(t *testing.T) {
	schema := dateSchema()
	factory := grouperRegistry[types.GROUP_DATE]
	for _, comp := range []string{"month", "week", "day", "day_of_week"} {
		params := json.RawMessage(fmt.Sprintf(`{"component":%q,"fiscal_offset":3}`, comp))
		_, err := factory(&types.Group{Type: types.GROUP_DATE, Field: "enrolled", Params: params}, schema)
		if err == nil {
			t.Errorf("expected error for fiscal_offset with component=%s, got nil", comp)
		}
	}
}

func groupKeys(m map[string][]*Record) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
