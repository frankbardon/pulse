//go:build heavy

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// chainMemorySlack is the noise band on a peak-heap comparison (the
// minimum of three sampled passes each): a regression this catches is
// the materialised input — a multiple, not a few percent.
const chainMemorySlack = 1.10

// minPeakHeap is measurePeakHeap's minimum over three passes of fn.
func minPeakHeap(t *testing.T, fn func() error) float64 {
	t.Helper()
	peak := -1.0
	for range 3 {
		got, err := measurePeakHeap(fn)
		if err != nil {
			t.Fatal(err)
		}
		if peak < 0 || got < peak {
			peak = got
		}
	}
	return peak
}

// totalChain is a three-stage total() chain over the dictionary
// archive: stage 0 a share grouped by the tag (two-phase on the
// per-shard arm), stage 1 two pre-pass layers over the forwarded tag,
// stage 2 an ungrouped share.
func totalChain(path string) *types.ChainRequest {
	f := func(label, expr string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: label}
	}
	return &types.ChainRequest{Cohort: &types.Cohort{Filename: path}, Stages: []*types.ChainStage{
		{Request: &types.Request{
			Attributes:   []*types.Attribute{f("sh", "x0 / total(x0)")},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sh", Label: "s"}, {Type: types.AGG_SUM, Field: "x1", Label: "s1"}},
		}},
		{Request: &types.Request{
			Attributes:   []*types.Attribute{f("a", "s / total(s)"), f("b", "a - total(a * s1)")},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "b", Label: "sb"}},
		}},
		{Request: &types.Request{
			Attributes:   []*types.Attribute{f("c", "sb / total(sb)")},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "c", Label: "sc"}},
		}},
	}}
}

// TestChainTotals_PeakMemoryNotWorseThanSerial (U74 E3-S3, #494): the
// total() chain's peak heap on ShardWorkers 4 (stage 0 two-phase on the
// per-shard arm, later stages two-phase over the rows) is not above the
// serial chain's (ShardWorkers 1: stage 0 buffers every record, a
// grouped total() being two-pass beside groups), and the answers are
// the same bytes. The archive is retention-shaped: 400,000 rows over
// 2,000 tags. (A grouping whose key count nears the row count is the
// per-shard reducer's own trade — each worker holds a partial of every
// group — on Process as on a chain stage 0.)
func TestChainTotals_PeakMemoryNotWorseThanSerial(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "chain.pulse", 4, 100_000, 2_000)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	run := func(workers int) (float64, string) {
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		var out []byte
		peak := minPeakHeap(t, func() error {
			resp, err := svc.ProcessChain(context.Background(), totalChain("chain.pulse"))
			if err != nil {
				return err
			}
			out, err = json.Marshal(resp)
			return err
		})
		return peak, string(out)
	}
	serial, want := run(1)
	parallel, got := run(4)
	t.Logf("peak heap: serial %.1f MB, ShardWorkers 4 %.1f MB", serial/(1<<20), parallel/(1<<20))
	if got != want {
		t.Fatal("ShardWorkers 4 chain answers differ from the serial chain")
	}
	if parallel > serial*chainMemorySlack {
		t.Errorf("parallel chain peak %.1f MB above the serial chain's %.1f MB", parallel/(1<<20), serial/(1<<20))
	}
}

// TestChainStageTotals_PeakMemoryNotWorseThanSerial: a later stage's
// two-phase drive over 300,000 previous-stage rows (grouped, two
// layers) peaks no higher than the serial processor over the same rows,
// which buffers a record a row.
func TestChainStageTotals_PeakMemoryNotWorseThanSerial(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	cfg, err := fs.New(fs.WithFs(afero.NewMemMapFs()))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	prior, in, data := chainTotalsPrior(300_000)
	stage := chainTotalsStages()["multilayer_grouped"]
	ctx := context.Background()
	twoPhase := minPeakHeap(t, func() error {
		schema, rows := chainTotalsRows(t, svc, prior, in, data)
		_, err := svc.runChainStageTotals(ctx, stage, nil, schema, rows)
		return err
	})
	serial := minPeakHeap(t, func() error {
		schema, rows := chainTotalsRows(t, svc, prior, in, data)
		_, err := svc.runChainStage(ctx, stage, nil, schema, rows)
		return err
	})
	t.Logf("peak heap: serial processor %.1f MB, two-phase %.1f MB", serial/(1<<20), twoPhase/(1<<20))
	if twoPhase > serial*chainMemorySlack {
		t.Errorf("two-phase stage peak %.1f MB above the serial processor's %.1f MB", twoPhase/(1<<20), serial/(1<<20))
	}
}
