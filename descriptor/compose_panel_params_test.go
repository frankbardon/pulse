package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-time params gate for the COMPOSE-host OVERLAY_PROP_Z_PANEL.
//
// ComposedRequest.Overlays is the ONLY slot the panel can execute out
// of — processing/overlay_compose_dispatch.go is the only dispatch
// table carrying the kind, ChainRequest.Overlays refuses it via
// chainOverlayKindAllowed and FacetRequest.Overlays via
// isFacetOverlayKind — so descriptor.ValidateCompose is the only
// predict arm that has to judge its params.

// composePanelRequest builds a minimal reference + one-target panel
// ComposedRequest carrying params. Both slots are MATRIX with the same
// axis schema so every structural gate downstream of the params gate
// passes and the envelope isolates the params verdict.
func composePanelRequest(params map[string]any) *types.ComposedRequest {
	return &types.ComposedRequest{
		Requests: []*types.Request{
			matrixCrosstabRequest("ref", "row", "col"),
			matrixCrosstabRequest("t1", "row", "col"),
		},
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "panel",
				Kind:      types.OverlayKindPropZPanel,
				Scope:     types.OverlayScopeCell,
				Reference: "ref",
				Targets:   []string{"t1"},
				Params:    params,
			},
		},
	}
}

// Absent params and an empty params object are the SAME clean verdict.
// This is the predict half of the story's byte-identity property: a
// spec that names no panel params must be indistinguishable from the
// pre-params baseline on every surface, envelope included.
func TestValidateCompose_PanelParamsAbsentAndEmptyAreClean(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"absent", nil},
		{"empty object", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := ValidateCompose(composePanelRequest(tc.params))
			if len(env.Errors) != 0 {
				t.Fatalf("expected a clean envelope, got %+v", env.Errors)
			}
			result := env.Data.(*ComposeValidationResult)
			if !result.Valid {
				t.Fatal("expected Valid=true for a params-free panel spec")
			}
		})
	}
}

// A params blob that cannot decode into types.PanelOverlayParams fires
// PULSE_OVERLAY_PARAM_MISSING rather than being dropped on the floor.
// The Details shape matches validateOverlayPairwise's so one renderer
// branch handles both families.
func TestValidateCompose_PanelParamsMalformedRefused(t *testing.T) {
	// A channel is the one value a map[string]any params slot can hold
	// that the JSON encoder cannot represent — the COMPOSE carrier is
	// already-decoded, so this is the reachable malformed shape while
	// the struct has no typed fields to mismatch against.
	env := ValidateCompose(composePanelRequest(map[string]any{"n_source": make(chan int)}))
	if !envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING, got %+v", env.Errors)
	}
	var found bool
	for _, e := range env.Errors {
		if e.Code != string(errors.PULSE_OVERLAY_PARAM_MISSING) {
			continue
		}
		found = true
		if e.Details["kind"] != string(types.OverlayKindPropZPanel) {
			t.Errorf("Details[kind] = %v, want %s", e.Details["kind"], types.OverlayKindPropZPanel)
		}
		if e.Details["index"] != 0 {
			t.Errorf("Details[index] = %v, want 0", e.Details["index"])
		}
	}
	if !found {
		t.Fatal("no PULSE_OVERLAY_PARAM_MISSING entry to inspect")
	}
	result := env.Data.(*ComposeValidationResult)
	if result.Valid {
		t.Error("expected Valid=false for a malformed params blob")
	}
}

// The MaxPanelTargets cap rides OverlaySpec.Options, NOT params, and
// stays the FIRST refusal: an over-cap spec is structurally wrong
// whatever its params say, and reporting the params instead would send
// the caller to the wrong knob. The cap gate returns, so a spec that is
// both over-cap and malformed reports the cap ALONE.
func TestValidateCompose_PanelOverCapFiresBeforeParams(t *testing.T) {
	requests := []*types.Request{matrixCrosstabRequest("ref", "row", "col")}
	targets := make([]string, 0, 4)
	for i := 1; i <= 4; i++ {
		label := "t" + composeDescriptorItoa(i)
		requests = append(requests, matrixCrosstabRequest(label, "row", "col"))
		targets = append(targets, label)
	}
	req := &types.ComposedRequest{
		Requests: requests,
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "panel",
				Kind:      types.OverlayKindPropZPanel,
				Scope:     types.OverlayScopeCell,
				Reference: "ref",
				Targets:   targets,
				Options:   &types.OverlayOptions{MaxPanelTargets: 3},
				Params:    map[string]any{"n_source": make(chan int)},
			},
		},
	}
	env := ValidateCompose(req)
	if !envHasCode(env, errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
		t.Fatalf("expected PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP, got %+v", env.Errors)
	}
	if envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("the cap gate must short-circuit the params gate, got %+v", env.Errors)
	}
}

// The params gate is scoped to the kind that owns the shape. The
// descriptive multi-reference sibling OVERLAY_PANEL_INDEX_VS_REF shares
// the cap but not the params type, so a blob that would refuse on the
// prop-Z panel must not refuse here.
func TestValidateCompose_PanelParamsGateScopedToPropZPanel(t *testing.T) {
	req := composePanelRequest(map[string]any{"n_source": make(chan int)})
	req.Overlays[0].Kind = types.OverlayKindPanelIndexVsRef
	env := ValidateCompose(req)
	if envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("OVERLAY_PANEL_INDEX_VS_REF must not inherit the panel params gate, got %+v", env.Errors)
	}
}
