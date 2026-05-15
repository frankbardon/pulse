package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// schema for the null tests: one nullable_decimal128 field whose null
// state is honored by the reader / aggregators.
func nullTestSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeNullableDecimal128, Precision: 10, Scale: 2, Description: "Amount with nulls"},
		},
	}
}

// makeNullableRecord builds a Record where `amount` is null or carries
// the given value. Mirrors how the reader populates Record.values /
// Record.nulls when it reads a nullable_decimal128 null cell.
func makeNullableRecord(schema *encoding.Schema, value float64, isNull bool) *Record {
	values := map[string]float64{"amount": value}
	nulls := map[string]bool{}
	if isNull {
		nulls["amount"] = true
	}
	return NewRecordWithNulls(schema, values, nulls)
}

// TestAGG_NULL_COUNT_Buffered verifies the records-shaped (buffered)
// path returns the count of records whose field is null.
func TestAGG_NULL_COUNT_Buffered(t *testing.T) {
	schema := nullTestSchema(t)
	records := []*Record{
		makeNullableRecord(schema, 10, false),
		makeNullableRecord(schema, 0, true),
		makeNullableRecord(schema, 30, false),
		makeNullableRecord(schema, 0, true),
		makeNullableRecord(schema, 50, false),
	}
	factory, ok := aggregatorRegistry[types.AGG_NULL_COUNT]
	if !ok {
		t.Fatal("AGG_NULL_COUNT not registered")
	}
	agg, err := factory(&types.Aggregation{Type: types.AGG_NULL_COUNT, Field: "amount"}, schema)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	got, err := agg.Aggregate(records, "amount")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if got != 2 {
		t.Errorf("AGG_NULL_COUNT = %v, want 2", got)
	}
}

// TestAGG_NULL_COUNT_Streaming exercises the OnlineAggregator path —
// the streaming orchestrator drives UpdateRow / Finalize directly.
func TestAGG_NULL_COUNT_Streaming(t *testing.T) {
	schema := nullTestSchema(t)
	records := []*Record{
		makeNullableRecord(schema, 1, false),
		makeNullableRecord(schema, 0, true),
		makeNullableRecord(schema, 0, true),
		makeNullableRecord(schema, 4, false),
	}
	factory := aggregatorRegistry[types.AGG_NULL_COUNT]
	agg, err := factory(&types.Aggregation{Type: types.AGG_NULL_COUNT, Field: "amount"}, schema)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	online, ok := agg.(OnlineAggregator)
	if !ok {
		t.Fatalf("nullCountAggregator does not implement OnlineAggregator (%T)", agg)
	}
	for _, r := range records {
		if err := online.UpdateRow(r, "amount"); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	got, err := online.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if got != 2 {
		t.Errorf("AGG_NULL_COUNT streaming = %v, want 2", got)
	}
}

// TestFILTER_NULL_IsNull keeps records with a null field; non-null rows
// are dropped.
func TestFILTER_NULL_IsNull(t *testing.T) {
	schema := nullTestSchema(t)
	builder := newNullFilterer()
	fn, err := builder.Build(&types.Filterer{
		Type:   types.FILTER_NULL,
		Field:  "amount",
		Values: []string{"is_null"},
	}, schema)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cases := []struct {
		rec   *Record
		keep  bool
		label string
	}{
		{makeNullableRecord(schema, 5, false), false, "non-null dropped"},
		{makeNullableRecord(schema, 0, true), true, "null kept"},
	}
	for _, c := range cases {
		got, err := fn(c.rec)
		if err != nil {
			t.Fatalf("%s: filter error: %v", c.label, err)
		}
		if got != c.keep {
			t.Errorf("%s: keep=%v, want %v", c.label, got, c.keep)
		}
	}
}

// TestFILTER_NULL_IsNotNull mirrors the inverse mode.
func TestFILTER_NULL_IsNotNull(t *testing.T) {
	schema := nullTestSchema(t)
	fn, err := newNullFilterer().Build(&types.Filterer{
		Type:   types.FILTER_NULL,
		Field:  "amount",
		Values: []string{"is_not_null"},
	}, schema)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := fn(makeNullableRecord(schema, 7, false))
	if err != nil || !got {
		t.Errorf("non-null should pass is_not_null; got=%v err=%v", got, err)
	}
	got, err = fn(makeNullableRecord(schema, 0, true))
	if err != nil || got {
		t.Errorf("null should be dropped by is_not_null; got=%v err=%v", got, err)
	}
}

// TestFILTER_NULL_RejectsInvalidMode confirms invalid Values surface a
// PROCESSING_CONFIG error rather than silently passing every row.
func TestFILTER_NULL_RejectsInvalidMode(t *testing.T) {
	schema := nullTestSchema(t)
	_, err := newNullFilterer().Build(&types.Filterer{
		Type:   types.FILTER_NULL,
		Field:  "amount",
		Values: []string{"sometimes"},
	}, schema)
	if err == nil {
		t.Fatal("expected error for invalid FILTER_NULL Values, got nil")
	}
}
