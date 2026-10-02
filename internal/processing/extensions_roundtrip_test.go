package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// fakeBrandScoreAggregator emits a constant sentinel value for every
// record it consumes — a stand-in for an embedder-shipped composite
// scoring aggregator. Implements both Aggregator and OnlineAggregator
// so it can ride the streaming path.
type fakeBrandScoreAggregator struct{ count int }

func (a *fakeBrandScoreAggregator) Aggregate(records []*Record, field string) (float64, error) {
	_, _ = records, field
	return 42.0, nil
}

func (a *fakeBrandScoreAggregator) UpdateRow(record *Record, field string) error {
	_, _ = record, field
	a.count++
	return nil
}

func (a *fakeBrandScoreAggregator) Finalize() (float64, error) { return 42.0, nil }

func fakeBrandScoreFactory(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
	return &fakeBrandScoreAggregator{}, nil
}

// TestExtensions_AggregatorRoundTrip_Streaming asserts that a custom
// aggregator registered via the overlay flows through the streaming
// Process path: canStream sees the OnlineAggregator and the result
// appears in Response.Data under the operator's label.
func TestExtensions_AggregatorRoundTrip_Streaming(t *testing.T) {
	records, schema := benchRecords(10)
	exts := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			"AGG_ACME_BRAND_SCORE": fakeBrandScoreFactory,
		},
		Streamable: map[string]bool{
			StreamabilityKey("aggregator", "AGG_ACME_BRAND_SCORE"): true,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_BRAND_SCORE", Field: "score", Label: "brand"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 result row, got %d", len(resp.Data))
	}
	got, ok := resp.Data[0]["brand"]
	if !ok {
		t.Fatalf("result row missing brand label; have %v", resp.Data[0])
	}
	if got.(float64) != 42.0 {
		t.Errorf("custom aggregator value = %v, want 42.0", got)
	}
	if proc.LastPath() != PathStreaming {
		t.Errorf("expected streaming path, got %s", proc.LastPath())
	}
}

// TestExtensions_AggregatorRoundTrip_Buffered exercises the buffered
// path: a custom aggregator that does NOT implement OnlineAggregator
// forces the orchestrator into PathBuffered. The result must still
// flow through the per-aggregation lookup.
func TestExtensions_AggregatorRoundTrip_Buffered(t *testing.T) {
	records, schema := benchRecords(5)
	exts := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			"AGG_ACME_BUFFERED_COUNT": bufferedOnlyFactory,
		},
		// Streamable defaults to false for this name.
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_BUFFERED_COUNT", Field: "score", Label: "n"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Data[0]["n"].(float64) != 99.0 {
		t.Errorf("buffered aggregator emitted %v, want 99.0", resp.Data[0]["n"])
	}
	if proc.LastPath() != PathBuffered {
		t.Errorf("expected buffered path, got %s", proc.LastPath())
	}
}

// bufferedOnlyAggregator implements only Aggregator — not
// OnlineAggregator. Forces canStream to reject the streaming path.
type bufferedOnlyAggregator struct{}

func (bufferedOnlyAggregator) Aggregate([]*Record, string) (float64, error) { return 99.0, nil }

func bufferedOnlyFactory(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
	return bufferedOnlyAggregator{}, nil
}

// TestExtensions_AggregatorRoundTrip_OverlayOverridesBuiltin
// asserts that registering AGG_COUNT as an overlay shadows the
// built-in factory. Used by embedders that need to replace stock
// behaviour without renaming.
func TestExtensions_AggregatorRoundTrip_OverlayOverridesBuiltin(t *testing.T) {
	records, schema := benchRecords(5)
	called := 0
	overlayFactory := AggregatorFactory(func(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
		called++
		return bufferedOnlyAggregator{}, nil
	})
	exts := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			types.AGG_COUNT: overlayFactory,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	if _, err := proc.Process(context.Background(), req, NewSliceIterator(records)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if called == 0 {
		t.Error("overlay factory was not invoked; built-in AGG_COUNT shadowed instead of being shadowed")
	}
}
