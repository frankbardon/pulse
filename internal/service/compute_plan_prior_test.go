package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestResolveComputePlan_PriorKindsKeepGroupComponents (U47 #328): an
// inferential prior-period kind over a grouped Process reads each
// period's leg off the scored aggregation's per-group components, so a
// `return` that excludes them still COMPUTES the aggregation components
// and their groups; the crosstab form keeps components.crosstab; a
// payload-only series kind vetoes nothing.
func TestResolveComputePlan_PriorKindsKeepGroupComponents(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	ctx := context.Background()
	ret := mustResolveReturn(t, &types.Request{Return: &types.Return{Preset: types.ReturnPresetMinimal}})
	minimal := processing.ComputePlanFor(nil).WithoutComponents()
	minimal.MatrixAuxiliary, minimal.MatrixScalars, minimal.MatrixVectors = false, false, false
	series := func(kind types.OverlayKind) *types.Request {
		return &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "k"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_WELFORD, Field: "x"}},
			Overlays:     []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeGroup}},
		}
	}
	if got := svc.resolveComputePlan(ctx, series(types.OverlayKindDeltaVsPrior), ret, nil); got != minimal {
		t.Errorf("payload-only series kind: %+v; want no veto", got)
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsPrior, types.OverlayKindZVsPrior, types.OverlayKindPropZVsPrior} {
		want := minimal
		want.Aggs, want.Groups = true, true
		if got := svc.resolveComputePlan(ctx, series(kind), ret, nil); got != want {
			t.Errorf("%s over a series host: %+v; want components.aggregations (+ groups) kept", kind, got)
		}
		xt := &types.Request{
			Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "k"}},
				Cell: &types.Aggregation{Type: types.AGG_WELFORD, Field: "x"},
			},
			Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeGroup}},
		}
		want = minimal
		want.Crosstab = true
		if got := svc.resolveComputePlan(ctx, xt, ret, nil); got != want {
			t.Errorf("%s over a no-column crosstab: %+v; want components.crosstab kept", kind, got)
		}
	}
}
