package pulse_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// E2-S2 (PRD FR-4 / FR-11): OnPhase fires once per phase that ran, in
// execution order, after the work and before OnOperationEnd; a phase
// that did not run is never reported.

// TestObservabilityPhases pins the exact ordered phase list of every
// execution arm, plus `shape` when a `return` block shapes the
// response.
//
// Falsified per phase by deleting its Lap (the phase disappears from
// its arm's list), e.g. the PhaseReduce lap in shard_reduce.go.
func TestObservabilityPhases(t *testing.T) {
	ctx := context.Background()
	want := map[string][]string{
		"streaming":         {"plan", "open", "scan"},
		"buffered":          {"plan", "open", "decode", "scan", "post"},
		"fused_crosstab":    {"plan", "open", "scan"},
		"crosstab_buffered": {"plan", "open", "decode", "scan"},
		"crosstab_join":     {"plan", "open", "decode", "scan"},
		"join":              {"plan", "open", "scan"},
		"shard_parallel":    {"plan", "open", "scan", "reduce"},
		"parallel_decode":   {"plan", "open", "scan", "reduce"},
	}
	cases := execArmCases(t)
	if len(cases) != len(want) {
		t.Fatalf("%d arm cases, %d phase expectations — keep them in step", len(cases), len(want))
	}
	for _, c := range cases {
		for _, shaped := range []bool{false, true} {
			name := c.name
			if shaped {
				name += "/return"
			}
			t.Run(name, func(t *testing.T) {
				rec := &execRecorder{}
				p, req := c.open(t, rec)
				expect := append([]string(nil), want[c.name]...)
				if shaped {
					req.Return = &types.Return{Preset: types.ReturnPresetStandard}
					expect = append(expect, "shape")
				}
				if _, err := p.Process(ctx, req); err != nil {
					t.Fatalf("Process: %v", err)
				}
				got := rec.takeLog()
				if !reflect.DeepEqual(got, append(expect, "end")) {
					t.Errorf("phase log = %v, want %v then end", got, expect)
				}
			})
		}
	}
}

// TestObservabilityPhaseTimings: every reported phase carries a
// non-negative duration, the phases together never exceed the
// operation, and an operation that runs no engine phase (inspect)
// reports none.
func TestObservabilityPhaseTimings(t *testing.T) {
	ctx := context.Background()
	var phases []observe.PhaseTiming
	var total []observe.OperationResult
	hooks := &observe.Hooks{
		OnPhase: func(_ context.Context, _ observe.OperationInfo, ph observe.PhaseTiming) {
			phases = append(phases, ph)
		},
		OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, res observe.OperationResult) {
			total = append(total, res)
		},
	}
	p, err := pulse.New(pulse.Options{FS: execSmallFS(t), Hooks: hooks})
	if err != nil {
		t.Fatal(err)
	}
	req := execSumReq(execCohort)
	// ATTR_PERCENTILE forces the buffered arm: every buffered phase runs.
	req.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"}}
	if _, err := p.Process(ctx, req); err != nil {
		t.Fatal(err)
	}
	if len(phases) == 0 || len(total) != 1 {
		t.Fatalf("phases=%d ends=%d", len(phases), len(total))
	}
	var sum int64
	for _, ph := range phases {
		if ph.Duration < 0 {
			t.Errorf("phase %s duration %v < 0", ph.Phase, ph.Duration)
		}
		sum += int64(ph.Duration)
	}
	if sum > int64(total[0].Duration) {
		t.Errorf("phases sum %d ns > operation %d ns", sum, total[0].Duration)
	}

	phases, total = nil, nil
	if _, err := p.Inspect(ctx, execCohort); err != nil {
		t.Fatal(err)
	}
	if len(phases) != 0 || len(total) != 1 {
		t.Errorf("inspect: phases=%v ends=%d, want no phase and one end", phases, len(total))
	}
}
