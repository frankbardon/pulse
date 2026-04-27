package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func TestRecord_NumericValue_Present(t *testing.T) {
	schema := numericSchema()
	r := NewRecord(schema, map[string]float64{"score": 42})
	v, ok := r.NumericValue("score")
	if !ok || v != 42 {
		t.Errorf("NumericValue = %f, %v; want 42, true", v, ok)
	}
}

func TestRecord_NumericValue_Missing(t *testing.T) {
	schema := numericSchema()
	r := NewRecord(schema, map[string]float64{"score": 42})
	v, ok := r.NumericValue("nonexistent")
	if ok || v != 0 {
		t.Errorf("NumericValue = %f, %v; want 0, false", v, ok)
	}
}

func TestRecord_NumericValue_Null(t *testing.T) {
	schema := numericSchema()
	r := NewRecordWithNulls(schema, map[string]float64{"score": 42}, map[string]bool{"score": true})
	v, ok := r.NumericValue("score")
	if ok || v != 0 {
		t.Errorf("NumericValue on null = %f, %v; want 0, false", v, ok)
	}
}

func TestRecord_StringValue_Categorical(t *testing.T) {
	schema := categoricalSchema()
	r := NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	s, ok := r.StringValue("brand")
	if !ok || s != "Apple" {
		t.Errorf("StringValue = %q, %v; want Apple, true", s, ok)
	}
}

func TestRecord_StringValue_NonCategorical(t *testing.T) {
	schema := numericSchema()
	r := NewRecord(schema, map[string]float64{"score": 10})
	s, ok := r.StringValue("score")
	if ok || s != "" {
		t.Errorf("StringValue on numeric = %q, %v; want empty, false", s, ok)
	}
}

func TestRecord_StringValue_Null(t *testing.T) {
	schema := categoricalSchema()
	r := NewRecordWithNulls(schema, map[string]float64{"brand": 0}, map[string]bool{"brand": true})
	s, ok := r.StringValue("brand")
	if ok || s != "" {
		t.Errorf("StringValue on null = %q, %v; want empty, false", s, ok)
	}
}

func TestRecord_StringValue_MissingField(t *testing.T) {
	schema := categoricalSchema()
	r := NewRecord(schema, map[string]float64{"brand": 0})
	s, ok := r.StringValue("nonexistent")
	if ok || s != "" {
		t.Errorf("StringValue on missing = %q, %v; want empty, false", s, ok)
	}
}

func TestRecord_StringValue_MissingValue(t *testing.T) {
	schema := categoricalSchema()
	r := NewRecord(schema, map[string]float64{}) // no brand value
	s, ok := r.StringValue("brand")
	if ok || s != "" {
		t.Errorf("StringValue on missing value = %q, %v; want empty, false", s, ok)
	}
}

func TestRecord_StringValue_OutOfRangeID(t *testing.T) {
	schema := categoricalSchema()
	r := NewRecord(schema, map[string]float64{"brand": 999}) // out of range
	s, ok := r.StringValue("brand")
	if ok || s != "" {
		t.Errorf("StringValue with bad ID = %q, %v; want empty, false", s, ok)
	}
}

func TestRecord_Schema(t *testing.T) {
	schema := numericSchema()
	r := NewRecord(schema, map[string]float64{"score": 42})
	if r.Schema() != schema {
		t.Error("Schema() did not return expected schema")
	}
}

func TestRecord_AllValues(t *testing.T) {
	schema := categoricalSchema()
	r := NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	vals := r.AllValues()

	// brand should be resolved to string
	if vals["brand"] != "Apple" {
		t.Errorf("AllValues brand = %v, want Apple", vals["brand"])
	}
	// score should be float64
	if vals["score"] != 10.0 {
		t.Errorf("AllValues score = %v, want 10.0", vals["score"])
	}
}

func TestRecord_AllValues_NullSkipped(t *testing.T) {
	schema := numericSchema()
	r := NewRecordWithNulls(schema, map[string]float64{"score": 42}, map[string]bool{"score": true})
	vals := r.AllValues()
	if _, ok := vals["score"]; ok {
		t.Error("AllValues should skip null fields")
	}
}

func TestNewRecordWithNulls_NilNulls(t *testing.T) {
	schema := numericSchema()
	r := NewRecordWithNulls(schema, map[string]float64{"score": 42}, nil)
	v, ok := r.NumericValue("score")
	if !ok || v != 42 {
		t.Errorf("NumericValue = %f, %v; want 42, true", v, ok)
	}
}

func TestSliceIterator_Reset(t *testing.T) {
	schema := numericSchema()
	records := makeRecords(schema, "score", []float64{10, 20})
	iter := NewSliceIterator(records)

	// First pass
	count := 0
	for iter.Next() {
		count++
	}
	if count != 2 {
		t.Errorf("first pass count = %d, want 2", count)
	}

	// Reset and second pass
	iter.Reset()
	count = 0
	for iter.Next() {
		count++
	}
	if count != 2 {
		t.Errorf("second pass count = %d, want 2", count)
	}
}

func TestSliceIterator_Empty(t *testing.T) {
	iter := NewSliceIterator([]*Record{})
	if iter.Next() {
		t.Error("expected Next() to return false on empty iterator")
	}
}

func TestRecord_StringValue_NilDictionary(t *testing.T) {
	// Create a categorical field without a dictionary
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: nil},
		},
	}
	r := NewRecord(schema, map[string]float64{"cat": 0})
	s, ok := r.StringValue("cat")
	if ok || s != "" {
		t.Errorf("StringValue with nil dict = %q, %v; want empty, false", s, ok)
	}
}
