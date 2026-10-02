package descriptor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// The request-time overlay kind gates — Request host (ValidateOverlays),
// Compose host (ValidateComposeWithOptions) and Facet host
// (ValidateFacetOverlaysWithOptions) — report a kind PredictOptions.
// Instance hides exactly as a kind not in the catalog, and every
// kind-keyed rule after the gate (Level/Within, the compose cost) takes
// the never-registered branch. Each case also runs the hidden kind
// unscoped, so a case the gate never reaches fails as vacuous.

const hiddenNeverOverlay = "OVERLAY_NEVER_REGISTERED"

func hiddenOverlayOpts(kind types.OverlayKind) *PredictOptions {
	return &PredictOptions{Instance: NewInstanceSnapshot(nil, FeatureSet{Hidden: []string{string(kind)}})}
}

// hiddenOverlayParity compares the hidden and never-registered
// outcomes after substitution and requires the unscoped control to
// differ.
func hiddenOverlayParity(t *testing.T, hidden types.OverlayKind, run func(kind types.OverlayKind, opts *PredictOptions) string) {
	t.Helper()
	opts := hiddenOverlayOpts(hidden)
	got, want, control := run(hidden, opts), run(hiddenNeverOverlay, opts), run(hidden, nil)
	if sub := strings.ReplaceAll(got, string(hidden), hiddenNeverOverlay); sub != want {
		t.Errorf("hidden %s diverges from never-registered\nhidden: %s\nnever:  %s", hidden, got, want)
	}
	if strings.ReplaceAll(control, string(hidden), hiddenNeverOverlay) == want {
		t.Errorf("vacuous: unscoped %s behaves like a never-registered kind\ncontrol: %s", hidden, control)
	}
}

func hiddenEnvJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestValidateOverlays_HiddenKindIsNotInCatalog: Request host. An
// out-of-range Level makes the Level/Within gate kind-keyed too: a
// registered share kind reports its axis, an unknown kind does not.
func TestValidateOverlays_HiddenKindIsNotInCatalog(t *testing.T) {
	hiddenOverlayParity(t, types.OverlayKindShareOfRow, func(kind types.OverlayKind, opts *PredictOptions) string {
		req := &types.Request{
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "a"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b"}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "a"},
			},
			Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeCell, Level: 5}},
		}
		env := descriptor.NewEnvelope(nil)
		ValidateOverlays(env, req, nil, opts)
		return hiddenEnvJSON(t, env.Errors)
	})
}

// TestValidateOverlays_HiddenKindLevelWithinRoute pins the Level/Within
// gate alone: with the catalog gate passed for every kind it is the
// only kind-keyed rule left.
func TestValidateOverlays_HiddenKindLevelWithinRoute(t *testing.T) {
	hiddenOverlayParity(t, types.OverlayKindIndexVsTotal, func(kind types.OverlayKind, opts *PredictOptions) string {
		req := &types.Request{
			Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "a"}},
			Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeRow, Level: 1}},
		}
		env := descriptor.NewEnvelope(nil)
		validateOverlayLevelWithinPredict(env, req, &req.Overlays[0], opts.overlayRoute(kind), 0)
		return hiddenEnvJSON(t, env.Errors)
	})
}

// TestValidateCompose_HiddenKindIsNotInCatalog: Compose host — the
// gate-0 error and the per-spec cost.
func TestValidateCompose_HiddenKindIsNotInCatalog(t *testing.T) {
	hiddenOverlayParity(t, types.OverlayKindIndexVsRef, func(kind types.OverlayKind, opts *PredictOptions) string {
		slot := func(label string) *types.Request {
			return &types.Request{
				Label:        label,
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "a"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a"}},
			}
		}
		req := &types.ComposedRequest{
			Requests: []*types.Request{slot("x"), slot("y")},
			Overlays: []types.ComposeOverlaySpec{{Kind: kind, Scope: types.OverlayScopeGroup, Reference: "x", Targets: []string{"y"}}},
		}
		env := ValidateComposeWithOptions(req, opts)
		res := env.Data.(*ComposeValidationResult)
		return hiddenEnvJSON(t, map[string]any{"errors": env.Errors, "cost": res.OverlayCost})
	})
}

// TestValidateFacetOverlays_HiddenKindIsNotInCatalog: Facet host.
func TestValidateFacetOverlays_HiddenKindIsNotInCatalog(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	hiddenOverlayParity(t, types.OverlayKindIndexVsPop, func(kind types.OverlayKind, opts *PredictOptions) string {
		req := &types.FacetRequest{
			Fields: []string{"category"},
			Overlays: []types.OverlaySpec{{
				Kind: kind, Scope: types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: json.RawMessage(`{"field":"category"}`),
			}},
		}
		env := descriptor.NewEnvelope(nil)
		ValidateFacetOverlaysWithOptions(env, req, schema, opts)
		return hiddenEnvJSON(t, env.Errors)
	})
}

// TestValidateFacetWithOptions_HiddenKindReachesGate: the facet predict
// entry point hands its options to the Facet-host gate. Errors only —
// the OverlaysApplied / cost surface is predict's (E1-S4).
func TestValidateFacetWithOptions_HiddenKindReachesGate(t *testing.T) {
	data := buildFacetOverlayPulseBytes(t)
	hiddenOverlayParity(t, types.OverlayKindIndexVsPop, func(kind types.OverlayKind, opts *PredictOptions) string {
		req := &types.FacetRequest{
			Cohort: &types.Cohort{Filename: "x.pulse"},
			Fields: []string{"category"},
			Overlays: []types.OverlaySpec{{
				Kind: kind, Scope: types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: json.RawMessage(`{"field":"category"}`),
			}},
		}
		env := ValidateFacetWithOptions(bytes.NewReader(data), req, opts)
		return hiddenEnvJSON(t, env.Errors)
	})
}
