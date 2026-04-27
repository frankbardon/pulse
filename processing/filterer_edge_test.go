package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

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
