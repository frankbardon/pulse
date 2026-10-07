package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestReturnSkipsComputation_Matrices extends TestReturnSkipsComputation
// to Request.Matrices (U18 rank 4): an excluded `matrices` slot is never
// ACCUMULATED — no slot is minted, so no record is folded — unless
// components.matrices still needs the co-moment state; an excluded
// matrices[*].auxiliary / .scalars / .vectors sub-part is never rendered
// at finalize. `standard` drops auxiliary, `minimal` also scalars and
// vectors. Every arm that folds matrices runs: serial streaming and
// buffered (ungrouped and grouped), parallel decode and shard reduce.
// The `absent` run proves each zero non-vacuous, and a kept sub-part
// equals the `absent` run's.

// skipMatrixRequest is skipRequest plus one pairwise MAT_CORRELATION
// over id x score with top_pairs, so every finalize sub-part
// (auxiliary.n, scalars.determinant, vectors.top_pairs) is rendered.
func skipMatrixRequest(path string, grouped, buffered bool) *types.Request {
	req := skipRequest(path, grouped, buffered)
	req.Matrices = []types.MatrixSpec{{
		Name:   "m",
		Type:   types.MAT_CORRELATION,
		Fields: []string{"id", "score"},
		Params: json.RawMessage(`{"missing": "pairwise", "summary": {"top_pairs": 1}}`),
	}}
	return req
}

var matrixSkipCounters = []skipCounter{
	{name: "accumulate", get: func(s processing.WorkStatsSnapshot) int64 { return s.MatrixAccumulators }},
	{name: "results", get: func(s processing.WorkStatsSnapshot) int64 { return s.MatrixResultBuilds }},
	{name: "auxiliary", get: func(s processing.WorkStatsSnapshot) int64 { return s.MatrixAuxiliaryBuilds }},
	{name: "scalars", get: func(s processing.WorkStatsSnapshot) int64 { return s.MatrixScalarBuilds }},
	{name: "vectors", get: func(s processing.WorkStatsSnapshot) int64 { return s.MatrixVectorBuilds }},
	{name: "components", get: func(s processing.WorkStatsSnapshot) int64 { return s.MatrixComponentBuilds }},
}

func matrixSkipSelections() []skipSelection {
	ex := func(paths ...string) *types.Return { return &types.Return{Exclude: paths} }
	slot := map[string]bool{"results": true, "auxiliary": true, "scalars": true, "vectors": true}
	return []skipSelection{
		{name: "absent"},
		{name: "keeps_matrices", ret: ex("metadata")},
		{name: "standard", ret: &types.Return{Preset: types.ReturnPresetStandard},
			zero: map[string]bool{"auxiliary": true, "components": true}},
		{name: "minimal", ret: &types.Return{Preset: types.ReturnPresetMinimal},
			zero: map[string]bool{"auxiliary": true, "scalars": true, "vectors": true, "components": true}},
		// components.matrices still needs the fold: the slot is not
		// rendered, but the accumulators run.
		{name: "exclude_matrices", ret: ex("matrices"), zero: slot},
		{name: "exclude_matrices_and_components", ret: ex("matrices", "components.matrices"),
			zero: map[string]bool{"accumulate": true, "results": true, "auxiliary": true, "scalars": true, "vectors": true, "components": true}},
		{name: "exclude_auxiliary", ret: ex("matrices[*].auxiliary"), zero: map[string]bool{"auxiliary": true}},
		{name: "exclude_scalars", ret: ex("matrices[*].scalars"), zero: map[string]bool{"scalars": true}},
		{name: "exclude_vectors", ret: ex("matrices[*].vectors"), zero: map[string]bool{"vectors": true}},
	}
}

func TestReturnSkipsComputation_Matrices(t *testing.T) {
	for _, arm := range skipArms() {
		t.Run(arm.name, func(t *testing.T) {
			if arm.long && testing.Short() {
				t.Skip("parallel decode needs a cohort above the decode threshold")
			}
			svc, path := arm.build(t)
			for _, grouped := range []bool{false, true} {
				for _, buffered := range []bool{false, true} {
					if buffered && arm.mergeable {
						continue // AGG_MEDIAN is not mergeable: the arm would not engage
					}
					shape := fmt.Sprintf("grouped=%v/buffered=%v", grouped, buffered)
					var base *types.Response // the `absent` run
					for _, sel := range matrixSkipSelections() {
						t.Run(shape+"/"+sel.name, func(t *testing.T) {
							req := skipMatrixRequest(path, grouped, buffered)
							req.Return = sel.ret
							before := processing.WorkStats()
							resp, err := svc.Process(context.Background(), req)
							if err != nil {
								t.Fatalf("Process: %v", err)
							}
							delta := processing.WorkStats().Sub(before)
							for _, c := range matrixSkipCounters {
								got := c.get(delta)
								if sel.zero[c.name] && got != 0 {
									t.Errorf("%s: excluded, yet built %d time(s)", c.name, got)
								}
								if !sel.zero[c.name] && got <= 0 {
									t.Errorf("%s: kept, yet never built (delta %d) — the zero assertions would be vacuous", c.name, got)
								}
							}
							if sel.ret == nil {
								if len(resp.Matrices) == 0 {
									t.Fatal("the return-absent run carried no matrices: the fixture proves nothing")
								}
								base = resp
								return
							}
							assertKeptMatricesEqual(t, base, resp, sel.zero, sel.name == "exclude_matrices" || sel.name == "exclude_matrices_and_components")
						})
					}
				}
			}
		})
	}
}

// assertKeptMatricesEqual: a skipped matrix part is absent (nil in Go)
// and every kept one equals the `return`-absent run's.
func assertKeptMatricesEqual(t *testing.T, base, got *types.Response, zero map[string]bool, slotSkipped bool) {
	t.Helper()
	if slotSkipped {
		if got.Matrices != nil {
			t.Errorf("matrices: slot skipped, yet present: %+v", got.Matrices)
		}
	} else {
		want := make([]types.MatrixResult, len(base.Matrices))
		for i, m := range base.Matrices {
			if zero["auxiliary"] {
				m.Auxiliary = nil
			}
			if zero["scalars"] {
				m.Scalars = nil
			}
			if zero["vectors"] {
				m.Vectors = nil
			}
			want[i] = m
		}
		if !reflect.DeepEqual(want, got.Matrices) {
			t.Errorf("matrices: kept figures moved\n  got:  %+v\n  want: %+v", got.Matrices, want)
		}
	}
	var have []types.MatrixComponents
	if got.Components != nil {
		have = got.Components.Matrices
	}
	if zero["components"] {
		if have != nil {
			t.Errorf("components.matrices: skipped, yet present: %+v", have)
		}
		return
	}
	if !reflect.DeepEqual(base.Components.Matrices, have) {
		t.Errorf("components.matrices: kept figures moved\n  got:  %+v\n  want: %+v", have, base.Components.Matrices)
	}
}
