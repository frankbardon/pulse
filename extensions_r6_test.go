package pulse_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// r6Aggregator counts which of its two paths the engine drives. It
// implements both Aggregator (buffered) and OnlineAggregator
// (streaming); a Streamable=true registration promises the engine may
// take the online path.
type r6Aggregator struct {
	updates, aggregates *atomic.Int64
	sum                 float64
}

func (a *r6Aggregator) Aggregate(rows extend.Rows, field string) (float64, error) {
	a.aggregates.Add(1)
	for i := 0; i < rows.Len(); i++ {
		r := rows.At(i)
		if v, ok := r.NumericValue(field); ok {
			a.sum += v
		}
	}
	return a.sum, nil
}

func (a *r6Aggregator) UpdateRow(r extend.Record, field string) error {
	a.updates.Add(1)
	if v, ok := r.NumericValue(field); ok {
		a.sum += v
	}
	return nil
}

func (a *r6Aggregator) Finalize() (float64, error) { return a.sum, nil }

// TestExtensions_StreamableWithComponentsTakesOnlinePath is the R6
// regression: a Streamable=true aggregator that ALSO supplies a
// ComponentsFunc must still be driven through its OnlineAggregator
// methods. The historical meta wrapper embedded only the Aggregator
// interface, so the engine's OnlineAggregator assertion failed on the
// wrapper and the request silently fell back to the buffered path.
func TestExtensions_StreamableWithComponentsTakesOnlinePath(t *testing.T) {
	var updates, aggregates atomic.Int64
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:        "AGG_ACME_R6_SUM",
			Description: "Sum that records which execution path ran.",
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return &r6Aggregator{updates: &updates, aggregates: &aggregates}, nil
			},
			Streamable:      true,
			Accepts:         []encoding.FieldType{encoding.FieldTypeF64},
			ComponentSchema: brandScoreSchema(),
			ComponentsFunc: func(extend.Aggregator) (map[string]any, error) {
				return map[string]any{"sum": 1.0}, nil
			},
		}},
	}
	p, path := brandScoreCohort(t, ext)
	updates.Store(0)
	aggregates.Store(0)
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{{Type: "AGG_ACME_R6_SUM", Field: "score", Label: "s"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := updates.Load(); got != 5 {
		t.Errorf("UpdateRow calls = %d, want 5 (online path); Aggregate calls = %d", got, aggregates.Load())
	}
	if got := aggregates.Load(); got != 0 {
		t.Errorf("Aggregate calls = %d, want 0 (buffered fallback taken)", got)
	}
	if resp.Components == nil || len(resp.Components.Aggregations) != 1 ||
		resp.Components.Aggregations[0].Operator["sum"] != 1.0 {
		t.Errorf("ComponentsFunc emission lost: %+v", resp.Components)
	}
}
