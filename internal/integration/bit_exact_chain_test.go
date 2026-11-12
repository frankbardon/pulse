package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// bit_exact_chain_test.go: a ProcessChain whose stages carry total()
// (U74 E3-S3, #494) — grouped and multi-layer — answers the serial
// chain's bits under every ShardWorkers / DecodeWorkers count. Stage 0
// runs Process, which fans out two-phase (the per-shard arm over the
// nine-shard archive, the parallel-decode arm over the 120,000-row file
// in the heavy tier); every later stage runs the two-phase drive over
// the previous stage's rows on the streaming arm, never buffering them.

// bitExactTotalChain is the three-stage chain over cohort: stage 0 a
// filtered, grouped share (and a frequency-weighted one), stage 1 three
// pre-pass layers grouped by the forwarded region key (a frequency
// weight on the forwarded count), stage 2 an ungrouped share.
func bitExactTotalChain(cohort string) *types.ChainRequest {
	f := func(label, expr string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: label}
	}
	wsh := f("wsh", "(x ?? 0) * 1000 / total(x)")
	wsh.Weight = types.SlotWeightOf(types.WeightSpec{Field: "fw", Kind: types.WeightKindFrequency})
	t1 := f("t1", "d == nil ? nil : d / total(d)")
	t1.Weight = types.SlotWeightOf(types.WeightSpec{Field: "n", Kind: types.WeightKindFrequency})
	return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{
		{Name: "share", Request: &types.Request{
			Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "y > 5"}},
			Attributes: []*types.Attribute{f("sh", "x == nil ? nil : x / total(x)"), wsh},
			Groups:     []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_ROUNDED, Field: "y", Interval: 2.5}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "sh", Label: "s"},
				{Type: types.AGG_SUM, Field: "wsh", Label: "ws"},
				{Type: types.AGG_COUNT, Field: "sh", Label: "n"},
			},
		}},
		{Name: "layers", Request: &types.Request{
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "n > 0"}},
			Attributes: []*types.Attribute{
				f("t0", "s == nil ? nil : s / total(s)"),
				f("d", "t0 == nil ? nil : t0 * 1000 - n / 7"),
				t1,
				f("t2", "(t1 ?? 0) * 1000 + total((t1 ?? 0) - ws / 11)"),
			},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "t0", Label: "s0"},
				{Type: types.AGG_SUM, Field: "t1", Label: "s1"},
				{Type: types.AGG_SUM, Field: "t2", Label: "s2"},
				{Type: types.AGG_AVERAGE, Field: "t2", Label: "a2"},
			},
		}},
		{Name: "final", Request: &types.Request{
			Attributes: []*types.Attribute{f("f", "s2 / total(s2)")},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "f", Label: "sf"},
				{Type: types.AGG_MAX, Field: "f", Label: "mf"},
			},
		}},
	}}
}

// chainCell is one run's knobs and the arm each stage must report.
type chainCell struct {
	layout   invariance.Layout
	dw, sw   int
	stage0   observe.Arm // "" = any (the reference)
	laterArm observe.Arm
}

// runBitExactChain runs the chain under c and returns each stage's
// layout-free response (JSON) and the arm each stage child reported.
func runBitExactChain(t *testing.T, co invCohort, c chainCell) ([]string, map[int]observe.Arm) {
	t.Helper()
	var mu sync.Mutex
	arms := map[int]observe.Arm{}
	p, err := pulse.New(pulse.Options{FS: co.fs, ShardWorkers: c.sw, DecodeWorkers: c.dw, Hooks: &observe.Hooks{
		OnOperationEnd: func(_ context.Context, info observe.OperationInfo, r observe.OperationResult) {
			if info.Scope != observe.ScopeChild {
				return
			}
			mu.Lock()
			arms[info.Index] = r.Arm
			mu.Unlock()
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.ProcessChain(context.Background(), bitExactTotalChain(co.path))
	if err != nil {
		t.Fatalf("%s dw=%d sw=%d: ProcessChain: %v", c.layout, c.dw, c.sw, err)
	}
	out := make([]string, len(resp.Stages))
	for i, st := range resp.Stages {
		b, err := json.Marshal(invariance.LayoutFree(st))
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(b)
	}
	if len(resp.Stages[1].Data) != 5 || len(resp.Stages[0].Data) <= 10 {
		t.Fatalf("stage rows %d / %d: the chain does not split", len(resp.Stages[0].Data), len(resp.Stages[1].Data))
	}
	return out, arms
}

// checkBitExactChain runs ref and every cell, requiring each cell's
// stages to match ref's bits and to report their arms.
func checkBitExactChain(t *testing.T, cohorts map[invariance.Layout]invCohort, ref chainCell, cells []chainCell) {
	t.Helper()
	want, _ := runBitExactChain(t, cohorts[ref.layout], ref)
	for _, c := range cells {
		t.Run(fmt.Sprintf("%s/dw=%d/sw=%d", c.layout, c.dw, c.sw), func(t *testing.T) {
			got, arms := runBitExactChain(t, cohorts[c.layout], c)
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("stage %d differs from the serial chain:\n got %s\nwant %s", i, got[i], want[i])
				}
			}
			if arms[0] != c.stage0 {
				t.Errorf("stage 0 arm = %q, want %q", arms[0], c.stage0)
			}
			for i := 1; i < len(want); i++ {
				if arms[i] != c.laterArm {
					t.Errorf("stage %d arm = %q, want %q (the two-phase drive over the rows)", i, arms[i], c.laterArm)
				}
			}
		})
	}
}

// TestBitExact_ChainTotals: over the nine-shard scattered archive, the
// total() chain under ShardWorkers 2/3/8 (stage 0 on the per-shard
// arm) and the serial archive scan equal the single file's serial
// chain, stage by stage, bitwise.
func TestBitExact_ChainTotals(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t, bitInvSmokeRows, true)
	fsys := afero.NewBasePathFs(afero.NewOsFs(), dir)
	cohorts := map[invariance.Layout]invCohort{invariance.File: {fsys, "flat.pulse"}, invariance.Archive: {fsys, "arch.pulse"}}
	ref := chainCell{layout: invariance.File, dw: 1, sw: 1}
	cells := []chainCell{{layout: invariance.Archive, dw: 1, sw: 1, stage0: observe.ArmBuffered, laterArm: observe.ArmStreaming}}
	for _, sw := range invariance.ArchiveShardWorkers {
		cells = append(cells, chainCell{layout: invariance.Archive, dw: 1, sw: sw, stage0: observe.ArmShardParallel, laterArm: observe.ArmStreaming})
	}
	checkBitExactChain(t, cohorts, ref, cells)
}
