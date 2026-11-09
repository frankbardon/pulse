package overlayhost

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestComposeFanOutKinds(t *testing.T) {
	t.Parallel()
	want := map[types.OverlayKind]bool{
		types.OverlayKindIndexVsRef: true,
		types.OverlayKindDeltaVsRef: true,
		types.OverlayKindTVsRef:     true,
		types.OverlayKindZVsRef:     true,
	}
	for _, k := range types.AllOverlayKinds() {
		if got := ComposeFanOutKind(k); got != want[k] {
			t.Errorf("ComposeFanOutKind(%s) = %v, want %v", k, got, want[k])
		}
		multi := want[k] || k == types.OverlayKindPanelIndexVsRef
		if got := ComposeMultiLayerKind(k); got != multi {
			t.Errorf("ComposeMultiLayerKind(%s) = %v, want %v", k, got, multi)
		}
	}
	if got := ComposeFanOutKinds(); len(got) != len(want) {
		t.Errorf("ComposeFanOutKinds() = %v, want %d kinds", got, len(want))
	}
}

func TestComposeLayerSpecs(t *testing.T) {
	t.Parallel()
	specs := []types.ComposeOverlaySpec{
		{Kind: types.OverlayKindTVsRef, Targets: []string{"a", "b", "c"}},
		{Kind: types.OverlayKindPropZCell, Targets: []string{"a", "b"}},
		{Kind: types.OverlayKindIndexVsRef, Targets: []string{"a"}},
		{Kind: types.OverlayKindPanelIndexVsRef, Targets: []string{"a", "b"}},
	}
	spec, target := ComposeLayerSpecs(specs, nil)
	if want := []int{0, 0, 0, 1, 2, 3, 3}; !reflect.DeepEqual(spec, want) {
		t.Errorf("spec = %v, want %v", spec, want)
	}
	if want := []int{0, 1, 2, 0, 0, 0, 1}; !reflect.DeepEqual(target, want) {
		t.Errorf("target = %v, want %v", target, want)
	}
	// A route that hides the fan-out reading keeps one layer per spec.
	spec, _ = ComposeLayerSpecs(specs[:1], func(types.OverlayKind) types.OverlayKind { return "OVERLAY_X_EXT" })
	if want := []int{0}; !reflect.DeepEqual(spec, want) {
		t.Errorf("routed spec = %v, want %v", spec, want)
	}
}

func TestComposeFanOutLayerName(t *testing.T) {
	t.Parallel()
	named := &types.ComposeOverlaySpec{Name: "lift", Kind: types.OverlayKindTVsRef}
	if got := ComposeFanOutLayerName(named, "b", 1); got != "lift" {
		t.Errorf("one target = %q, want lift", got)
	}
	if got := ComposeFanOutLayerName(named, "b", 2); got != "lift__b" {
		t.Errorf("two targets = %q, want lift__b", got)
	}
	unnamed := &types.ComposeOverlaySpec{Kind: types.OverlayKindTVsRef}
	if got := ComposeFanOutLayerName(unnamed, "b", 3); got != "OVERLAY_T_VS_REF__b" {
		t.Errorf("unnamed = %q", got)
	}
}

func TestComposeFanOutCapRefusal(t *testing.T) {
	t.Parallel()
	targets := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "t"
		}
		return out
	}
	spec := &types.ComposeOverlaySpec{Kind: types.OverlayKindZVsRef, Targets: targets(17)}
	ce := ComposeFanOutCapRefusal(spec, spec.Kind, 2)
	if ce == nil || ce.Code != errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP {
		t.Fatalf("17 targets = %v, want PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP", ce)
	}
	want := map[string]any{"index": 2, "kind": "OVERLAY_Z_VS_REF", "observed": 17, "cap": 16}
	if !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("Details = %v, want %v", ce.Details, want)
	}
	spec.Targets = targets(16)
	if ce := ComposeFanOutCapRefusal(spec, spec.Kind, 2); ce != nil {
		t.Errorf("16 targets refused: %v", ce)
	}
	spec.Options = &types.OverlayOptions{MaxPanelTargets: 2}
	spec.Targets = targets(3)
	if ce := ComposeFanOutCapRefusal(spec, spec.Kind, 0); ce == nil || ce.Details["cap"] != 2 {
		t.Errorf("Options cap 2 over 3 targets = %v", ce)
	}
	// Not a fan-out kind: the panel kinds keep their own arms.
	panel := &types.ComposeOverlaySpec{Kind: types.OverlayKindPanelIndexVsRef, Targets: targets(17)}
	if ce := ComposeFanOutCapRefusal(panel, panel.Kind, 0); ce != nil {
		t.Errorf("panel kind refused by the fan-out cap: %v", ce)
	}
}
