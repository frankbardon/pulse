package descriptor

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestPredict_InvalidHeader(t *testing.T) {
	env := PredictFromBytes([]byte("not a pulse file"), &types.Request{}, nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for invalid header")
	}
	result := env.Data.(*PredictResult)
	if result.Valid {
		t.Error("result.Valid should be false")
	}
}

func TestPredict_InvalidSchema(t *testing.T) {
	// Valid header but truncated schema.
	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	buf.Write([]byte{0xFF}) // garbage schema

	env := PredictFromBytes(buf.Bytes(), &types.Request{}, nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for invalid schema")
	}
	result := env.Data.(*PredictResult)
	if result.Valid {
		t.Error("result.Valid should be false")
	}
}

func TestPredict_FilterUnknownField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "nonexistent", Values: []string{"1"}},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for unknown filter field")
	}
}

func TestPredict_FilterExpressionNoField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_EXPRESSION, Expression: "score > 0"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Errorf("unexpected errors for expression filter without field: %v", env.Errors)
	}
}

func TestPredict_GroupUnknownField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "nonexistent"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for unknown group field")
	}
}

func TestPredict_AttributeUnknownField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "nonexistent", Label: "z"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for unknown attribute field")
	}
}

func TestPredict_DescriptionQuality_Strict(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: ""},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := PredictFromBytes(data, &types.Request{}, &PredictOptions{Strict: true})

	// Should be errors, not warnings, in strict mode.
	foundDescError := false
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
			foundDescError = true
		}
	}
	if !foundDescError {
		t.Error("expected PULSE_FIELD_DESCRIPTION_LOW_QUALITY as error in strict mode")
	}
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
			t.Error("should not have description quality warning in strict mode")
		}
	}
}

func TestPredict_LowQualityDescriptionCases(t *testing.T) {
	cases := []struct {
		desc    string
		lowQual bool
	}{
		{"", true},
		{"short", true},
		{"n/a", true},
		{"TBD", true},
		{"unknown", true},
		{"field", true},
		{"A detailed description of the field", false},
		{"This is a good description", false},
	}

	for _, tc := range cases {
		got := isLowQualityDescription(tc.desc)
		if got != tc.lowQual {
			t.Errorf("isLowQualityDescription(%q) = %v, want %v", tc.desc, got, tc.lowQual)
		}
	}
}

func TestInspect_InvalidHeader(t *testing.T) {
	env := InspectFromBytes([]byte("not a pulse file"), nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for invalid header")
	}
}

func TestInspect_InvalidSchema(t *testing.T) {
	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	buf.Write([]byte{0xFF})

	env := InspectFromBytes(buf.Bytes(), nil)
	if len(env.Errors) == 0 {
		t.Fatal("expected error for invalid schema")
	}
}

func TestInspect_CustomDictionaryLimit(t *testing.T) {
	values := make([]string, 50)
	for i := range values {
		values[i] = "v" + string(rune('A'+i%26))
	}
	dict := makeDictionary(t, values...)
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Description: "Categorical field test", Dictionary: dict},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, &InspectOptions{DictionaryLimit: 10})
	result := env.Data.(*InspectResult)
	field := result.Fields[0]
	if !field.Dictionary.Truncated {
		t.Error("expected Truncated = true with DictionaryLimit 10")
	}
	if len(field.Dictionary.Values) != 10 {
		t.Errorf("Values count = %d, want 10", len(field.Dictionary.Values))
	}
}

func TestPredict_FilterWithFieldSet(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "score", Values: []string{"1"}},
		},
	}

	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Errorf("unexpected errors: %v", env.Errors)
	}
}
