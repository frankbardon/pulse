package descriptor

import (
	"bytes"
	"encoding/json"
	"testing"

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

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}
}

func TestPredict_ValidRequest(t *testing.T) {
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

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}

	result, ok := env.Data.(*PredictResult)
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

	env := PredictFromBytes(data, req, nil)
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

	result := env.Data.(*PredictResult)
	if result.Valid {
		t.Error("result.Valid = true, want false")
	}
}

func TestPredict_CategoricalWarning(t *testing.T) {
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

	env := PredictFromBytes(data, req, nil)

	if len(env.Warnings) == 0 {
		t.Fatal("expected categorical warning")
	}

	found := false
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
			found = true
		}
	}
	if !found {
		t.Error("expected PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL warning")
	}
}

func TestPredict_DescriptionQualityWarning(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: ""},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Age of the participant in years"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{}

	env := PredictFromBytes(data, req, nil)

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
	dict := makeDictionary(t, "A", "B", "C")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "grade", Label: "sum_grade"},
		},
	}

	env := PredictFromBytes(data, req, &PredictOptions{Strict: true})

	// In strict mode, the categorical warning should be an error.
	if len(env.Warnings) != 0 {
		t.Errorf("expected no warnings in strict mode, got %d", len(env.Warnings))
	}

	foundCat := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
			foundCat = true
		}
	}
	if !foundCat {
		t.Error("expected PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL as error in strict mode")
	}
}

func TestPredict_CountOnCategorical_NoWarning(t *testing.T) {
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
			{Type: types.AGG_FREQUENCY, Field: "grade", Label: "freq_grade"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
			t.Error("COUNT/FREQUENCY should not trigger categorical warning")
		}
	}
}

func TestPredictGolden_SimpleRequest(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Age of the participant in years"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "predict_simple.json", out)
}

func TestPredictGolden_ComposedRequest(t *testing.T) {
	dict := makeDictionary(t, "A", "B", "C")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg_score"},
			{Type: types.AGG_SUM, Field: "grade", Label: "sum_grade"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "grade"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "predict_composed.json", out)
}

func TestPredictKeyComposerMatchesAggregatorKey(t *testing.T) {
	// Verify that all aggregation types listed in types are handled
	// by checking they don't cause panics in predict.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "val", Type: encoding.FieldTypeF64, Description: "Test value for all aggregation types"},
		},
	}
	data := buildTestPulseFile(t, schema)

	for _, aggType := range types.AllAggregationTypes() {
		req := &types.Request{
			Aggregations: []*types.Aggregation{
				{Type: aggType, Field: "val", Label: "result"},
			},
		}
		env := PredictFromBytes(data, req, nil)
		if len(env.Errors) != 0 {
			t.Errorf("aggregation %s produced errors: %v", aggType, env.Errors)
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
