package service

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestDistributeComposeWarnings_RoutesByLayerPosition: with the
// dispatcher's per-warning layer positions a warning lands on the layer
// that raised it — a fan-out spec's second target layer, or a layer the
// earlier fan-out shifted — never on the layer at its spec index.
// Without positions (the bare ApplyComposeOverlays path) the spec index
// still routes.
func TestDistributeComposeWarnings_RoutesByLayerPosition(t *testing.T) {
	t.Parallel()
	warn := func(spec int, code string) types.OverlayWarning {
		return types.OverlayWarning{Code: code, Details: map[string]any{"overlay_index": spec}}
	}
	// Spec 0 fanned out to layers 0 and 1; spec 1 is layer 2.
	ws := []types.OverlayWarning{warn(0, "A"), warn(0, "B"), warn(1, "C")}
	layers := distributeComposeWarnings(make([]types.OverlayLayer, 3), ws, []int{0, 1, 2})
	for i, want := range []string{"A", "B", "C"} {
		if got := layers[i].Warnings; len(got) != 1 || got[0].Code != want {
			t.Errorf("layer %d warnings = %+v, want [%s]", i, got, want)
		}
	}
	bare := distributeComposeWarnings(make([]types.OverlayLayer, 3), ws, nil)
	if len(bare[0].Warnings) != 2 || len(bare[1].Warnings) != 1 || bare[2].Warnings != nil {
		t.Errorf("spec-index routing = %d / %d / %d warnings, want 2 / 1 / 0", len(bare[0].Warnings), len(bare[1].Warnings), len(bare[2].Warnings))
	}
}
