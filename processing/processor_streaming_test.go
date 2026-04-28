package processing

import (
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestProcessor_StreamingPathSelected verifies the orchestrator picks
// the streaming path for a request that has only online aggregations,
// no groups, no attributes, and only row-level filters.
func TestProcessor_StreamingPathSelected(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30, 40, 50})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
			{Type: types.AGG_MIN, Field: "score", Label: "lo"},
			{Type: types.AGG_MAX, Field: "score", Label: "hi"},
		},
	}

	if _, err := proc.Process(context.Background(), req, iter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Errorf("LastPath = %s, want streaming", proc.LastPath())
	}
}

// TestProcessor_StreamingPathFallsBackForBufferedAgg verifies that any
// non-online aggregator (MEDIAN here) forces the buffered path.
func TestProcessor_StreamingPathFallsBackForBufferedAgg(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30, 40, 50})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
			{Type: types.AGG_MEDIAN, Field: "score", Label: "med"},
		},
	}

	if _, err := proc.Process(context.Background(), req, iter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proc.LastPath() != PathBuffered {
		t.Errorf("LastPath = %s, want buffered (MEDIAN forces fallback)", proc.LastPath())
	}
}

// TestProcessor_StreamingFallsBackForGroups verifies grouping always
// uses the buffered path.
func TestProcessor_StreamingFallsBackForGroups(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "age", []float64{25, 30, 25})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "age"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "age", Label: "n"},
		},
	}

	if _, err := proc.Process(context.Background(), req, iter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proc.LastPath() != PathBuffered {
		t.Errorf("LastPath = %s, want buffered (groups force fallback)", proc.LastPath())
	}
}

// TestProcessor_StreamingFallsBackForAttributes verifies attributes
// always use the buffered path.
func TestProcessor_StreamingFallsBackForAttributes(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_NORMALIZED, Field: "score", Label: "n"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "n", Label: "avg"},
		},
	}

	if _, err := proc.Process(context.Background(), req, iter); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proc.LastPath() != PathBuffered {
		t.Errorf("LastPath = %s, want buffered (attributes force fallback)", proc.LastPath())
	}
}

// TestProcessor_StreamingFiltersWork verifies row-level filters compose
// with the streaming path (FilteredRows reflects post-filter count).
func TestProcessor_StreamingFiltersWork(t *testing.T) {
	schema := numericSchema()
	proc := NewProcessor(schema)

	records := makeRecords(schema, "score", []float64{10, 20, 30, 40, 50})
	iter := NewSliceIterator(records)

	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"20", "40"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	resp, err := proc.Process(context.Background(), req, iter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Fatalf("LastPath = %s, want streaming", proc.LastPath())
	}
	if resp.Metadata.TotalRows != 5 {
		t.Errorf("TotalRows = %d, want 5", resp.Metadata.TotalRows)
	}
	if resp.Metadata.FilteredRows != 3 {
		t.Errorf("FilteredRows = %d, want 3", resp.Metadata.FilteredRows)
	}
	row := resp.Data[0]
	if n := row["n"].(float64); n != 3 {
		t.Errorf("count = %f, want 3", n)
	}
	if total := row["total"].(float64); total != 90 {
		t.Errorf("sum = %f, want 90 (20+30+40)", total)
	}
}

// onlineAggCases returns the set of online aggregator type constants
// the equivalence test should iterate over. MEDIAN, PERCENTILE, ZSCORE
// are intentionally excluded — they are non-online.
func onlineAggCases() []types.AggregationType {
	return []types.AggregationType{
		types.AGG_COUNT,
		types.AGG_SUM,
		types.AGG_AVERAGE,
		types.AGG_MIN,
		types.AGG_MAX,
		types.AGG_RANGE,
		types.AGG_VARIANCE,
		types.AGG_STDDEV,
		types.AGG_SKEWNESS,
		types.AGG_KURTOSIS,
		types.AGG_DISTINCT_COUNT,
		types.AGG_FREQUENCY,
		types.AGG_MODE,
	}
}

// TestProcessor_StreamingMatchesBuffered runs a fixed dataset through
// both paths for every online aggregator and asserts the results match
// within float64 tolerance. This is the correctness backbone of Unit
// 11: the streaming refactor must produce byte-equivalent output (for
// integer aggregators) and float-equivalent output (for moment-based
// aggregators) compared to the buffered path.
func TestProcessor_StreamingMatchesBuffered(t *testing.T) {
	schema := numericSchema()
	rng := rand.New(rand.NewSource(0xC0FFEE))
	values := make([]float64, 1000)
	for i := range values {
		values[i] = rng.NormFloat64()*100 + 50
	}
	records := makeRecords(schema, "score", values)

	for _, aggType := range onlineAggCases() {
		t.Run(string(aggType), func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: aggType, Field: "score", Label: "v"},
				},
			}

			procStream := NewProcessor(schema)
			respStream, err := procStream.Process(context.Background(), req, NewSliceIterator(records))
			if err != nil {
				t.Fatalf("streaming: %v", err)
			}
			if procStream.LastPath() != PathStreaming {
				t.Fatalf("expected streaming path, got %s", procStream.LastPath())
			}

			// Force buffered by adding a no-op MEDIAN we throw away?
			// Cleaner: call processRecords directly with the records.
			procBuf := NewProcessor(schema)
			respBuf, err := procBuf.processRecords(context.Background(), req, records)
			if err != nil {
				t.Fatalf("buffered: %v", err)
			}

			gotStream := respStream.Data[0]["v"].(float64)
			gotBuf := respBuf.Data[0]["v"].(float64)
			// 1e-8 tolerance is comfortable for Welford on 1000 samples.
			// COUNT/MIN/MAX/MODE/etc. should match exactly; moment-based
			// match within tens of ulps.
			eps := 1e-8 * math.Max(1, math.Abs(gotBuf))
			if math.Abs(gotStream-gotBuf) > eps {
				t.Errorf("%s: streaming=%v buffered=%v diff=%g eps=%g",
					aggType, gotStream, gotBuf, gotStream-gotBuf, eps)
			}
		})
	}
}

// TestProcessor_StreamingMatchesBuffered_NullsAndCombinations covers
// a small mixed dataset with nulls and a multi-aggregation request to
// verify that batching multiple online aggregators in one streaming
// pass produces identical output to buffered.
func TestProcessor_StreamingMatchesBuffered_NullsAndCombinations(t *testing.T) {
	schema := numericSchema()
	records := makeRecordsWithNulls(schema, "score",
		[]float64{1, 0, 2, 3, 0, 5, 8, 13, 0, 21},
		[]int{1, 4, 8})

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "sum"},
			{Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
			{Type: types.AGG_VARIANCE, Field: "score", Label: "var"},
			{Type: types.AGG_STDDEV, Field: "score", Label: "sd"},
			{Type: types.AGG_MIN, Field: "score", Label: "lo"},
			{Type: types.AGG_MAX, Field: "score", Label: "hi"},
			{Type: types.AGG_RANGE, Field: "score", Label: "range"},
			{Type: types.AGG_DISTINCT_COUNT, Field: "score", Label: "distinct"},
			{Type: types.AGG_FREQUENCY, Field: "score", Label: "freq"},
			{Type: types.AGG_MODE, Field: "score", Label: "mode"},
			{Type: types.AGG_SKEWNESS, Field: "score", Label: "skew"},
			{Type: types.AGG_KURTOSIS, Field: "score", Label: "kurt"},
		},
	}

	procStream := NewProcessor(schema)
	respStream, err := procStream.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming: %v", err)
	}
	if procStream.LastPath() != PathStreaming {
		t.Fatalf("expected streaming, got %s", procStream.LastPath())
	}

	procBuf := NewProcessor(schema)
	respBuf, err := procBuf.processRecords(context.Background(), req, records)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}

	for label := range respBuf.Data[0] {
		gs := respStream.Data[0][label].(float64)
		gb := respBuf.Data[0][label].(float64)
		eps := 1e-8 * math.Max(1, math.Abs(gb))
		if math.Abs(gs-gb) > eps {
			t.Errorf("%s: streaming=%v buffered=%v diff=%g",
				label, gs, gb, gs-gb)
		}
	}
}
