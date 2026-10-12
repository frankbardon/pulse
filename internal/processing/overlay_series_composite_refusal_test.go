package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compositeRefusedSeriesSpecs is every SERIES overlay that reads the
// host as ONE ordered series (lag-1 prior, rolling window, positional
// baseline, first-match sibling) — refused on a composite-row host.
func compositeRefusedSeriesSpecs() []types.OverlaySpec {
	window := json.RawMessage(`{"window": 2}`)
	return []types.OverlaySpec{
		{Name: "dp", Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup},
		{Name: "ip", Kind: types.OverlayKindIndexVsPrior, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Prior: &types.OverlayPriorRef{}}},
		{Name: "irm", Kind: types.OverlayKindIndexVsRollingMean, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "zr", Kind: types.OverlayKindZScoreVsRolling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: window},
		{Name: "ib", Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "db", Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{}}},
		{Name: "is", Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"}}},
		{Name: "ds", Kind: types.OverlayKindDeltaVsSibling, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "east"}}},
	}
}

// TestOverlaySeries_CompositeHostRefused: on a host with two or more
// groupers (U54 composite rows) every row-order series kind is refused
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with details.groupers —
// mirroring descriptor.refuseCompositeSeriesHost — while the same
// spec over one grouper still folds a layer.
func TestOverlaySeries_CompositeHostRefused(t *testing.T) {
	values := func(i int) (float64, bool) { return float64(10 + i), true }
	for _, spec := range compositeRefusedSeriesSpecs() {
		t.Run(string(spec.Kind), func(t *testing.T) {
			one := NewSeriesHostViewWithGrouper(
				[]types.AxisKey{{"east"}, {"west"}, {"north"}}, values,
				[]string{"region"}, []types.GroupType{types.GROUP_CATEGORY})
			layers, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, one)
			if err != nil {
				t.Fatalf("one grouper: %v", err)
			}
			if len(layers) != 1 || layers[0].Payload.Series == nil || len(layers[0].Payload.Series.Entries) != 3 {
				t.Fatalf("one grouper: layers %+v, want one 3-entry series layer", layers)
			}
			for _, n := range []int{2, 3} {
				keys := []types.AxisKey{{"east", "F", "x"}, {"east", "M", "x"}, {"west", "F", "x"}}
				fields := []string{"region", "sex", "tier"}[:n]
				kinds := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_CATEGORY, types.GROUP_CATEGORY}[:n]
				for i := range keys {
					keys[i] = keys[i][:n]
				}
				host := NewSeriesHostViewWithGrouper(keys, values, fields, kinds)
				layers, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, host)
				if layers != nil {
					t.Errorf("%d groupers: a layer over composite rows: %+v", n, layers)
				}
				coded, ok := err.(*errors.CodedError)
				if !ok {
					t.Fatalf("%d groupers: err = %v (%T), want a coded refusal", n, err, err)
				}
				if coded.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || coded.Details["groupers"] != n || coded.Details["kind"] != string(spec.Kind) {
					t.Errorf("%d groupers: refusal %s %v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE groupers=%d", n, coded.Code, coded.Details, n)
				}
			}
		})
	}
}
