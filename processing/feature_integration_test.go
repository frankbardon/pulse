package processing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// makeNumericRecord builds a Record with one numeric field.
func makeNumericRecord(t *testing.T, schema *encoding.Schema, field string, value float64) *Record {
	t.Helper()
	return NewRecord(schema, map[string]float64{field: value})
}

// TestFeaturePipeline_LogThenAggregate verifies a per-row feature output
// is visible to downstream aggregation under the buffered path.
func TestFeaturePipeline_LogThenAggregate(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input value for log feature"},
		},
	}
	records := []*Record{
		makeNumericRecord(t, schema, "x", 0),       // log1p(0) = 0
		makeNumericRecord(t, schema, "x", math.E-1), // log1p(e-1) = 1
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_LOG, Field: "x", Label: "log_x"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "log_x", Label: "avg_log_x"},
		},
	}

	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected one row, got %d", len(resp.Data))
	}
	got := resp.Data[0]["avg_log_x"]
	v, ok := got.(float64)
	if !ok {
		t.Fatalf("avg_log_x type %T, want float64", got)
	}
	want := 0.5 // (0 + 1) / 2
	if math.Abs(v-want) > 1e-9 {
		t.Errorf("avg_log_x=%f, want %f", v, want)
	}
}

// TestFeaturePipeline_StreamableFeaturePicksStreamingPath verifies that
// requests whose every feature implements StreamingComputer flow
// through the streaming path with online-capable aggregators.
func TestFeaturePipeline_StreamableFeaturePicksStreamingPath(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "stream-eligible numeric field"},
		},
	}
	records := []*Record{
		makeNumericRecord(t, schema, "x", 4),
		makeNumericRecord(t, schema, "x", 9),
	}

	p := NewProcessor(schema)

	streamReq := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		},
	}
	if _, err := p.Process(context.Background(), streamReq, NewSliceIterator(records)); err != nil {
		t.Fatalf("Process (no features): %v", err)
	}
	if p.lastPath != PathStreaming {
		t.Errorf("baseline expected streaming, got %s", p.lastPath)
	}

	featReq := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_SQRT, Field: "x", Label: "sx"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		},
	}
	if _, err := p.Process(context.Background(), featReq, NewSliceIterator(records)); err != nil {
		t.Fatalf("Process (with features): %v", err)
	}
	if p.lastPath != PathStreaming {
		t.Errorf("with stream-eligible features expected streaming, got %s", p.lastPath)
	}
}

// TestFeaturePipeline_BucketizeThenFilter verifies feature output is
// reachable to filters that run after the feature stage.
func TestFeaturePipeline_BucketizeThenFilter(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input value to bucketize and filter"},
		},
	}
	records := []*Record{
		makeNumericRecord(t, schema, "x", 5),  // bucket 0
		makeNumericRecord(t, schema, "x", 15), // bucket 1
		makeNumericRecord(t, schema, "x", 25), // bucket 2
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Features: []*types.Feature{
			{
				Type:   types.FEAT_BUCKETIZE,
				Field:  "x",
				Label:  "bx",
				Params: json.RawMessage(`{"boundaries":[10,20]}`),
			},
		},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "bx", Values: []string{"1", "2"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		},
	}

	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	v, ok := resp.Data[0]["n"].(float64)
	if !ok {
		t.Fatalf("n type %T, want float64", resp.Data[0]["n"])
	}
	if v != 2 {
		t.Errorf("filtered count=%f, want 2 (buckets 1 and 2)", v)
	}
}
