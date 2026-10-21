package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
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
// Every aggregator here (COUNT, MIN, MAX and the binned reproducible
// AGG_SUM / AGG_AVERAGE) is order-free, so the three shapes are
// compared bit for bit.
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

// assertResponseParity compares two responses' Data bit for bit.
// Grouped output is sorted by group key at emit time in
// internal/processing/processor.go, so row order is deterministic across
// runs, codepaths, and worker counts; every cell the cases emit is an
// order-free reduction (integer counts, sentinel scans, binned sums).
func assertResponseParity(t *testing.T, label string, a, b *types.Response) {
	t.Helper()
	if !reflect.DeepEqual(a.Data, b.Data) {
		t.Errorf("%s: Data diverges:\n %v\n %v", label, a.Data, b.Data)
	}
}
