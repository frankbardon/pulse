package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_SeriesKindsMultiGrouperRefused: every SERIES
// overlay that reads the host as one ordered series (lag-1 prior,
// rolling window, positional baseline, first-match sibling) is refused
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with details.groupers when
// the request carries two or more groupers — the host is then the U54
// composite rows — and stays accepted over one grouper. Runtime twin:
// processing.TestOverlaySeries_CompositeHostRefused.
func TestValidateOverlay_SeriesKindsMultiGrouperRefused(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)
	window := json.RawMessage(`{"window": 2}`)
	specs := []types.OverlaySpec{
		{Name: "dp", Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup},
		{Name: "ip", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Prior: &types.OverlayPriorRef{}}},
		{Name: "irm", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "ib", Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "db", Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "is", Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"}}},
		{Name: "ds", Kind: types.OverlayKindDeltaVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"}}},
	}
	region := func() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "region"} }
	segment := func() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "segment"} }
	for _, spec := range specs {
		t.Run(string(spec.Kind), func(t *testing.T) {
			for _, c := range []struct {
				name   string
				groups []*types.Group
			}{
				{"one grouper", []*types.Group{region()}},
				{"outer region", []*types.Group{region(), segment()}},
				{"inner region", []*types.Group{segment(), region()}},
			} {
				req := &types.Request{
					Groups:       c.groups,
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
					Overlays:     []types.OverlaySpec{spec},
				}
				env := predictFromBytes(data, req, nil)
				var refusal *map[string]any
				for _, e := range env.Errors {
					if e.Code == string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
						d := e.Details
						refusal = &d
					}
				}
				if len(c.groups) == 1 {
					if len(env.Errors) > 0 {
						t.Errorf("%s: predict errors %+v, want accepted", c.name, env.Errors)
					}
					continue
				}
				if refusal == nil {
					t.Fatalf("%s: no PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: %+v", c.name, env.Errors)
				}
				if d := *refusal; d["groupers"] != 2 || d["kind"] != string(spec.Kind) || d["index"] != 0 {
					t.Errorf("%s: details %v, want index 0, kind, groupers 2", c.name, d)
				}
			}
		})
	}
}
