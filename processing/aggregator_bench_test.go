package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// benchRecords builds N records with a single numeric "score" field whose
// values cycle deterministically so aggregations have non-trivial input.
func benchRecords(n int) ([]*Record, *encoding.Schema) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := make([]*Record, n)
	for i := range n {
		records[i] = NewRecord(schema, map[string]float64{"score": float64((i * 37) % 1009)})
	}
	return records, schema
}

// BenchmarkProcessor_ManyAggregationsSameField issues 8 distinct aggregations
// over the same field via the orchestrator. With the collect cache, the
// records should be scanned exactly once; without it, 8 times.
func BenchmarkProcessor_ManyAggregationsSameField(b *testing.B) {
	records, schema := benchRecords(10_000)
	proc := NewProcessor(schema)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
			{Type: types.AGG_MIN, Field: "score", Label: "min"},
			{Type: types.AGG_MAX, Field: "score", Label: "max"},
			{Type: types.AGG_STDDEV, Field: "score", Label: "sd"},
			{Type: types.AGG_VARIANCE, Field: "score", Label: "var"},
			{Type: types.AGG_MEDIAN, Field: "score", Label: "med"},
		},
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		iter := NewSliceIterator(records)
		_, err := proc.Process(ctx, req, iter)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProcessor_ManyAggregationsManyFields issues aggregations over
// different fields; the cache populates one slice per field but doesn't
// share work across fields. Validates the pool path.
func BenchmarkProcessor_ManyAggregationsManyFields(b *testing.B) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "a", Type: encoding.FieldTypeF64},
			{Name: "b", Type: encoding.FieldTypeF64},
			{Name: "c", Type: encoding.FieldTypeF64},
			{Name: "d", Type: encoding.FieldTypeF64},
		},
	}
	records := make([]*Record, 10_000)
	for i := range records {
		records[i] = NewRecord(schema, map[string]float64{
			"a": float64(i),
			"b": float64(i * 3),
			"c": float64(i % 17),
			"d": float64(i * 11),
		})
	}
	proc := NewProcessor(schema)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "a", Label: "a_avg"},
			{Type: types.AGG_SUM, Field: "b", Label: "b_sum"},
			{Type: types.AGG_MAX, Field: "c", Label: "c_max"},
			{Type: types.AGG_STDDEV, Field: "d", Label: "d_sd"},
		},
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		iter := NewSliceIterator(records)
		_, err := proc.Process(ctx, req, iter)
		if err != nil {
			b.Fatal(err)
		}
	}
}
