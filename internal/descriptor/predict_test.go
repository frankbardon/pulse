package descriptor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// buildTestPulseFile creates a minimal .pulse file with header + schema (no records).
func buildTestPulseFile(t *testing.T, schema *encoding.Schema) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	return buf.Bytes()
}

func TestPredictReadsHeaderOnly(t *testing.T) {
	t.Parallel()

	// Build a file with header + schema but NO records.
	// If predict tries to read records, it would fail or panic.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	env := predictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}
}

func TestPredict_ValidRequest(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: makeDictionary(t, "A", "B", "C")},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	env := predictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}

	result, ok := env.Data.(*descriptor.PredictResult)
	if !ok {
		t.Fatal("Data is not *PredictResult")
	}
	if !result.Valid {
		t.Error("result.Valid = false, want true")
	}
	if result.SchemaInfo == nil {
		t.Fatal("SchemaInfo is nil")
	}
	if result.SchemaInfo.FieldCount != 2 {
		t.Errorf("FieldCount = %d, want 2", result.SchemaInfo.FieldCount)
	}
}

func TestPredict_InvalidRequest(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "nonexistent", Label: "avg"},
		},
	}

	env := predictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected errors for unknown field")
	}

	found := false
	for _, e := range env.Errors {
		if e.Code == string(errors.SERVICE_VALIDATION) {
			found = true
		}
	}
	if !found {
		t.Error("expected SERVICE_VALIDATION error")
	}

	result := env.Data.(*descriptor.PredictResult)
	if result.Valid {
		t.Error("result.Valid = true, want false")
	}
}

// TestPredict_NumericAggOnCategoricalRefused pins E2-S4: a numeric
// aggregation over a categorical (its dictionary index) is the
// accepted-types table's one PULSE_FIELD_TYPE_UNSUPPORTED error — the
// runtime's refusal — never the retired advisory warning.
func TestPredict_NumericAggOnCategoricalRefused(t *testing.T) {
	t.Parallel()

	dict := makeDictionary(t, "A", "B", "C")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "grade", Label: "avg_grade"},
		},
	}

	env := predictFromBytes(data, req, nil)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_FIELD_TYPE_UNSUPPORTED) {
		t.Fatalf("errors = %+v, want one PULSE_FIELD_TYPE_UNSUPPORTED", env.Errors)
	}
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
			t.Errorf("retired warning %q", w.Message)
		}
	}
}

func TestPredict_DescriptionQualityWarning(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: ""},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Age of the participant in years"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{}

	env := predictFromBytes(data, req, nil)

	found := false
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
			found = true
		}
	}
	if !found {
		t.Error("expected PULSE_FIELD_DESCRIPTION_LOW_QUALITY warning for empty description")
	}
}

func TestPredict_StrictMode_WarningsAreErrors(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: ""},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum_score"},
		},
	}

	env := predictFromBytes(data, req, &PredictOptions{Strict: true})

	// In strict mode, the description-quality warning is an error.
	if len(env.Warnings) != 0 {
		t.Errorf("expected no warnings in strict mode, got %d", len(env.Warnings))
	}

	found := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
			found = true
		}
	}
	if !found {
		t.Error("expected PULSE_FIELD_DESCRIPTION_LOW_QUALITY as error in strict mode")
	}
}

func TestPredict_CountOnCategorical_NoWarning(t *testing.T) {
	t.Parallel()

	dict := makeDictionary(t, "A", "B", "C")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	// COUNT and FREQUENCY are meaningful for categorical.
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "grade", Label: "count_grade"},
			{Type: types.AGG_MODE_COUNT, Field: "grade", Label: "freq_grade"},
		},
	}

	env := predictFromBytes(data, req, nil)
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
			t.Error("COUNT/FREQUENCY should not trigger categorical warning")
		}
	}
}

func TestPredictKeyComposerMatchesAggregatorKey(t *testing.T) {
	t.Parallel()

	// Verify that all aggregation types listed in types are handled
	// by checking they don't cause panics in predict.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "val", Type: encoding.FieldTypeF64, Description: "Test value for all aggregation types"},
			{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: makeDictionary(t, "a", "b"), Description: "Multi-select tags for the set aggregations"},
		},
	}
	data := buildTestPulseFile(t, schema)

	for _, aggType := range types.AllAggregationTypes() {
		agg := &types.Aggregation{Type: aggType, Field: "val", Label: "result"}
		if strings.HasPrefix(string(aggType), "AGG_SET_") {
			// A set aggregator reads a set column (an f64 is refused).
			agg.Field = "tags"
		}
		if aggType == types.AGG_FREQUENCY {
			// Its required params.value is refused when absent
			// (TestPredict_AggFrequencyMissingValue).
			agg.Params = json.RawMessage(`{"value":"1"}`)
		}
		if aggType == types.AGG_WEIGHTED_MEAN {
			// A weighted figure: with no weight resolving it is refused
			// (TestResolveWeights_WeightedMeanSugar).
			agg.Params = json.RawMessage(`{"weight_field":"val"}`)
		}
		if aggType == types.AGG_MAPE || aggType == types.AGG_RMSE || aggType == types.AGG_MAE {
			// Model error compares field with a required prediction column.
			agg.Params = json.RawMessage(`{"prediction_field":"val"}`)
		}
		if aggType == types.AGG_AUC {
			// AUC scores field against a required binary outcome column.
			agg.Params = json.RawMessage(`{"outcome":"val"}`)
		}
		if aggType.ArrayValued() {
			// A vector aggregator reads fields, never field.
			agg.Field, agg.Fields = "", []string{"val"}
		}
		req := &types.Request{Aggregations: []*types.Aggregation{agg}}
		env := predictFromBytes(data, req, nil)
		if len(env.Errors) != 0 {
			t.Errorf("aggregation %s produced errors: %+v", aggType, env.Errors[0])
		}
	}
}

// makeDictionary builds a dictionary with the given values.
func makeDictionary(t *testing.T, values ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("Dictionary.Add(%q): %v", v, err)
		}
	}
	return d
}
