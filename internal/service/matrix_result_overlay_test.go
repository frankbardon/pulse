package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestMatrixResultOverlays_RunAfterMultiplicityFold pins the decided
// apply order of the MATRIX_RESULT overlay pass (U28 E4-S1): it runs
// AFTER the multiplicity fold, so it reads every grouped result with its
// auxiliary.p_adjusted and Multiplicity echo already written — on a
// process request (after foldRequestMultiplicity) and on a Compose
// slot's own overlays, serial and parallel (deferred past the barrier's
// foldComposeMultiplicity). The pass is observed through its seam; each
// layer lands at its spec's index.
func TestMatrixResultOverlays_RunAfterMultiplicityFold(t *testing.T) {
	// serial: swaps the package-level service.applyMatrixResultOverlays test hook

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(160))
	req := func() *types.Request {
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", "")
		spec.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodBH}
		return &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "f", Interval: 1}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "f", Label: "n"}},
			Matrices:     []types.MatrixSpec{spec},
			Overlays: []types.OverlaySpec{
				{Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup},
				{Kind: types.OverlayKindMatrixGroupSummary, Scope: types.OverlayScopeMatrix,
					Ref: types.OverlayRef{Matrix: &types.OverlayMatrixRef{Name: "m"}}},
			}}
	}
	var seen []bool
	orig := applyMatrixResultOverlays
	t.Cleanup(func() { applyMatrixResultOverlays = orig })
	applyMatrixResultOverlays = func(req *types.Request, resp *types.Response, exts *processing.ExtensionRegistry, compute processing.ComputePlan) error {
		folded := len(resp.Matrices) > 1
		for _, m := range resp.Matrices {
			folded = folded && m.Auxiliary["p_adjusted"] != nil && m.Multiplicity != nil
		}
		seen = append(seen, folded)
		return orig(req, resp, exts, compute)
	}
	check := func(t *testing.T, name string, resp *types.Response) {
		t.Helper()
		if len(resp.Overlays) != 2 || resp.Overlays[0].Kind != types.OverlayKindShareOfTotal ||
			resp.Overlays[1].Kind != types.OverlayKindMatrixGroupSummary || resp.Overlays[1].Payload.Matrix == nil {
			t.Fatalf("%s: overlays %+v", name, resp.Overlays)
		}
	}
	svc := New(cfg)
	resp, err := svc.Process(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	check(t, "process", resp)
	composed := func() *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{req(), req()}}
	}
	out, err := svc.Compose(context.Background(), composed())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Responses {
		check(t, "compose", r)
	}
	out, err = svc.ComposeParallel(context.Background(), composed(), ComposeOptions{MaxWorkers: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Responses {
		check(t, "compose parallel", r)
	}
	if len(seen) != 5 {
		t.Fatalf("the pass ran %d times, want 5 (process + 2 x 2 Compose slots)", len(seen))
	}
	for i, ok := range seen {
		if !ok {
			t.Errorf("run %d: the pass read the matrices before the multiplicity fold wrote p_adjusted", i)
		}
	}
}
