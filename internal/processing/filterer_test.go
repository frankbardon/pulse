package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeFilterFunc(t *testing.T, filterType types.FiltererType, field string, values []string, expr string, schema *encoding.Schema) FilterFunc {
	t.Helper()
	factory, ok := filtererRegistry[filterType]
	if !ok {
		t.Fatalf("no filterer registered for %s", filterType)
	}
	builder := factory()
	fn, err := builder.Build(&types.Filterer{
		Type:       filterType,
		Field:      field,
		Values:     values,
		Expression: expr,
	}, schema)
	if err != nil {
		t.Fatalf("build filter: %v", err)
	}
	return fn
}

func TestFilterer_Include_Basic(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_INCLUDE, "score", []string{"10", "20"}, "", schema)

	rec := NewRecord(schema, map[string]float64{"score": 10})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Error("expected record with score=10 to pass include filter")
	}

	rec2 := NewRecord(schema, map[string]float64{"score": 30})
	pass2, err := fn(rec2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass2 {
		t.Error("expected record with score=30 to fail include filter")
	}
}

func TestFilterer_Include_NullHandling(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_INCLUDE, "score", []string{"10"}, "", schema)

	rec := NewRecordWithNulls(schema, map[string]float64{"score": 0}, map[string]bool{"score": true})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass {
		t.Error("expected null record to fail include filter")
	}
}

func TestFilterer_Exclude_Basic(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_EXCLUDE, "score", []string{"10", "20"}, "", schema)

	rec := NewRecord(schema, map[string]float64{"score": 30})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Error("expected record with score=30 to pass exclude filter")
	}

	rec2 := NewRecord(schema, map[string]float64{"score": 10})
	pass2, err := fn(rec2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass2 {
		t.Error("expected record with score=10 to fail exclude filter")
	}
}

func TestFilterer_Exclude_NullHandling(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_EXCLUDE, "score", []string{"10"}, "", schema)

	rec := NewRecordWithNulls(schema, map[string]float64{"score": 0}, map[string]bool{"score": true})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Null values pass exclude filter (they're not in the exclude set)
	if !pass {
		t.Error("expected null record to pass exclude filter")
	}
}

func TestFilterer_Range_Basic(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_RANGE, "score", []string{"10", "30"}, "", schema)

	tests := []struct {
		value float64
		want  bool
	}{
		{15, true},
		{10, true},
		{30, true},
		{5, false},
		{35, false},
	}

	for _, tt := range tests {
		rec := NewRecord(schema, map[string]float64{"score": tt.value})
		pass, err := fn(rec)
		if err != nil {
			t.Fatalf("unexpected error for value %f: %v", tt.value, err)
		}
		if pass != tt.want {
			t.Errorf("range filter for score=%f: got %v, want %v", tt.value, pass, tt.want)
		}
	}
}

func TestFilterer_Range_NullHandling(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_RANGE, "score", []string{"10", "30"}, "", schema)

	rec := NewRecordWithNulls(schema, map[string]float64{"score": 0}, map[string]bool{"score": true})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass {
		t.Error("expected null record to fail range filter")
	}
}

func TestFilterer_Expression_Basic(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_EXPRESSION, "", nil, "score > 15", schema)

	rec := NewRecord(schema, map[string]float64{"score": 20})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Error("expected score=20 to pass score > 15")
	}

	rec2 := NewRecord(schema, map[string]float64{"score": 10})
	pass2, err := fn(rec2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass2 {
		t.Error("expected score=10 to fail score > 15")
	}
}

func TestFilterer_Expression_Compound(t *testing.T) {
	schema := numericSchema()
	fn := makeFilterFunc(t, types.FILTER_EXPRESSION, "", nil, "score >= 10 && age < 50", schema)

	rec := NewRecord(schema, map[string]float64{"score": 20, "age": 30})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Error("expected compound condition to pass")
	}

	rec2 := NewRecord(schema, map[string]float64{"score": 20, "age": 60})
	pass2, err := fn(rec2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass2 {
		t.Error("expected compound condition to fail for age=60")
	}
}

func TestFilterer_Expression_Categorical(t *testing.T) {
	schema := categoricalSchema()
	fn := makeFilterFunc(t, types.FILTER_EXPRESSION, "", nil, `brand == "Apple"`, schema)

	rec := NewRecord(schema, map[string]float64{"brand": 0, "score": 10})
	pass, err := fn(rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Error("expected Apple to pass brand == Apple")
	}

	rec2 := NewRecord(schema, map[string]float64{"brand": 1, "score": 10})
	pass2, err := fn(rec2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass2 {
		t.Error("expected Samsung to fail brand == Apple")
	}
}
