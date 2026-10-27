//go:build heavy

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The DecodeWorkers arm shares shardPartial / foldGroupedRow /
// finalizeMergedPartial with the shard reducer, so it had the same
// grouped holes (no groupers block, Sort and include order dropped,
// GROUP_SET_PER_ELEMENT refused). Same whole-document parity, single
// file above the parallel-decode threshold.
func TestDecodeWorkers_GroupedAnswersParity(t *testing.T) {
	const rowCount = parallelDecodeRecordThreshold + 4096
	schema := componentsParitySchema()
	recs, nullAt := componentsParityRows(rowCount, 0)
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/grouped_parity.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	aggs := []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		{Type: types.AGG_SUM, Field: "score", Label: "sum"},
	}
	reqs := map[string]*types.Request{
		"range_sorted": {Cohort: &types.Cohort{Filename: path}, Aggregations: aggs,
			Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 20000}},
			Sort:   []types.OrderKey{{Field: "n", Desc: true}}},
		"set_value":       {Cohort: &types.Cohort{Filename: path}, Aggregations: aggs, Groups: []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "picks"}}},
		"set_per_element": {Cohort: &types.Cohort{Filename: path}, Aggregations: aggs, Groups: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "picks"}}},
		// A tally attribute's per-worker counts fold (U51 E3-S2).
		"scale_score_set_value": {Cohort: &types.Cohort{Filename: path},
			Attributes: []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Fields: []string{"score", "id"}, Label: "sc",
				Params: json.RawMessage(`{"scale_min":0,"scale_max":90000}`)}},
			Aggregations: append(append([]*types.Aggregation(nil), aggs...), &types.Aggregation{Type: types.AGG_SUM, Field: "sc", Label: "sum_sc"}),
			Groups:       []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "picks"}}},
	}
	answer := func(t *testing.T, workers int, req *types.Request) string {
		t.Helper()
		svc := New(cfg)
		svc.SetDecodeWorkers(workers)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process(DecodeWorkers=%d): %v", workers, err)
		}
		if resp.Components == nil || len(resp.Components.Groupers) != 1 {
			t.Fatalf("DecodeWorkers=%d emitted no groupers block: %+v", workers, resp.Components)
		}
		if ac := resp.Components.Aggregations; len(ac) != len(req.Aggregations) || len(ac[0].Groups) != len(resp.Data) {
			t.Fatalf("DecodeWorkers=%d emitted no per-group aggregation components", workers)
		}
		if len(req.Attributes) > 0 {
			if ac := resp.Components.Attributes; len(ac) != 1 || ac[0].Operator["n_out_of_range"] == 0 || len(resp.Warnings) == 0 {
				t.Fatalf("DecodeWorkers=%d emitted a degenerate attribute tally %+v", workers, ac)
			}
		}
		b, _ := json.Marshal(struct {
			Data       any
			Components any
			Warnings   any
		}{resp.Data, resp.Components, resp.Warnings})
		return string(b)
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}
	for name, req := range reqs {
		t.Run(name, func(t *testing.T) {
			if !processing.CanMergeRequest(req, schema) {
				t.Fatal("request is not mergeable; the parallel decode reducer would never engage")
			}
			want := answer(t, 1, req)
			if got := answer(t, 4, req); got != want {
				t.Errorf("DecodeWorkers=4 answers differently from DecodeWorkers=1\n got  %.600s\n want %.600s", got, want)
			}
		})
	}
}
