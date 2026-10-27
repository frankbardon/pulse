package mcp

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// sweepBindInstance is a Compose sweep + fits profile with or without
// capability:compose_sweep_bind.
func sweepBindInstance(t *testing.T, with bool) *descx.InstanceSnapshot {
	t.Helper()
	features := []string{"capability:compose", "capability:compose_sweep", "capability:compose_fits", "REG_OLS", "AGG_SUM"}
	if with {
		features = append(features, descx.FeatureComposeSweepBind)
	}
	_, inst := instanceFor(t, &pulse.FeatureProfile{Features: features}, pulse.Extensions{})
	return inst
}

// TestFitsSchema_FromSweep: the bound `fits` item carries `from_sweep`
// (and requires only `name`) when capability:compose_sweep_bind is
// enabled, and keeps the request-only shape when it is hidden.
func TestFitsSchema_FromSweep(t *testing.T) {
	t.Parallel()

	item := func(inst *descx.InstanceSnapshot) map[string]any {
		return fitsSchema(map[string]any{"type": "object"}, inst)["items"].(map[string]any)
	}
	on := item(sweepBindInstance(t, true))
	fs, ok := on["properties"].(map[string]any)["from_sweep"].(map[string]any)
	if !ok || !slices.Equal(fs["required"].([]string), []string{"regression"}) {
		t.Fatalf("enabled: from_sweep %v", on["properties"])
	}
	if !slices.Equal(on["required"].([]string), []string{"name"}) {
		t.Fatalf("enabled: required %v", on["required"])
	}
	off := item(sweepBindInstance(t, false))
	if _, ok := off["properties"].(map[string]any)["from_sweep"]; ok {
		t.Fatal("hidden: from_sweep still offered")
	}
	if !slices.Equal(off["required"].([]string), []string{"name", "request"}) {
		t.Fatalf("hidden: required %v", off["required"])
	}
}

// TestCheckUnknownKeysComposed_HiddenFromSweepRefusedLikeLibrary: a
// `from_sweep` key sent over MCP to an instance hiding the capability
// is refused exactly as the library refuses the typed request; an
// enabling instance accepts the key.
func TestCheckUnknownKeysComposed_HiddenFromSweepRefusedLikeLibrary(t *testing.T) {
	t.Parallel()

	body := []byte(`{"requests":[{"cohort":{"filename":"x.pulse"}}],"fits":[{"name":"best","from_sweep":{"regression":"m"}}]}`)
	typed := &types.ComposedRequest{Requests: []*types.Request{{Cohort: &types.Cohort{Filename: "x.pulse"}}},
		Fits: []types.FitSpec{{Name: "best", FromSweep: &types.FitFromSweep{Regression: "m"}}}}
	off := sweepBindInstance(t, false)
	sameRefusal(t, "from_sweep", checkUnknownKeysComposed(body, off), codedOf(t, descx.SlotRefusal(typed, off)))
	if ce := checkUnknownKeysComposed(body, sweepBindInstance(t, true)); ce != nil {
		t.Errorf("enabled: from_sweep refused: %v", ce)
	}
}
