package service

// INTERPRETATION HONESTY (overlays).
//
// The internal/service twin of internal/processing's
// TestInterpretationFieldsHoldAtRuntime. An overlay Interpretation on a
// summary.parameters.<key> path names a key of a map[string]float64 that
// exists only at run time, so internal/descriptor defers it
// (descx.DeclaredInterpretationFields, Deferred, Category "overlay").
// Here a deferred overlay path meets a real layer: each probe runs its
// overlay kind end to end through a host (facet / crosstab / compose)
// fixture and requires the key in the Go value of
// OverlayLayer.Summary.Parameters.
//
// No overlay kind declares an Interpretation yet, so overlayInterpretation
// Probes starts empty — the completeness halves are what matter: the
// first overlay Interpretation on a summary.parameters.* path fails
// every_declaration_is_probed until a probe fixture lands here, and a
// probe whose declaration is gone fails no_probe_outlives_its_declaration.
// TestOverlayParametersPathResolves keeps the resolver honest on a real
// layer so the empty table is not also an untested one.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// overlayInterpretationProbe runs its overlay kind through a host
// fixture and returns the emitted layer, plus the deferred fields it
// proves present.
type overlayInterpretationProbe struct {
	fields []string
	run    func(t *testing.T) types.OverlayLayer
}

// overlayInterpretationProbes is keyed by OVERLAY_* kind. Held two-way
// against the deferred overlay entries of descx.DeclaredInterpretationFields.
var overlayInterpretationProbes = map[string]overlayInterpretationProbe{}

// overlayLayerPath resolves a summary.parameters.<key> path against the
// Go value of layer.Summary.Parameters, reporting key PRESENCE.
func overlayLayerPath(layer types.OverlayLayer, field string) bool {
	key, ok := strings.CutPrefix(field, "summary.parameters.")
	if !ok || key == "" || strings.Contains(key, ".") || layer.Summary == nil {
		return false
	}
	_, ok = layer.Summary.Parameters[key]
	return ok
}

// chiSqVsPopFacetLayer runs OVERLAY_CHISQ_VS_POP on the facet host
// fixture and returns its layer.
func chiSqVsPopFacetLayer(t *testing.T) types.OverlayLayer {
	t.Helper()
	svc, path := buildFacetOverlayCohort(t)
	res, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{{
			Name:   "chisq-vs-pop",
			Kind:   types.OverlayKindChiSqVsPop,
			Scope:  types.OverlayScopeGroup,
			Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
			Params: json.RawMessage(`{"field":"category"}`),
		}},
	})
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if len(res.Overlays) != 1 {
		t.Fatalf("FacetResult.Overlays = %d layers, want 1", len(res.Overlays))
	}
	return res.Overlays[0]
}

// TestOverlayParametersPathResolves pins overlayLayerPath on a real
// emitted layer: CHISQ_VS_POP carries summary.parameters.df, and a key
// it does not carry (or a malformed path) does not resolve.
func TestOverlayParametersPathResolves(t *testing.T) {
	layer := chiSqVsPopFacetLayer(t)
	cases := []struct {
		field string
		want  bool
	}{
		{"summary.parameters.df", true},
		{"summary.parameters.odds_ratio", false},
		{"summary.parameters.", false},
		{"summary.parameters.df.extra", false},
		{"summary.df", false},
		{"details.df", false},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			if got := overlayLayerPath(layer, tc.field); got != tc.want {
				t.Errorf("overlayLayerPath(%q) = %v, want %v (parameters %v)", tc.field, got, tc.want, layer.Summary)
			}
		})
	}
	if overlayLayerPath(types.OverlayLayer{}, "summary.parameters.df") {
		t.Error("a layer with no Summary resolved summary.parameters.df")
	}
}

// TestOverlayInterpretationFieldsHoldAtRuntime proves every deferred
// overlay Interpretation field is really emitted by its kind.
func TestOverlayInterpretationFieldsHoldAtRuntime(t *testing.T) {
	declared := map[string]map[string]bool{}
	for _, d := range descx.DeclaredInterpretationFields() {
		if !d.Deferred || d.Category != "overlay" {
			continue
		}
		if declared[d.Name] == nil {
			declared[d.Name] = map[string]bool{}
		}
		declared[d.Name][d.Field] = true
	}

	t.Run("every_declaration_is_probed", func(t *testing.T) {
		for name, fields := range declared {
			probe, ok := overlayInterpretationProbes[name]
			have := map[string]bool{}
			if ok {
				for _, f := range probe.fields {
					have[f] = true
				}
			}
			for f := range fields {
				if !have[f] {
					t.Errorf("%s declares deferred Interpretation field %q with no probe in overlayInterpretationProbes — "+
						"only a runtime probe can show a summary.parameters key exists; add a host fixture for it", name, f)
				}
			}
		}
	})

	t.Run("no_probe_outlives_its_declaration", func(t *testing.T) {
		for name, probe := range overlayInterpretationProbes {
			for _, f := range probe.fields {
				if !declared[name][f] {
					t.Errorf("overlayInterpretationProbes probes %s %q, which is not a declared deferred Interpretation field; "+
						"drop the probe or restore the declaration", name, f)
				}
			}
		}
	})

	names := make([]string, 0, len(overlayInterpretationProbes))
	for name := range overlayInterpretationProbes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		probe := overlayInterpretationProbes[name]
		t.Run(name, func(t *testing.T) {
			layer := probe.run(t)
			if string(layer.Kind) != name {
				t.Fatalf("probe for %s emitted a %s layer", name, layer.Kind)
			}
			for _, f := range probe.fields {
				if !overlayLayerPath(layer, f) {
					t.Errorf("%s declares an Interpretation for %q, but the emitted layer carries no such key (summary %+v)", name, f, layer.Summary)
				}
			}
		})
	}
}
