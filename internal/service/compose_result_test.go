package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// compose_result_test.go pins the result pre-phase's schedule
// (compose_result.go): every slot an optimize block's result() reads
// runs once, after the fit phases and before the block; the other
// slots run after it; a sweep slot phase 0 already ran is not run
// again — serial and parallel alike.

// resultSchedBatch is sweepBindBatch with each sweep slot also counting
// its rows (n), a slot "after" no block reads, and one optimize block
// whose bounds read the explicit slot "scored" and the sweep slot
// "upto_40": maximise x - y over x ∈ [0, n(scored) / 8] and
// y ∈ [n(upto_40) / 41, 30], so x lands on 104 / 8 = 13 and y on
// 41 / 41 = 1.
func resultSchedBatch() *types.ComposedRequest {
	c := sweepBindBatch()
	c.Sweep.Request = json.RawMessage(strings.Replace(string(c.Sweep.Request), `"regressions":`,
		`"aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}],"regressions":`, 1))
	c.Requests = append(c.Requests, &types.Request{Label: "after", Cohort: &types.Cohort{Filename: fitsCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}})
	c.Optimize = []types.OptimizeSpec{{
		Name: "fit_x", Direction: types.OptimizeMaximize,
		Variables: []types.OptimizeVariable{
			{Name: "x", Lower: types.BoundValue(0), Upper: types.BoundExpr(`result("scored", "n") / 8`)},
			{Name: "y", Lower: types.BoundExpr(`result("upto_40", "n") / 41`), Upper: types.BoundValue(30)},
		},
		Objective: "x - y",
	}}
	return c
}

// TestComposeResult_PrePhaseSchedule: "scored" (read by the block) runs
// before "after" (read by none) and exactly once; every sweep slot runs
// once (upto_40, read by the block, in phase 0 only); responses keep
// index order; the bounds read the slots' counts (x on 13, y on 1).
func TestComposeResult_PrePhaseSchedule(t *testing.T) {
	// serial: swaps the package-level service.fitRunObserver test hook; swaps the package-level service.slotModelsObserver test hook

	svc := fitsService(t)
	for _, mode := range []string{"serial", "parallel"} {
		t.Run(mode, func(t *testing.T) {
			o := observeBind(t)
			var out *types.ComposedResponse
			var err error
			if mode == "serial" {
				out, err = svc.Compose(context.Background(), resultSchedBatch())
			} else {
				out, err = svc.ComposeParallel(context.Background(), resultSchedBatch(), ComposeOptions{MaxWorkers: 4, FailFast: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			labels := []string{}
			for _, r := range out.Responses {
				labels = append(labels, r.Label)
			}
			if want := []string{"scored", "after", "upto_80", "upto_40", "upto_60"}; !reflect.DeepEqual(labels, want) {
				t.Fatalf("responses %v, want %v", labels, want)
			}
			for _, e := range []string{"slot:scored", "slot:after", "slot:upto_80", "slot:upto_40", "slot:upto_60", "fit:best"} {
				if o.count(e) != 1 {
					t.Fatalf("%s ran %d times: %v", e, o.count(e), o.events)
				}
			}
			if scored, after, best := o.at("slot:scored"), o.at("slot:after"), o.at("fit:best"); scored < best || after < scored {
				t.Fatalf("pre-phase order: %v", o.events)
			}
			if len(out.Optimizations) != 1 || out.Optimizations[0].Error != nil {
				t.Fatalf("optimizations %+v", out.Optimizations)
			}
			x, y := out.Optimizations[0].Variables[0], out.Optimizations[0].Variables[1]
			if x.Value != 13 || y.Value != 1 {
				t.Fatalf("x = %+v, y = %+v; want 13 and 1", x, y)
			}
		})
	}
}
