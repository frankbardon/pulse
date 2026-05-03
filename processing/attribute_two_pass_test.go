package processing

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// makeNumericRecords returns N records with the given values for one
// numeric field. Helper local to two-pass tests.
func makeNumericRecords(t *testing.T, schema *encoding.Schema, field string, values []float64) []*Record {
	t.Helper()
	recs := make([]*Record, len(values))
	for i, v := range values {
		recs[i] = NewRecord(schema, map[string]float64{field: v})
	}
	return recs
}

// runStreamingAndBuffered drives the same request through both paths
// and returns (streamingResp, bufferedResp) for downstream parity
// assertion. Records are cloned for the buffered run to avoid sharing
// mutated value maps with the streaming run.
func runStreamingAndBuffered(t *testing.T, schema *encoding.Schema, records []*Record, req *types.Request) (*types.Response, *types.Response) {
	t.Helper()

	stream := NewProcessor(schema)
	streamResp, err := stream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming Process: %v", err)
	}

	// Re-build records to avoid attribute-injected labels from the
	// streaming run leaking into the buffered comparison.
	bufRecs := make([]*Record, len(records))
	for i, r := range records {
		clone := make(map[string]float64, len(r.values))
		for k, v := range r.values {
			clone[k] = v
		}
		bufRecs[i] = NewRecord(schema, clone)
	}
	buf := NewProcessor(schema)
	bufResp, err := buf.processRecords(context.Background(), req, bufRecs)
	if err != nil {
		t.Fatalf("buffered processRecords: %v", err)
	}
	return streamResp, bufResp
}

// TestTwoPass_ZScoreParity confirms ATTR_ZSCORE streaming output
// matches the buffered path on a non-trivial distribution.
func TestTwoPass_ZScoreParity(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input"},
		},
	}
	records := makeNumericRecords(t, schema, "x", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "z", Label: "sum_z"},
			{Type: types.AGG_AVERAGE, Field: "z", Label: "avg_z"},
		},
	}

	streamResp, bufResp := runStreamingAndBuffered(t, schema, records, req)
	for _, label := range []string{"sum_z", "avg_z"} {
		sv, _ := streamResp.Data[0][label].(float64)
		bv, _ := bufResp.Data[0][label].(float64)
		if math.Abs(sv-bv) > 1e-9 {
			t.Errorf("%s: streaming=%v buffered=%v", label, sv, bv)
		}
	}
}

// TestTwoPass_TScoreParity covers ATTR_TSCORE.
func TestTwoPass_TScoreParity(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input"},
		},
	}
	records := makeNumericRecords(t, schema, "x", []float64{2, 4, 6, 8, 10})

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_TSCORE, Field: "x", Label: "t"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "t", Label: "sum_t"},
		},
	}

	streamResp, bufResp := runStreamingAndBuffered(t, schema, records, req)
	sv, _ := streamResp.Data[0]["sum_t"].(float64)
	bv, _ := bufResp.Data[0]["sum_t"].(float64)
	if math.Abs(sv-bv) > 1e-9 {
		t.Errorf("sum_t: streaming=%v buffered=%v", sv, bv)
	}
}

// TestTwoPass_NormalizedParity covers ATTR_NORMALIZED.
func TestTwoPass_NormalizedParity(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input"},
		},
	}
	records := makeNumericRecords(t, schema, "x", []float64{5, 10, 15, 20, 25})

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_NORMALIZED, Field: "x", Label: "n"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "n", Label: "sum_n"},
			{Type: types.AGG_AVERAGE, Field: "n", Label: "avg_n"},
		},
	}

	streamResp, bufResp := runStreamingAndBuffered(t, schema, records, req)
	for _, label := range []string{"sum_n", "avg_n"} {
		sv, _ := streamResp.Data[0][label].(float64)
		bv, _ := bufResp.Data[0][label].(float64)
		if math.Abs(sv-bv) > 1e-9 {
			t.Errorf("%s: streaming=%v buffered=%v", label, sv, bv)
		}
	}
}

// TestTwoPass_RowLocalAttributePresentAlsoStreams confirms a request
// mixing FORMULA (row-local) and ZSCORE (two-pass) takes the two-pass
// orchestrator and produces correct values for both.
func TestTwoPass_RowLocalAttributePresentAlsoStreams(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input"},
		},
	}
	records := makeNumericRecords(t, schema, "x", []float64{1, 2, 3, 4, 5})

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "x", Expression: "x * 10", Label: "x10"},
			{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x10", Label: "sum_x10"},
			{Type: types.AGG_SUM, Field: "z", Label: "sum_z"},
		},
	}

	streamResp, bufResp := runStreamingAndBuffered(t, schema, records, req)
	for _, label := range []string{"sum_x10", "sum_z"} {
		sv, _ := streamResp.Data[0][label].(float64)
		bv, _ := bufResp.Data[0][label].(float64)
		if math.Abs(sv-bv) > 1e-9 {
			t.Errorf("%s: streaming=%v buffered=%v", label, sv, bv)
		}
	}
}

// TestTwoPass_FilterAppliesToPrePass confirms filter-passing records
// drive PrePass; rows excluded by filters do not affect the population
// stats. Mirrors buffered behavior where Compute receives the filtered
// slice.
func TestTwoPass_FilterAppliesToPrePass(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input"},
		},
	}
	records := makeNumericRecords(t, schema, "x", []float64{1, 2, 3, 100, 200})

	// Filter out the two large outliers; population stats should reflect
	// only {1, 2, 3}.
	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "x", Values: []string{"0", "10"}},
		},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_NORMALIZED, Field: "x", Label: "n"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "n", Label: "sum_n"},
		},
	}

	streamResp, bufResp := runStreamingAndBuffered(t, schema, records, req)
	sv, _ := streamResp.Data[0]["sum_n"].(float64)
	bv, _ := bufResp.Data[0]["sum_n"].(float64)
	if math.Abs(sv-bv) > 1e-9 {
		t.Errorf("sum_n: streaming=%v buffered=%v", sv, bv)
	}
	// Verify pass 2 also filters: filtered_rows reflects post-filter count.
	if streamResp.Metadata.FilteredRows != 3 {
		t.Errorf("FilteredRows = %d, want 3", streamResp.Metadata.FilteredRows)
	}
}
