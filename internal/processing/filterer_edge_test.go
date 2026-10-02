package processing

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func numericCategoricalSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	if _, err := dict.Add("100"); err != nil {
		t.Fatalf("dict.Add: %v", err)
	}
	if _, err := dict.Add("2178"); err != nil {
		t.Fatalf("dict.Add: %v", err)
	}
	if _, err := dict.Add("500"); err != nil {
		t.Fatalf("dict.Add: %v", err)
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand_code", Type: encoding.FieldTypeCategoricalU16, Dictionary: dict},
		},
	}
}

func TestFilterer_Range_InvalidValues(t *testing.T) {
	schema := numericSchema()
	factory := filtererRegistry[types.FILTER_RANGE]
	builder := factory()

	// Not enough values
	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_RANGE,
		Field:  "score",
		Values: []string{"10"},
	}, schema)
	if err == nil {
		t.Error("expected error for range filter with 1 value")
	}
}

func TestFilterer_Range_InvalidMinValue(t *testing.T) {
	schema := numericSchema()
	factory := filtererRegistry[types.FILTER_RANGE]
	builder := factory()

	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_RANGE,
		Field:  "score",
		Values: []string{"abc", "30"},
	}, schema)
	if err == nil {
		t.Error("expected error for non-numeric min value")
	}
}

func TestFilterer_Range_InvalidMaxValue(t *testing.T) {
	schema := numericSchema()
	factory := filtererRegistry[types.FILTER_RANGE]
	builder := factory()

	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_RANGE,
		Field:  "score",
		Values: []string{"10", "abc"},
	}, schema)
	if err == nil {
		t.Error("expected error for non-numeric max value")
	}
}

func TestFilterer_Expression_Empty(t *testing.T) {
	schema := numericSchema()
	factory := filtererRegistry[types.FILTER_EXPRESSION]
	builder := factory()

	_, err := builder.Build(&types.Filterer{
		Type:       types.FILTER_EXPRESSION,
		Expression: "",
	}, schema)
	if err == nil {
		t.Error("expected error for empty expression")
	}
}

func TestFilterer_Include_InvalidValues(t *testing.T) {
	schema := numericSchema()
	factory := filtererRegistry[types.FILTER_INCLUDE]
	builder := factory()

	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_INCLUDE,
		Field:  "score",
		Values: []string{"abc"}, // non-numeric, non-categorical
	}, schema)
	if err == nil {
		t.Error("expected error for non-numeric include value")
	}
}

func TestFilterer_Exclude_InvalidValues(t *testing.T) {
	schema := numericSchema()
	factory := filtererRegistry[types.FILTER_EXCLUDE]
	builder := factory()

	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_EXCLUDE,
		Field:  "score",
		Values: []string{"abc"},
	}, schema)
	if err == nil {
		t.Error("expected error for non-numeric exclude value")
	}
}

func TestFilterer_Include_CategoricalValues(t *testing.T) {
	schema := categoricalSchema()
	fn := makeFilterFunc(t, types.FILTER_INCLUDE, "brand", []string{"Apple"}, "", schema)

	rec := NewRecord(schema, map[string]float64{"brand": 0}) // Apple
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Error("expected Apple to pass include filter for Apple")
	}

	rec2 := NewRecord(schema, map[string]float64{"brand": 1}) // Samsung
	pass2, err := fn(rec2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass2 {
		t.Error("expected Samsung to fail include filter for Apple")
	}
}

func TestFilterer_Exclude_CategoricalValues(t *testing.T) {
	schema := categoricalSchema()
	fn := makeFilterFunc(t, types.FILTER_EXCLUDE, "brand", []string{"Apple"}, "", schema)

	rec := NewRecord(schema, map[string]float64{"brand": 0}) // Apple
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass {
		t.Error("expected Apple to fail exclude filter for Apple")
	}

	rec2 := NewRecord(schema, map[string]float64{"brand": 1}) // Samsung
	pass2, err := fn(rec2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass2 {
		t.Error("expected Samsung to pass exclude filter for Apple")
	}
}

// Regression: numeric-string dictionary entries must be resolved via the
// dictionary, not parsed as floats. Previously "2178" was parsed as 2178.0
// and compared against the encoded dictionary index, which silently never
// matched.
func TestFilterer_Include_CategoricalNumericValues(t *testing.T) {
	schema := numericCategoricalSchema(t)
	fn := makeFilterFunc(t, types.FILTER_INCLUDE, "brand_code", []string{"2178"}, "", schema)

	// Dictionary order: "100" -> 0, "2178" -> 1, "500" -> 2.
	rec100 := NewRecord(schema, map[string]float64{"brand_code": 0})
	rec2178 := NewRecord(schema, map[string]float64{"brand_code": 1})
	rec500 := NewRecord(schema, map[string]float64{"brand_code": 2})

	pass, err := fn(rec2178)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Error("expected record at dict index 1 (\"2178\") to pass include filter")
	}

	for _, rec := range []*Record{rec100, rec500} {
		pass, err := fn(rec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pass {
			t.Error("expected non-matching record to fail include filter")
		}
	}
}

func TestFilterer_Exclude_CategoricalNumericValues(t *testing.T) {
	schema := numericCategoricalSchema(t)
	fn := makeFilterFunc(t, types.FILTER_EXCLUDE, "brand_code", []string{"2178"}, "", schema)

	rec100 := NewRecord(schema, map[string]float64{"brand_code": 0})
	rec2178 := NewRecord(schema, map[string]float64{"brand_code": 1})
	rec500 := NewRecord(schema, map[string]float64{"brand_code": 2})

	pass, err := fn(rec2178)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass {
		t.Error("expected record at dict index 1 (\"2178\") to be excluded")
	}

	for _, rec := range []*Record{rec100, rec500} {
		pass, err := fn(rec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !pass {
			t.Error("expected non-matching record to pass exclude filter")
		}
	}
}

func TestFilterer_Include_CategoricalValueNotInDictionary(t *testing.T) {
	schema := numericCategoricalSchema(t)
	factory := filtererRegistry[types.FILTER_INCLUDE]
	builder := factory()

	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_INCLUDE,
		Field:  "brand_code",
		Values: []string{"9999"},
	}, schema)
	if err == nil {
		t.Fatal("expected error for categorical filter value missing from dictionary")
	}
	msg := err.Error()
	if !strings.Contains(msg, "brand_code") || !strings.Contains(msg, "9999") {
		t.Errorf("error should mention field and value, got: %v", err)
	}
}

func TestFilterer_Exclude_CategoricalValueNotInDictionary(t *testing.T) {
	schema := numericCategoricalSchema(t)
	factory := filtererRegistry[types.FILTER_EXCLUDE]
	builder := factory()

	_, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_EXCLUDE,
		Field:  "brand_code",
		Values: []string{"9999"},
	}, schema)
	if err == nil {
		t.Fatal("expected error for categorical filter value missing from dictionary")
	}
	msg := err.Error()
	if !strings.Contains(msg, "brand_code") || !strings.Contains(msg, "9999") {
		t.Errorf("error should mention field and value, got: %v", err)
	}
}
