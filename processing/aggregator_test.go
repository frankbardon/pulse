package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func numericSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
			{Name: "age", Type: encoding.FieldTypeU8},
			{Name: "value", Type: encoding.FieldTypeF64},
		},
	}
}

func categoricalSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	dict.Add("Apple")
	dict.Add("Samsung")
	dict.Add("Google")
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
}

func makeRecords(schema *encoding.Schema, fieldName string, values []float64) []*Record {
	records := make([]*Record, len(values))
	for i, v := range values {
		records[i] = NewRecord(schema, map[string]float64{fieldName: v})
	}
	return records
}

func makeRecordsWithNulls(schema *encoding.Schema, fieldName string, values []float64, nullIdxs []int) []*Record {
	nullSet := make(map[int]bool)
	for _, idx := range nullIdxs {
		nullSet[idx] = true
	}
	records := make([]*Record, len(values))
	for i, v := range values {
		nulls := map[string]bool{}
		if nullSet[i] {
			nulls[fieldName] = true
		}
		records[i] = NewRecordWithNulls(schema, map[string]float64{fieldName: v}, nulls)
	}
	return records
}

func floatClose(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// --- Average ---

func TestAggregator_Average_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_AVERAGE, "score", schema)
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 20.0, 0.001) {
		t.Errorf("average = %f, want 20.0", result)
	}
}

func TestAggregator_Average_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_AVERAGE, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 42.0, 0.001) {
		t.Errorf("average = %f, want 42.0", result)
	}
}

func TestAggregator_Average_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_AVERAGE, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("average of empty = %f, want 0", result)
	}
}

func TestAggregator_Average_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_AVERAGE, "score", schema)
	records := makeRecordsWithNulls(schema, "score", []float64{10, 0, 30}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null at index 1 is skipped; average of [10, 30] = 20
	if !floatClose(result, 20.0, 0.001) {
		t.Errorf("average = %f, want 20.0", result)
	}
}

// --- Sum ---

func TestAggregator_Sum_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SUM, "score", schema)
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 60.0, 0.001) {
		t.Errorf("sum = %f, want 60.0", result)
	}
}

func TestAggregator_Sum_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SUM, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("sum of empty = %f, want 0", result)
	}
}

func TestAggregator_Sum_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SUM, "score", schema)
	records := makeRecords(schema, "score", []float64{99})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 99.0, 0.001) {
		t.Errorf("sum = %f, want 99.0", result)
	}
}

// --- Count ---

func TestAggregator_Count_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_COUNT, "score", schema)
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 3.0, 0.001) {
		t.Errorf("count = %f, want 3.0", result)
	}
}

func TestAggregator_Count_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_COUNT, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("count of empty = %f, want 0", result)
	}
}

func TestAggregator_Count_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_COUNT, "score", schema)
	records := makeRecordsWithNulls(schema, "score", []float64{10, 0, 30}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count skips nulls
	if !floatClose(result, 2.0, 0.001) {
		t.Errorf("count = %f, want 2.0", result)
	}
}

// --- StdDev ---

func TestAggregator_StdDev_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_STDDEV, "score", schema)
	records := makeRecords(schema, "score", []float64{2, 4, 4, 4, 5, 5, 7, 9})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Population stddev of this set is 2.0
	if !floatClose(result, 2.0, 0.01) {
		t.Errorf("stddev = %f, want ~2.0", result)
	}
}

func TestAggregator_StdDev_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_STDDEV, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("stddev of single value = %f, want 0", result)
	}
}

func TestAggregator_StdDev_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_STDDEV, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("stddev of empty = %f, want 0", result)
	}
}

// --- Min ---

func TestAggregator_Min_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MIN, "score", schema)
	records := makeRecords(schema, "score", []float64{30, 10, 20})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 10.0, 0.001) {
		t.Errorf("min = %f, want 10.0", result)
	}
}

func TestAggregator_Min_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MIN, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 42.0, 0.001) {
		t.Errorf("min = %f, want 42.0", result)
	}
}

func TestAggregator_Min_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MIN, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("min of empty = %f, want 0", result)
	}
}

// --- Max ---

func TestAggregator_Max_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MAX, "score", schema)
	records := makeRecords(schema, "score", []float64{30, 10, 20})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 30.0, 0.001) {
		t.Errorf("max = %f, want 30.0", result)
	}
}

func TestAggregator_Max_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MAX, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 42.0, 0.001) {
		t.Errorf("max = %f, want 42.0", result)
	}
}

func TestAggregator_Max_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MAX, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("max of empty = %f, want 0", result)
	}
}

// --- Range ---

func TestAggregator_Range_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_RANGE, "score", schema)
	records := makeRecords(schema, "score", []float64{10, 30, 20})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 20.0, 0.001) {
		t.Errorf("range = %f, want 20.0", result)
	}
}

func TestAggregator_Range_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_RANGE, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("range of single = %f, want 0", result)
	}
}

func TestAggregator_Range_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_RANGE, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("range of empty = %f, want 0", result)
	}
}

// --- ZScore ---

func TestAggregator_ZScore_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_ZSCORE, "score", schema)
	// Mean=5, stddev=~1.58
	records := makeRecords(schema, "score", []float64{3, 5, 7})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// z-score aggregation returns mean z-score = 0
	if !floatClose(result, 0.0, 0.01) {
		t.Errorf("zscore = %f, want ~0.0", result)
	}
}

func TestAggregator_ZScore_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_ZSCORE, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("zscore of empty = %f, want 0", result)
	}
}

// --- Frequency ---

func TestAggregator_Frequency_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_FREQUENCY, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 2, 1, 3, 1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Frequency returns count of most frequent value (1 appears 3 times)
	if !floatClose(result, 3.0, 0.001) {
		t.Errorf("frequency = %f, want 3.0", result)
	}
}

func TestAggregator_Frequency_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_FREQUENCY, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("frequency of empty = %f, want 0", result)
	}
}

// --- Categorical ---

func TestAggregator_OnCategoricalField(t *testing.T) {
	schema := categoricalSchema()
	agg := makeAggregator(t, types.AGG_AVERAGE, "brand", schema)
	records := makeRecords(schema, "brand", []float64{0, 1, 2}) // Apple=0, Samsung=1, Google=2

	// Runs without error; produces meaningless numbers (no rejection)
	result, err := agg.Aggregate(records, "brand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Average of 0, 1, 2 = 1.0 (meaningless but accepted)
	if !floatClose(result, 1.0, 0.001) {
		t.Errorf("average on categorical = %f, want 1.0", result)
	}
}

// helper to create aggregator from registry
func makeAggregator(t *testing.T, aggType types.AggregationType, field string, schema *encoding.Schema) Aggregator {
	t.Helper()
	factory, ok := aggregatorRegistry[aggType]
	if !ok {
		t.Fatalf("no aggregator registered for %s", aggType)
	}
	agg, err := factory(&types.Aggregation{Type: aggType, Field: field}, schema)
	if err != nil {
		t.Fatalf("create aggregator: %v", err)
	}
	return agg
}
