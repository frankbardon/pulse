package processing

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func makeTestDict(t *testing.T, values ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("Dictionary.Add(%q): %v", v, err)
		}
	}
	return d
}

// rowsByKey indexes a Data slice by the named group field's value.
func rowsByKey(data []map[string]any, groupField string) map[string]map[string]any {
	out := make(map[string]map[string]any, len(data))
	for _, row := range data {
		k, _ := row[groupField].(string)
		out[k] = row
	}
	return out
}

// TestGroupedStreaming_CategoryParityWithBuffered runs the same request
// through both paths and asserts identical per-key aggregates.
func TestGroupedStreaming_CategoryParityWithBuffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "score"},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "letter grade",
				Dictionary: makeTestDict(t, "A", "B", "C")},
		},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 10, "grade": 0}),
		NewRecord(schema, map[string]float64{"score": 20, "grade": 0}),
		NewRecord(schema, map[string]float64{"score": 30, "grade": 1}),
		NewRecord(schema, map[string]float64{"score": 40, "grade": 1}),
		NewRecord(schema, map[string]float64{"score": 50, "grade": 2}),
	}

	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
	}

	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming Process: %v", err)
	}
	if stream.LastPath() != PathStreaming {
		t.Fatalf("expected streaming path, got %s", stream.LastPath())
	}

	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered processRecords: %v", err)
	}

	streamRows := rowsByKey(streamResp.Data, "grade")
	bufRows := rowsByKey(bufResp.Data, "grade")
	if len(streamRows) != len(bufRows) {
		t.Fatalf("group count mismatch: streaming=%d buffered=%d", len(streamRows), len(bufRows))
	}
	for key, want := range bufRows {
		got, ok := streamRows[key]
		if !ok {
			t.Errorf("streaming missing key %q", key)
			continue
		}
		gw, _ := got["s"].(float64)
		ww, _ := want["s"].(float64)
		if math.Abs(gw-ww) > 1e-9 {
			t.Errorf("key %q: streaming=%v buffered=%v", key, gw, ww)
		}
	}
}

// TestGroupedStreaming_RangeAggMultiple covers GROUP_RANGE × multiple
// online aggregators for parity with buffered processing.
func TestGroupedStreaming_RangeAggMultiple(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input value"},
		},
	}
	records := []*Record{
		makeNumericRecord(t, schema, "x", 5),
		makeNumericRecord(t, schema, "x", 12),
		makeNumericRecord(t, schema, "x", 18),
		makeNumericRecord(t, schema, "x", 25),
		makeNumericRecord(t, schema, "x", 33),
	}

	req := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 10}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			{Type: types.AGG_SUM, Field: "x", Label: "s"},
			{Type: types.AGG_AVERAGE, Field: "x", Label: "a"},
		},
	}

	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming: %v", err)
	}
	if stream.LastPath() != PathStreaming {
		t.Fatalf("expected streaming, got %s", stream.LastPath())
	}

	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}

	streamRows := rowsByKey(streamResp.Data, "x")
	bufRows := rowsByKey(bufResp.Data, "x")
	for key, want := range bufRows {
		got, ok := streamRows[key]
		if !ok {
			t.Errorf("streaming missing key %q", key)
			continue
		}
		for _, label := range []string{"n", "s", "a"} {
			gv, _ := got[label].(float64)
			wv, _ := want[label].(float64)
			if math.Abs(gv-wv) > 1e-9 {
				t.Errorf("key %q label %q: streaming=%v buffered=%v", key, label, gv, wv)
			}
		}
	}
}

// TestGroupedStreaming_NullKeySkipped: rows with null group values must
// be excluded from buckets in both paths.
func TestGroupedStreaming_NullKeySkipped(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input value"},
			{Name: "g", Type: encoding.FieldTypeCategoricalU8, Description: "group",
				Dictionary: makeTestDict(t, "A", "B")},
		},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"x": 1, "g": 0}),
		NewRecordWithNulls(schema, map[string]float64{"x": 2}, map[string]bool{"g": true}),
		NewRecord(schema, map[string]float64{"x": 3, "g": 1}),
	}

	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	}

	stream := NewProcessor(schema)
	resp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 group rows (null group skipped), got %d", len(resp.Data))
	}
}
