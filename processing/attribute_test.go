package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeAttribute(t *testing.T, attrType types.AttributeType, field string, schema *encoding.Schema, expr string) AttributeComputer {
	t.Helper()
	factory, ok := attributeRegistry[attrType]
	if !ok {
		t.Fatalf("no attribute registered for %s", attrType)
	}
	attr, err := factory(&types.Attribute{Type: attrType, Field: field, Label: "result", Expression: expr}, schema)
	if err != nil {
		t.Fatalf("create attribute: %v", err)
	}
	return attr
}

// --- ZScore Attribute ---

func TestAttribute_ZScore_Basic(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_ZSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{2, 4, 4, 4, 5, 5, 7, 9})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 8 {
		t.Fatalf("result length = %d, want 8", len(result))
	}
	// Mean=5, stddev=2. Z-score of 9 = (9-5)/2 = 2.0
	if !floatClose(result[7], 2.0, 0.01) {
		t.Errorf("zscore[7] = %f, want 2.0", result[7])
	}
	// Z-score of 2 = (2-5)/2 = -1.5
	if !floatClose(result[0], -1.5, 0.01) {
		t.Errorf("zscore[0] = %f, want -1.5", result[0])
	}
}

func TestAttribute_ZScore_Empty(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_ZSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("result length = %d, want 0", len(result))
	}
}

func TestAttribute_ZScore_SingleValue(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_ZSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{42})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("result length = %d, want 1", len(result))
	}
	// Stddev=0 => z-score=0
	if result[0] != 0 {
		t.Errorf("zscore = %f, want 0", result[0])
	}
}

// --- TScore Attribute ---

func TestAttribute_TScore_Basic(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_TSCORE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{2, 4, 4, 4, 5, 5, 7, 9})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 8 {
		t.Fatalf("result length = %d, want 8", len(result))
	}
	// T-score = z*10 + 50. For value 9: z=2.0, t=70
	if !floatClose(result[7], 70.0, 0.1) {
		t.Errorf("tscore[7] = %f, want 70.0", result[7])
	}
	// For value 2: z=-1.5, t=35
	if !floatClose(result[0], 35.0, 0.1) {
		t.Errorf("tscore[0] = %f, want 35.0", result[0])
	}
}

// --- Normalized Attribute ---

func TestAttribute_Normalized_Basic(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_NORMALIZED, "score", schema, "")
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("result length = %d, want 3", len(result))
	}
	// Min=10, Max=30. Normalized: (x-min)/(max-min)
	if !floatClose(result[0], 0.0, 0.001) {
		t.Errorf("normalized[0] = %f, want 0.0", result[0])
	}
	if !floatClose(result[1], 0.5, 0.001) {
		t.Errorf("normalized[1] = %f, want 0.5", result[1])
	}
	if !floatClose(result[2], 1.0, 0.001) {
		t.Errorf("normalized[2] = %f, want 1.0", result[2])
	}
}

func TestAttribute_Normalized_AllSame(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_NORMALIZED, "score", schema, "")
	records := makeRecords(schema, "score", []float64{5, 5, 5})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All same => all 0
	for i, v := range result {
		if v != 0 {
			t.Errorf("normalized[%d] = %f, want 0", i, v)
		}
	}
}

// --- Formula Attribute ---

func TestAttribute_Formula_Basic(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_FORMULA, "score", schema, "score * 2")
	records := makeRecords(schema, "score", []float64{10, 20, 30})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("result length = %d, want 3", len(result))
	}
	if !floatClose(result[0], 20.0, 0.001) {
		t.Errorf("formula[0] = %f, want 20.0", result[0])
	}
	if !floatClose(result[2], 60.0, 0.001) {
		t.Errorf("formula[2] = %f, want 60.0", result[2])
	}
}

func TestFormulaAttribute_CategoricalStringAccess(t *testing.T) {
	schema := categoricalSchema()
	attr := makeAttribute(t, types.ATTR_FORMULA, "brand", schema, `brand == "Apple"`)

	// Apple=0, Samsung=1, Google=2
	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	records[1] = NewRecord(schema, map[string]float64{"brand": 1, "score": 20})
	records[2] = NewRecord(schema, map[string]float64{"brand": 2, "score": 30})

	result, err := attr.Compute(records, "brand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// brand == "Apple" => true(1), false(0), false(0)
	if result[0] != 1.0 {
		t.Errorf("formula[0] = %f, want 1.0 (Apple==Apple)", result[0])
	}
	if result[1] != 0.0 {
		t.Errorf("formula[1] = %f, want 0.0 (Samsung!=Apple)", result[1])
	}
}

func TestFormulaAttribute_CategoricalInOperator(t *testing.T) {
	schema := categoricalSchema()
	attr := makeAttribute(t, types.ATTR_FORMULA, "brand", schema, `brand in ["Apple", "Samsung"]`)

	records := make([]*Record, 3)
	records[0] = NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	records[1] = NewRecord(schema, map[string]float64{"brand": 1, "score": 20})
	records[2] = NewRecord(schema, map[string]float64{"brand": 2, "score": 30})

	result, err := attr.Compute(records, "brand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// brand in ["Apple", "Samsung"] => true(1), true(1), false(0)
	if result[0] != 1.0 {
		t.Errorf("formula[0] = %f, want 1.0", result[0])
	}
	if result[1] != 1.0 {
		t.Errorf("formula[1] = %f, want 1.0", result[1])
	}
	if result[2] != 0.0 {
		t.Errorf("formula[2] = %f, want 0.0", result[2])
	}
}

// --- Percentile Attribute ---

func TestAttribute_Percentile_Basic(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_PERCENTILE, "score", schema, "")
	records := makeRecords(schema, "score", []float64{10, 30, 20, 40})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("result length = %d, want 4", len(result))
	}
	// Values sorted: 10, 20, 30, 40
	// Percentiles: 10=>25, 20=>50, 30=>75, 40=>100
	if !floatClose(result[0], 25.0, 0.1) {
		t.Errorf("percentile[0] (10) = %f, want ~25.0", result[0])
	}
	if !floatClose(result[2], 50.0, 0.1) {
		t.Errorf("percentile[2] (20) = %f, want ~50.0", result[2])
	}
}

// --- Rank Attribute ---

func TestAttribute_Rank_Basic(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_RANK, "score", schema, "")
	records := makeRecords(schema, "score", []float64{30, 10, 20})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("result length = %d, want 3", len(result))
	}
	// Ranks: 30=>3, 10=>1, 20=>2 (ascending rank)
	if !floatClose(result[0], 3.0, 0.001) {
		t.Errorf("rank[0] (30) = %f, want 3.0", result[0])
	}
	if !floatClose(result[1], 1.0, 0.001) {
		t.Errorf("rank[1] (10) = %f, want 1.0", result[1])
	}
	if !floatClose(result[2], 2.0, 0.001) {
		t.Errorf("rank[2] (20) = %f, want 2.0", result[2])
	}
}

func TestAttribute_Rank_Empty(t *testing.T) {
	schema := numericSchema()
	attr := makeAttribute(t, types.ATTR_RANK, "score", schema, "")
	records := makeRecords(schema, "score", []float64{})

	result, err := attr.Compute(records, "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("result length = %d, want 0", len(result))
	}
}

// Suppress unused import warning
var _ = math.Abs
