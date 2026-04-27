package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func TestInspect_HeaderOnly(t *testing.T) {
	// Build a file with header + schema but NO records.
	// If inspect tries to read records, it would fail.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, nil)
	if len(env.Errors) != 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}

	result, ok := env.Data.(*InspectResult)
	if !ok {
		t.Fatal("Data is not *InspectResult")
	}
	if result.FieldCount != 1 {
		t.Errorf("FieldCount = %d, want 1", result.FieldCount)
	}
}

func TestInspect_CategoricalField(t *testing.T) {
	dict := makeDictionary(t, "red", "green", "blue")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, Description: "Primary color category", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", env.Errors)
	}

	result := env.Data.(*InspectResult)
	if len(result.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(result.Fields))
	}

	field := result.Fields[0]
	if !field.Categorical {
		t.Error("expected Categorical = true")
	}
	if field.Dictionary == nil {
		t.Fatal("expected non-nil Dictionary")
	}
	if field.Dictionary.TotalEntries != 3 {
		t.Errorf("TotalEntries = %d, want 3", field.Dictionary.TotalEntries)
	}
	if len(field.Dictionary.Values) != 3 {
		t.Errorf("Values count = %d, want 3", len(field.Dictionary.Values))
	}
}

func TestInspect_DictionaryTruncation(t *testing.T) {
	// Create a dictionary with 150 entries.
	values := make([]string, 150)
	for i := range values {
		values[i] = "val_" + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}
	dict := makeDictionary(t, values...)
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "category", Type: encoding.FieldTypeCategoricalU8, Description: "Test category with many values", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	// Default: truncated to 100.
	env := InspectFromBytes(data, nil)
	result := env.Data.(*InspectResult)
	field := result.Fields[0]
	if field.Dictionary.TotalEntries != 150 {
		t.Errorf("TotalEntries = %d, want 150", field.Dictionary.TotalEntries)
	}
	if !field.Dictionary.Truncated {
		t.Error("expected Truncated = true")
	}
	if len(field.Dictionary.Values) != DefaultDictionaryLimit {
		t.Errorf("Values count = %d, want %d", len(field.Dictionary.Values), DefaultDictionaryLimit)
	}

	// FullDict: all values.
	env2 := InspectFromBytes(data, &InspectOptions{FullDict: true})
	result2 := env2.Data.(*InspectResult)
	field2 := result2.Fields[0]
	if field2.Dictionary.Truncated {
		t.Error("expected Truncated = false with FullDict")
	}
	if len(field2.Dictionary.Values) != 150 {
		t.Errorf("Values count = %d, want 150 with FullDict", len(field2.Dictionary.Values))
	}
}

func TestInspect_FieldDescriptions(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, nil)
	result := env.Data.(*InspectResult)
	field := result.Fields[0]

	if field.Description != "Student test score value" {
		t.Errorf("Description = %q, want %q", field.Description, "Student test score value")
	}
	if field.DescriptionSource != "schema" {
		t.Errorf("DescriptionSource = %q, want %q", field.DescriptionSource, "schema")
	}
}

func TestInspect_SynthesizedDescription(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: ""},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "", Dictionary: encoding.NewDictionary()},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, nil)
	result := env.Data.(*InspectResult)

	// Numeric field with no description.
	if result.Fields[0].DescriptionSource != "synthesized" {
		t.Errorf("score DescriptionSource = %q, want %q", result.Fields[0].DescriptionSource, "synthesized")
	}
	if result.Fields[0].Description != "Numeric field: score" {
		t.Errorf("score Description = %q, want %q", result.Fields[0].Description, "Numeric field: score")
	}

	// Categorical field with no description.
	if result.Fields[1].DescriptionSource != "synthesized" {
		t.Errorf("grade DescriptionSource = %q, want %q", result.Fields[1].DescriptionSource, "synthesized")
	}
	if result.Fields[1].Description != "Categorical field: grade" {
		t.Errorf("grade Description = %q, want %q", result.Fields[1].Description, "Categorical field: grade")
	}
}

func TestInspectGolden(t *testing.T) {
	dict := makeDictionary(t, "red", "green", "blue")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Age of the participant in years"},
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, Description: "Primary color category", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, nil)
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}

	compareGolden(t, "inspect.json", out)
}
