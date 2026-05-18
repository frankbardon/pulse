package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestShardParity_SingleFileVsArchiveVsAnchor is the cross-shape
// regression harness. For a representative set of request shapes —
// online aggregation, grouped aggregation, filtered aggregation — it
// builds the SAME data three ways:
//
//  1. Single-file `.pulse` (concatenated records)
//  2. Multi-shard archive (the same records split across N shards)
//  3. Anchor-resolved single-shard view inside a one-shard archive
//
// and asserts the Process response is byte-equal across all three
// shapes. The point is to catch silent divergence in the iterator,
// reducer, or anchor overlay when a future change touches one path
// but not the others.
//
// Welford-merged aggregators (AGG_AVERAGE) are compared with a ULP
// tolerance because the parallel merge introduces float drift within
// the spec.
func TestShardParity_SingleFileVsArchiveVsAnchor(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	// Shape 1: single-file cohort containing the concatenated records.
	if err := afero.WriteFile(fsys, "single.pulse",
		writeSinglePulse(t, schema, concat), 0o644); err != nil {
		t.Fatalf("WriteFile single: %v", err)
	}

	// Shape 2: multi-shard archive with the same records split 4/4/4.
	multiArchive := buildShardArchive(t, schema, shards)
	if err := afero.WriteFile(fsys, "multi.pulse", multiArchive, 0o644); err != nil {
		t.Fatalf("WriteFile multi: %v", err)
	}

	// Shape 3: a one-shard archive containing all 12 records, opened
	// via anchor syntax. This exercises the anchor overlay against an
	// archive whose only shard carries the full dataset.
	soloShards := []struct {
		Name    string
		Records [][]uint64
	}{{Name: "only.pulse", Records: concat}}
	soloArchive := buildShardArchive(t, schema, soloShards)
	if err := afero.WriteFile(fsys, "solo.pulse", soloArchive, 0o644); err != nil {
		t.Fatalf("WriteFile solo: %v", err)
	}

	cases := []struct {
		name string
		req  func(filename string) *types.Request
	}{
		{
			name: "online_aggregation",
			req: func(f string) *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: f},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "score", Label: "n"},
						{Type: types.AGG_SUM, Field: "score", Label: "total"},
						{Type: types.AGG_MIN, Field: "score", Label: "lo"},
						{Type: types.AGG_MAX, Field: "score", Label: "hi"},
						{Type: types.AGG_AVERAGE, Field: "score", Label: "avg"},
					},
				}
			},
		},
		{
			name: "grouped_aggregation",
			req: func(f string) *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: f},
					Groups: []*types.Group{
						{Type: types.GROUP_RANGE, Field: "score", Interval: 50.0},
					},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "score", Label: "n"},
						{Type: types.AGG_SUM, Field: "score", Label: "total"},
					},
				}
			},
		},
		{
			name: "filtered_aggregation",
			req: func(f string) *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: f},
					Filterers: []*types.Filterer{
						{Type: types.FILTER_RANGE, Field: "score", Values: []string{"30", "90"}},
					},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "score", Label: "n"},
						{Type: types.AGG_SUM, Field: "score", Label: "total"},
					},
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			single, err := svc.Process(ctx, tc.req("single.pulse"))
			if err != nil {
				t.Fatalf("single-file Process: %v", err)
			}
			multi, err := svc.Process(ctx, tc.req("multi.pulse"))
			if err != nil {
				t.Fatalf("multi-shard Process: %v", err)
			}
			anchor, err := svc.Process(ctx, tc.req("solo.pulse#only.pulse"))
			if err != nil {
				t.Fatalf("anchor Process: %v", err)
			}

			assertResponseParity(t, "single↔multi", single, multi)
			assertResponseParity(t, "single↔anchor", single, anchor)
		})
	}
}

// assertResponseParity compares two responses row-by-row in index
// order. Grouped output is sorted by group key at emit time in
// processing/processor.go, so row order is deterministic across
// runs, codepaths, and worker counts. Float cells use a ULP
// tolerance (Welford-merged means drift within a few ULP across the
// parallel reduce path; associative integer ops are byte-equal).
func assertResponseParity(t *testing.T, label string, a, b *types.Response) {
	t.Helper()
	if len(a.Data) != len(b.Data) {
		t.Fatalf("%s: row count diverges: %d vs %d", label, len(a.Data), len(b.Data))
	}
	for i := range a.Data {
		left, right := a.Data[i], b.Data[i]
		if len(left) != len(right) {
			t.Fatalf("%s row %d: key count diverges: %d vs %d", label, i, len(left), len(right))
		}
		for k, lv := range left {
			rv, ok := right[k]
			if !ok {
				t.Errorf("%s row %d: missing key %q on right", label, i, k)
				continue
			}
			if !cellEqual(lv, rv) {
				t.Errorf("%s row %d key %q: %v != %v", label, i, k, lv, rv)
			}
		}
	}
}

// cellEqual treats two floats as equal when their absolute difference
// is within 4 ULPs scaled to magnitude, mirroring the tolerance used
// by the existing TestShardArchiveProcessSums Welford-mean check. All
// other types are compared with ==.
func cellEqual(a, b any) bool {
	af, aok := a.(float64)
	bf, bok := b.(float64)
	if aok && bok {
		if af == bf {
			return true
		}
		if math.IsNaN(af) && math.IsNaN(bf) {
			return true
		}
		scale := math.Max(math.Abs(af), math.Abs(bf))
		if scale == 0 {
			return math.Abs(af-bf) < 1e-12
		}
		return math.Abs(af-bf)/scale < 4*1e-15
	}
	return a == b
}
