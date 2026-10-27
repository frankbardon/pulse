package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// singleGrouperStreamAllocCeiling is the single-grouper streaming
// grouped run's allocations per Process call over the fixture below,
// measured on the pre-U54 path (313). The multi-grouper composite arm
// must put nothing on the one-grouper fast path. U51 E2-S4 re-based it
// to 317: a bucket's aggregators and floors now ride one *GroupBucket
// map entry (one more allocation per bucket, four buckets here) in
// place of a second per-key floor table — fewer bytes a group, one
// allocation more.
const singleGrouperStreamAllocCeiling = 317

// TestStreamingGrouped_SingleGrouperAllocCeiling: the one-grouper
// streaming arm allocates no more than it did before composite keys —
// no per-record composite work, no extra per-run state.
func TestStreamingGrouped_SingleGrouperAllocCeiling(t *testing.T) {
	// serial: testing.AllocsPerRun counts process-wide allocations, which concurrent tests inflate

	recs := make([]*Record, 0, 200)
	for i := 0; i < 200; i++ {
		recs = append(recs, NewRecord(nil, map[string]float64{"a": float64(i % 7), "b": float64(i)}))
	}
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "a", Interval: 2}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "b", Label: "s"}},
	}
	p := NewProcessor(nil)
	resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	if p.LastPath() != PathStreaming {
		t.Fatalf("LastPath = %v, want PathStreaming", p.LastPath())
	}
	if len(resp.Data) != 4 {
		t.Fatalf("%d rows, want 4", len(resp.Data))
	}
	if raceEnabled {
		t.Skip("the race runtime adds its own allocations; the ceiling is a plain-build figure")
	}
	got := testing.AllocsPerRun(50, func() {
		_, _ = p.Process(context.Background(), req, NewSliceIterator(recs))
	})
	if got > singleGrouperStreamAllocCeiling {
		t.Fatalf("single-grouper streaming run allocated %v per call, ceiling %d", got, singleGrouperStreamAllocCeiling)
	}
}
