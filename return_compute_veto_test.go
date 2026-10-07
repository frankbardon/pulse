package pulse_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestReturnComputeVeto_ComposeOverlayKeepsSlotComponents: a Compose
// overlay reads its slots' Components AFTER they run (a probability-
// weighted proportion overlay places Σw on N* off the slot's weighted
// floor), so a slot `return` that excludes components must not skip
// computing them (U18 compute plan, conservative veto): the layer
// equals the unshaped run's, serial and parallel, while the slots'
// wire still drops components.
func TestReturnComputeVeto_ComposeOverlayKeepsSlotComponents(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	p := newReturnInstance(t, fs, pulse.Options{})
	prob := types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	fixture := func(ret *types.Return) *types.ComposedRequest {
		slot := func(label string, filtered bool) *types.Request {
			r := floorCrosstab(cohort, &prob, types.SlotWeight{}, nil)
			r.Label = label
			r.Return = ret
			if filtered {
				r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x >= 10"}}
			}
			return r
		}
		return &types.ComposedRequest{
			Requests: []*types.Request{slot("total", false), slot("sub", true)},
			Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell, Reference: "total", Targets: []string{"sub"}}},
		}
	}
	base, _ := composeJSON(t, p, fixture(nil), false)
	if len(base.Overlays) != 1 || base.Overlays[0].Summary == nil {
		t.Fatalf("baseline overlay layer: %+v", base.Overlays)
	}
	want, _ := json.Marshal(base.Overlays[0])
	for _, parallel := range []bool{false, true} {
		out, _ := composeJSON(t, p, fixture(&types.Return{Preset: types.ReturnPresetStandard}), parallel)
		got, _ := json.Marshal(out.Overlays[0])
		if !bytes.Equal(got, want) {
			t.Errorf("parallel=%v: overlay layer moved under a slot return\n got %s\nwant %s", parallel, got, want)
		}
		for i, r := range out.Responses {
			if r.Components != nil {
				t.Errorf("parallel=%v: slot %d kept components on the wire under standard", parallel, i)
			}
		}
	}
}
