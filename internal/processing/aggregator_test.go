package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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

func TestAggregator_Median_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MEDIAN, "score", schema)
	records := makeRecords(schema, "score", []float64{3, 1, 4, 1, 5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 3.0, 0.001) {
		t.Errorf("median = %f, want 3.0", result)
	}
}

func TestAggregator_Median_Even(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MEDIAN, "score", schema)
	records := makeRecords(schema, "score", []float64{3, 1, 4, 2})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 2.5, 0.001) {
		t.Errorf("median = %f, want 2.5", result)
	}
}

func TestAggregator_Median_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MEDIAN, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 42.0, 0.001) {
		t.Errorf("median = %f, want 42.0", result)
	}
}

func TestAggregator_Median_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MEDIAN, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("median of empty = %f, want 0", result)
	}
}

func TestAggregator_Median_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MEDIAN, "score", schema)
	records := makeRecordsWithNulls(schema, "score", []float64{10, 0, 30}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null at index 1 is skipped; median of [10, 30] = 20
	if !floatClose(result, 20.0, 0.001) {
		t.Errorf("median = %f, want 20.0", result)
	}
}

func TestAggregator_Variance_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_VARIANCE, "score", schema)
	records := makeRecords(schema, "score", []float64{2, 4, 4, 4, 5, 5, 7, 9})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Population variance of this set is 4.0
	if !floatClose(result, 4.0, 0.01) {
		t.Errorf("variance = %f, want 4.0", result)
	}
}

func TestAggregator_Variance_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_VARIANCE, "score", schema)
	records := makeRecords(schema, "score", []float64{5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("variance of single value = %f, want 0", result)
	}
}

func TestAggregator_Variance_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_VARIANCE, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("variance of empty = %f, want 0", result)
	}
}

func TestAggregator_Variance_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_VARIANCE, "score", schema)
	records := makeRecordsWithNulls(schema, "score", []float64{10, 0, 30}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null at index 1 is skipped; variance of [10, 30] = 100
	if !floatClose(result, 100.0, 0.001) {
		t.Errorf("variance = %f, want 100.0", result)
	}
}

func TestAggregator_Variance_MatchesStdDevSquared(t *testing.T) {
	schema := numericSchema()
	varianceAgg := makeAggregator(t, types.AGG_VARIANCE, "score", schema)
	stddevAgg := makeAggregator(t, types.AGG_STDDEV, "score", schema)
	records := makeRecords(schema, "score", []float64{2, 4, 4, 4, 5, 5, 7, 9})

	variance, err := varianceAgg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stddev, err := stddevAgg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(variance, stddev*stddev, 0.0001) {
		t.Errorf("variance (%f) != stddev^2 (%f)", variance, stddev*stddev)
	}
}

func TestAggregator_Mode_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MODE, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 2, 2, 3, 3, 3})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 3.0, 0.001) {
		t.Errorf("mode = %f, want 3.0", result)
	}
}

func TestAggregator_Mode_Tie(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MODE, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 1, 2, 2})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tie: 1 and 2 both appear twice; smallest wins
	if !floatClose(result, 1.0, 0.001) {
		t.Errorf("mode = %f, want 1.0", result)
	}
}

func TestAggregator_Mode_AllUnique(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MODE, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 2, 3})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All freq 1; smallest wins
	if !floatClose(result, 1.0, 0.001) {
		t.Errorf("mode = %f, want 1.0", result)
	}
}

func TestAggregator_Mode_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MODE, "score", schema)
	records := makeRecords(schema, "score", []float64{7})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 7.0, 0.001) {
		t.Errorf("mode = %f, want 7.0", result)
	}
}

func TestAggregator_Mode_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MODE, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("mode of empty = %f, want 0", result)
	}
}

func TestAggregator_Mode_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_MODE, "score", schema)
	records := makeRecordsWithNulls(schema, "score", []float64{10, 0, 10, 20}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null at index 1 is skipped; mode of [10, 10, 20] = 10
	if !floatClose(result, 10.0, 0.001) {
		t.Errorf("mode = %f, want 10.0", result)
	}
}

func TestAggregator_Skewness_Symmetric(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SKEWNESS, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 2, 3, 4, 5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Symmetric distribution has skewness ~0
	if !floatClose(result, 0.0, 0.001) {
		t.Errorf("skewness = %f, want ~0.0", result)
	}
}

func TestAggregator_Skewness_RightSkew(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SKEWNESS, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 1, 1, 1, 10})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Right-skewed distribution should be positive
	if result <= 0 {
		t.Errorf("skewness = %f, want positive (right skew)", result)
	}
}

func TestAggregator_Skewness_LeftSkew(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SKEWNESS, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 10, 10, 10, 10})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Left-skewed distribution should be negative
	if result >= 0 {
		t.Errorf("skewness = %f, want negative (left skew)", result)
	}
}

func TestAggregator_Skewness_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SKEWNESS, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("skewness of single value = %f, want 0", result)
	}
}

func TestAggregator_Skewness_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SKEWNESS, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("skewness of empty = %f, want 0", result)
	}
}

func TestAggregator_Skewness_AllSame(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SKEWNESS, "score", schema)
	records := makeRecords(schema, "score", []float64{5, 5, 5, 5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All same values: stddev=0, skewness=0
	if result != 0 {
		t.Errorf("skewness of all same = %f, want 0", result)
	}
}

func TestAggregator_Skewness_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_SKEWNESS, "score", schema)
	// Values [1, null, 1, 1, 10] -> after null removal [1, 1, 1, 10] -> right skew
	records := makeRecordsWithNulls(schema, "score", []float64{1, 0, 1, 1, 10}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be positive (right skew) after excluding nulls
	if result <= 0 {
		t.Errorf("skewness = %f, want positive (right skew after null exclusion)", result)
	}
}

func TestAggregator_Kurtosis_Uniform(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_KURTOSIS, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 2, 3, 4, 5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Uniformly spaced values have negative excess kurtosis (platykurtic)
	if result >= 0 {
		t.Errorf("kurtosis = %f, want negative (platykurtic for uniform)", result)
	}
	// For [1,2,3,4,5], excess kurtosis is -1.3
	if !floatClose(result, -1.3, 0.1) {
		t.Errorf("kurtosis = %f, want ~-1.3", result)
	}
}

func TestAggregator_Kurtosis_Peaked(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_KURTOSIS, "score", schema)
	// Values clustered at center with outliers -> positive excess kurtosis (leptokurtic)
	records := makeRecords(schema, "score", []float64{0, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 100})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Heavy-tailed distribution should have positive excess kurtosis
	if result <= 0 {
		t.Errorf("kurtosis = %f, want positive (leptokurtic)", result)
	}
}

func TestAggregator_Kurtosis_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_KURTOSIS, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("kurtosis of single value = %f, want 0", result)
	}
}

func TestAggregator_Kurtosis_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_KURTOSIS, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("kurtosis of empty = %f, want 0", result)
	}
}

func TestAggregator_Kurtosis_AllSame(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_KURTOSIS, "score", schema)
	records := makeRecords(schema, "score", []float64{5, 5, 5, 5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All same values: stddev=0, kurtosis=0
	if result != 0 {
		t.Errorf("kurtosis of all same = %f, want 0", result)
	}
}

func TestAggregator_Kurtosis_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_KURTOSIS, "score", schema)
	// Values [1, null, 2, 3, 4, 5] -> after null removal [1, 2, 3, 4, 5] -> negative kurtosis
	records := makeRecordsWithNulls(schema, "score", []float64{1, 0, 2, 3, 4, 5}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be negative (platykurtic) after excluding nulls
	if result >= 0 {
		t.Errorf("kurtosis = %f, want negative (platykurtic after null exclusion)", result)
	}
}

func TestAggregator_DistinctCount_Basic(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema)
	records := makeRecords(schema, "score", []float64{1, 2, 3, 2, 1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 3.0, 0.001) {
		t.Errorf("distinct count = %f, want 3.0", result)
	}
}

func TestAggregator_DistinctCount_AllSame(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema)
	records := makeRecords(schema, "score", []float64{5, 5, 5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 1.0, 0.001) {
		t.Errorf("distinct count = %f, want 1.0", result)
	}
}

func TestAggregator_DistinctCount_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 1.0, 0.001) {
		t.Errorf("distinct count = %f, want 1.0", result)
	}
}

func TestAggregator_DistinctCount_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("distinct count of empty = %f, want 0", result)
	}
}

func TestAggregator_DistinctCount_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema)
	records := makeRecordsWithNulls(schema, "score", []float64{1, 0, 2, 1}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null at index 1 is skipped; distinct count of [1, 2, 1] = 2
	if !floatClose(result, 2.0, 0.001) {
		t.Errorf("distinct count = %f, want 2.0", result)
	}
}

func TestAggregator_Percentile_P50(t *testing.T) {
	schema := numericSchema()
	agg := makePercentileAggregator(t, "score", schema, 50)
	records := makeRecords(schema, "score", []float64{3, 1, 4, 1, 5})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Median of [1,1,3,4,5] via linear interpolation at rank 2.0 = 3.0
	medianAgg := makeAggregator(t, types.AGG_MEDIAN, "score", schema)
	medianResult, _ := medianAgg.Aggregate(records, "score")
	if !floatClose(result, medianResult, 0.001) {
		t.Errorf("percentile p50 = %f, median = %f, want equal", result, medianResult)
	}
}

func TestAggregator_Percentile_P0(t *testing.T) {
	schema := numericSchema()
	agg := makePercentileAggregator(t, "score", schema, 0)
	records := makeRecords(schema, "score", []float64{10, 30, 20})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 10.0, 0.001) {
		t.Errorf("percentile p0 = %f, want 10.0 (min)", result)
	}
}

func TestAggregator_Percentile_P100(t *testing.T) {
	schema := numericSchema()
	agg := makePercentileAggregator(t, "score", schema, 100)
	records := makeRecords(schema, "score", []float64{10, 30, 20})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 30.0, 0.001) {
		t.Errorf("percentile p100 = %f, want 30.0 (max)", result)
	}
}

func TestAggregator_Percentile_P25_P75(t *testing.T) {
	schema := numericSchema()
	// Values: [1, 2, 3, 4, 5, 6, 7, 8]
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	records := makeRecords(schema, "score", values)

	agg25 := makePercentileAggregator(t, "score", schema, 25)
	result25, err := agg25.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// rank = 0.25 * 7 = 1.75; interpolate between vals[1]=2 and vals[2]=3: 2 + 0.75*1 = 2.75
	if !floatClose(result25, 2.75, 0.001) {
		t.Errorf("percentile p25 = %f, want 2.75", result25)
	}

	agg75 := makePercentileAggregator(t, "score", schema, 75)
	result75, err := agg75.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// rank = 0.75 * 7 = 5.25; interpolate between vals[5]=6 and vals[6]=7: 6 + 0.25*1 = 6.25
	if !floatClose(result75, 6.25, 0.001) {
		t.Errorf("percentile p75 = %f, want 6.25", result75)
	}
}

func TestAggregator_Percentile_SingleValue(t *testing.T) {
	schema := numericSchema()
	agg := makePercentileAggregator(t, "score", schema, 90)
	records := makeRecords(schema, "score", []float64{42})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !floatClose(result, 42.0, 0.001) {
		t.Errorf("percentile single value = %f, want 42.0", result)
	}
}

func TestAggregator_Percentile_Empty(t *testing.T) {
	schema := numericSchema()
	agg := makePercentileAggregator(t, "score", schema, 50)
	records := makeRecords(schema, "score", []float64{})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 0 {
		t.Errorf("percentile of empty = %f, want 0", result)
	}
}

func TestAggregator_Percentile_NullHandling(t *testing.T) {
	schema := numericSchema()
	agg := makePercentileAggregator(t, "score", schema, 50)
	records := makeRecordsWithNulls(schema, "score", []float64{10, 0, 30}, []int{1})

	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null at index 1 is skipped; p50 of [10, 30] = 20 (linear interpolation at rank 0.5)
	if !floatClose(result, 20.0, 0.001) {
		t.Errorf("percentile = %f, want 20.0", result)
	}
}

func TestAggregator_Percentile_InvalidParams(t *testing.T) {
	schema := numericSchema()
	// Percentile < 0
	_, err := newPercentileAggregator(&types.Aggregation{
		Type:   types.AGG_PERCENTILE,
		Field:  "score",
		Params: json.RawMessage(`{"percentile": -1}`),
	}, schema)
	if err == nil {
		t.Fatal("expected error for percentile < 0")
	}

	// Percentile > 100
	_, err = newPercentileAggregator(&types.Aggregation{
		Type:   types.AGG_PERCENTILE,
		Field:  "score",
		Params: json.RawMessage(`{"percentile": 101}`),
	}, schema)
	if err == nil {
		t.Fatal("expected error for percentile > 100")
	}
}

func TestAggregator_Percentile_DefaultParams(t *testing.T) {
	schema := numericSchema()
	// nil params should default to p50
	agg, err := newPercentileAggregator(&types.Aggregation{
		Type:  types.AGG_PERCENTILE,
		Field: "score",
	}, schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	records := makeRecords(schema, "score", []float64{3, 1, 4, 1, 5})
	result, err := agg.Aggregate(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default p50 of [1,1,3,4,5] at rank 2.0 = 3.0
	if !floatClose(result, 3.0, 0.001) {
		t.Errorf("default percentile = %f, want 3.0", result)
	}
}

// helper to create percentile aggregator with specific percentile value
func makePercentileAggregator(t *testing.T, field string, schema *encoding.Schema, percentile float64) Aggregator {
	t.Helper()
	params, _ := json.Marshal(percentileParams{Percentile: percentile})
	agg, err := newPercentileAggregator(&types.Aggregation{
		Type:   types.AGG_PERCENTILE,
		Field:  field,
		Params: params,
	}, schema)
	if err != nil {
		t.Fatalf("create percentile aggregator: %v", err)
	}
	return agg
}

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

// ---------------------------------------------------------------------
// AGG_DISTINCT_SUM — sums a value field once per distinct key.
//
// field names the value summed (Orbit's use: `weight`); Params.distinct_by
// names the key field (Orbit's use: `respondent`). The scalar return is the
// sum over distinct keys; Components() additionally carries the distinct
// count so one operator yields both figures from one scan.
// ---------------------------------------------------------------------

// distinctSumSchema reuses the shared numeric schema: `score` is the
// summed value, `age` stands in as the distinct key.
func distinctSumRecords(schema *encoding.Schema, keys, values []float64) []*Record {
	recs := make([]*Record, len(keys))
	for i := range keys {
		recs[i] = NewRecord(schema, map[string]float64{"age": keys[i], "score": values[i]})
	}
	return recs
}

func newDistinctSum(t *testing.T, distinctBy string) Aggregator {
	t.Helper()
	spec := &types.Aggregation{
		Type:   types.AGG_DISTINCT_SUM,
		Field:  "score",
		Params: json.RawMessage(`{"distinct_by":"` + distinctBy + `"}`),
	}
	factory, ok := aggregatorRegistry[types.AGG_DISTINCT_SUM]
	if !ok {
		t.Fatalf("AGG_DISTINCT_SUM not registered in aggregatorRegistry")
	}
	agg, err := factory(spec, numericSchema())
	if err != nil {
		t.Fatalf("create AGG_DISTINCT_SUM: %v", err)
	}
	return agg
}

func TestAggregator_DistinctSum_Buffered(t *testing.T) {
	schema := numericSchema()
	// Keys 1,1,1,2,3 carrying a constant value per key: 10,10,10,20,30.
	// The distinct sum is 10+20+30 = 60, not the 80 a plain AGG_SUM gives.
	recs := distinctSumRecords(schema,
		[]float64{1, 1, 1, 2, 3},
		[]float64{10, 10, 10, 20, 30})

	agg := newDistinctSum(t, "age")
	got, err := agg.Aggregate(recs, "score")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if !floatClose(got, 60, 1e-9) {
		t.Errorf("distinct sum = %v, want 60 (each key counted once)", got)
	}
}

func TestAggregator_DistinctSum_Streaming(t *testing.T) {
	schema := numericSchema()
	recs := distinctSumRecords(schema,
		[]float64{1, 1, 1, 2, 3},
		[]float64{10, 10, 10, 20, 30})

	agg := newDistinctSum(t, "age")
	online, ok := agg.(OnlineAggregator)
	if !ok {
		t.Fatalf("AGG_DISTINCT_SUM does not implement OnlineAggregator — it is declared Streamable")
	}
	for _, r := range recs {
		if err := online.UpdateRow(r, "score"); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	got, err := online.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !floatClose(got, 60, 1e-9) {
		t.Errorf("streaming distinct sum = %v, want 60 (parity with the buffered path)", got)
	}
}

// TestAggregator_DistinctSum_FirstValueWins pins the documented tie rule:
// when one key carries conflicting values, the FIRST value observed is the
// one summed. Both execution paths must agree.
func TestAggregator_DistinctSum_FirstValueWins(t *testing.T) {
	schema := numericSchema()
	// Key 1 arrives as 10 then 999; key 2 arrives once as 20.
	recs := distinctSumRecords(schema,
		[]float64{1, 1, 2},
		[]float64{10, 999, 20})

	buffered := newDistinctSum(t, "age")
	got, err := buffered.Aggregate(recs, "score")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if !floatClose(got, 30, 1e-9) {
		t.Errorf("buffered first-value-wins = %v, want 30 (10 + 20, not 999 + 20)", got)
	}

	streaming := newDistinctSum(t, "age").(OnlineAggregator)
	for _, r := range recs {
		if err := streaming.UpdateRow(r, "score"); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	gotOnline, err := streaming.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !floatClose(gotOnline, 30, 1e-9) {
		t.Errorf("streaming first-value-wins = %v, want 30", gotOnline)
	}
}

// TestAggregator_DistinctSum_MissingDistinctBy asserts a missing or empty
// params.distinct_by is REFUSED with a coded error rather than defaulted to
// the aggregation's own field (which would silently degrade the operator
// into a distinct-value sum).
func TestAggregator_DistinctSum_MissingDistinctBy(t *testing.T) {
	factory, ok := aggregatorRegistry[types.AGG_DISTINCT_SUM]
	if !ok {
		t.Fatalf("AGG_DISTINCT_SUM not registered in aggregatorRegistry")
	}
	cases := []struct {
		name   string
		params json.RawMessage
	}{
		{"absent params", nil},
		{"empty object", json.RawMessage(`{}`)},
		{"empty distinct_by", json.RawMessage(`{"distinct_by":""}`)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := factory(&types.Aggregation{
				Type:   types.AGG_DISTINCT_SUM,
				Field:  "score",
				Params: tc.params,
			}, numericSchema())
			if err == nil {
				t.Fatalf("factory accepted a spec with no distinct_by; want a coded refusal")
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("error %v is not a *errors.CodedError", err)
			}
			if ce.Code != errors.PROCESSING_CONFIG {
				t.Errorf("code = %v, want PROCESSING_CONFIG", ce.Code)
			}
		})
	}
}

// TestAggregator_DistinctSum_Components pins the two-figures-one-scan
// contract: the scalar is the distinct sum and Components() carries the
// distinct count alongside it.
func TestAggregator_DistinctSum_Components(t *testing.T) {
	schema := numericSchema()
	recs := distinctSumRecords(schema,
		[]float64{1, 1, 1, 2, 3},
		[]float64{10, 10, 10, 20, 30})

	agg := newDistinctSum(t, "age")
	if _, err := agg.Aggregate(recs, "score"); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	meta, ok := agg.(MetaAggregator)
	if !ok {
		t.Fatalf("AGG_DISTINCT_SUM does not implement MetaAggregator")
	}
	comps, err := meta.Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	sum, ok := comps["sum"].(float64)
	if !ok {
		t.Fatalf("components[sum] = %#v, want float64", comps["sum"])
	}
	if !floatClose(sum, 60, 1e-9) {
		t.Errorf("components[sum] = %v, want 60", sum)
	}
	dc, ok := comps["distinct_count"].(int)
	if !ok {
		t.Fatalf("components[distinct_count] = %#v, want int", comps["distinct_count"])
	}
	if dc != 3 {
		t.Errorf("components[distinct_count] = %d, want 3", dc)
	}
	if _, present := comps["n"]; present {
		t.Error("Components() re-emitted the universal floor key n; the orchestrator owns it")
	}
	if _, present := comps["n_null"]; present {
		t.Error("Components() re-emitted the universal floor key n_null; the orchestrator owns it")
	}
}

// TestAggregator_DistinctSum_Nulls asserts a row missing either half of
// the (key, value) pair contributes nothing — the key is not registered, so
// a later row carrying the same key with a real value still counts.
func TestAggregator_DistinctSum_Nulls(t *testing.T) {
	schema := numericSchema()
	recs := []*Record{
		NewRecordWithNulls(schema, map[string]float64{"age": 1, "score": 10}, map[string]bool{"score": true}),
		NewRecord(schema, map[string]float64{"age": 1, "score": 10}),
		NewRecordWithNulls(schema, map[string]float64{"age": 2, "score": 20}, map[string]bool{"age": true}),
		NewRecord(schema, map[string]float64{"age": 3, "score": 30}),
	}

	agg := newDistinctSum(t, "age")
	got, err := agg.Aggregate(recs, "score")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if !floatClose(got, 40, 1e-9) {
		t.Errorf("distinct sum with nulls = %v, want 40 (key 1 via its non-null row, plus key 3)", got)
	}
	comps, err := agg.(MetaAggregator).Components()
	if err != nil {
		t.Fatalf("Components: %v", err)
	}
	if dc, _ := comps["distinct_count"].(int); dc != 2 {
		t.Errorf("components[distinct_count] = %d, want 2 (the null-keyed row registers no key)", dc)
	}
}

// TestAggregator_DistinctSum_MergeOnline asserts the parallel/shard fold
// unions the per-key maps, and that the RECEIVER's value wins on a key
// present in both — partials merge in deterministic shard order, so
// receiver-wins IS first-value-wins across the whole input.
func TestAggregator_DistinctSum_MergeOnline(t *testing.T) {
	schema := numericSchema()
	left := newDistinctSum(t, "age").(OnlineAggregator)
	right := newDistinctSum(t, "age").(OnlineAggregator)

	for _, r := range distinctSumRecords(schema, []float64{1, 2}, []float64{10, 20}) {
		if err := left.UpdateRow(r, "score"); err != nil {
			t.Fatalf("UpdateRow(left): %v", err)
		}
	}
	// Key 2 repeats with a conflicting value; key 3 is new.
	for _, r := range distinctSumRecords(schema, []float64{2, 3}, []float64{999, 30}) {
		if err := right.UpdateRow(r, "score"); err != nil {
			t.Fatalf("UpdateRow(right): %v", err)
		}
	}

	mergeable, ok := left.(MergeableAggregator)
	if !ok {
		t.Fatalf("AGG_DISTINCT_SUM does not implement MergeableAggregator — it is declared Mergeable")
	}
	if err := mergeable.MergeOnline(right); err != nil {
		t.Fatalf("MergeOnline: %v", err)
	}
	got, err := left.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !floatClose(got, 60, 1e-9) {
		t.Errorf("merged distinct sum = %v, want 60 (10 + 20 + 30; the receiver's key 2 wins)", got)
	}
}

// TestAggregator_DistinctSum_EmptyInput asserts Finalize is safe with no
// UpdateRow calls, per the OnlineAggregator contract.
func TestAggregator_DistinctSum_EmptyInput(t *testing.T) {
	agg := newDistinctSum(t, "age")
	got, err := agg.(OnlineAggregator).Finalize()
	if err != nil {
		t.Fatalf("Finalize on empty input: %v", err)
	}
	if got != 0 {
		t.Errorf("empty-input distinct sum = %v, want 0", got)
	}
}

// TestAggregator_DistinctSum_Classification pins the two declarations the
// whole effort's memory premise rests on. Both switches in
// types/streamability.go default to the restrictive branch, so an operator
// that forgets to opt in silently loses crosstab fusion — same numbers, no
// warning, several times the peak heap.
func TestAggregator_DistinctSum_Classification(t *testing.T) {
	if !types.AGG_DISTINCT_SUM.Streamable() {
		t.Error("AGG_DISTINCT_SUM.Streamable() = false, want true (per-key map, online)")
	}
	if !types.AGG_DISTINCT_SUM.Mergeable() {
		t.Error("AGG_DISTINCT_SUM.Mergeable() = false, want true (per-key maps union)")
	}
}
