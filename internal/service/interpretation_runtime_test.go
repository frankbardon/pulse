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
// On a SERIES kind (OVERLAY_CHISQ_ROW / _COL) the parameters ride each
// series entry's summary rather than the layer's, so the path resolves
// against every entry there. The completeness halves keep the table
// honest: an overlay Interpretation on a summary.parameters.* path fails
// every_declaration_is_probed until a probe fixture lands here, and a
// probe whose declaration is gone fails no_probe_outlives_its_declaration.
// TestOverlayParametersPathResolves keeps the resolver honest on real
// scalar and series layers.

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
var overlayInterpretationProbes = map[string]overlayInterpretationProbe{
	"OVERLAY_CHISQ_MATRIX": {fields: []string{"summary.parameters.df"}, run: func(t *testing.T) types.OverlayLayer {
		return crosstabOverlayLayer(t, types.OverlayKindChiSqMatrix, types.OverlayScopeMatrix)
	}},
	"OVERLAY_CHISQ_ROW": {fields: []string{"summary.parameters.df"}, run: func(t *testing.T) types.OverlayLayer {
		return crosstabOverlayLayer(t, types.OverlayKindChiSqRow, types.OverlayScopeRow)
	}},
	"OVERLAY_CHISQ_COL": {fields: []string{"summary.parameters.df"}, run: func(t *testing.T) types.OverlayLayer {
		return crosstabOverlayLayer(t, types.OverlayKindChiSqCol, types.OverlayScopeColumn)
	}},
	"OVERLAY_CHISQ_VS_POP": {fields: []string{"summary.parameters.df"}, run: chiSqVsPopFacetLayer},
	"OVERLAY_CHISQ_VS_REF": {fields: []string{"summary.parameters.df"}, run: chiSqVsRefComposeLayer},
	"OVERLAY_KS_VS_POP":    {fields: []string{"summary.parameters.n_pop", "summary.parameters.n_subset"}, run: ksVsPopFacetLayer},
}

// overlayLayerPath resolves a summary.parameters.<key> path against the
// Go value of the layer's parameters, reporting key PRESENCE: in
// layer.Summary.Parameters, or — for a SERIES layer, whose per-entry
// summaries carry the test — in every entry's Summary.Parameters.
func overlayLayerPath(layer types.OverlayLayer, field string) bool {
	key, ok := strings.CutPrefix(field, "summary.parameters.")
	if !ok || key == "" || strings.Contains(key, ".") {
		return false
	}
	if layer.Summary != nil {
		if _, ok := layer.Summary.Parameters[key]; ok {
			return true
		}
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) == 0 {
		return false
	}
	for _, e := range layer.Payload.Series.Entries {
		if _, ok := e.Summary.Parameters[key]; !ok {
			return false
		}
	}
	return true
}

// crosstabOverlayLayer runs a per-Request crosstab overlay kind over a
// count crosstab (region x segment, AGG_COUNT cells — the counts the
// chi-square family is defined on) and returns its layer.
func crosstabOverlayLayer(t *testing.T, kind types.OverlayKind, scope types.OverlayScope) types.OverlayLayer {
	t.Helper()
	cfg := setupTestFS(t, "ct.pulse", crosstabSchema(), crosstabRecords())
	resp, err := New(cfg).Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: []types.OverlaySpec{{Name: "probe", Kind: kind, Scope: scope}},
	})
	if err != nil {
		t.Fatalf("Process(%s): %v", kind, err)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("Response.Overlays = %d layers, want 1", len(resp.Overlays))
	}
	return resp.Overlays[0]
}

// chiSqVsRefComposeLayer runs OVERLAY_CHISQ_VS_REF across two count
// crosstab slots of a Compose request and returns its layer.
func chiSqVsRefComposeLayer(t *testing.T) types.OverlayLayer {
	t.Helper()
	slot := func(label, file string) *types.Request {
		req := parityRequestMatrix(label, file)
		req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"}
		return req
	}
	res, err := parityServiceWithCohorts(t).Compose(context.Background(), &types.ComposedRequest{
		Requests: []*types.Request{slot("baseline", "baseline.pulse"), slot("treatment", "treatment.pulse")},
		Overlays: []types.ComposeOverlaySpec{{
			Name:      "probe",
			Kind:      types.OverlayKindChiSqVsRef,
			Scope:     types.OverlayScopeMatrix,
			Reference: "baseline",
			Targets:   []string{"treatment"},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(res.Overlays) != 1 {
		t.Fatalf("ComposedResponse.Overlays = %d layers, want 1", len(res.Overlays))
	}
	return res.Overlays[0]
}

// ksVsPopFacetLayer runs OVERLAY_KS_VS_POP on the facet host fixture's
// numeric field (histogram arm) and returns its layer.
func ksVsPopFacetLayer(t *testing.T) types.OverlayLayer {
	t.Helper()
	svc, path := buildFacetOverlayCohort(t)
	res, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort:           &types.Cohort{Filename: path},
		Fields:           []string{"score"},
		IncludeHistogram: true,
		HistogramBins:    10,
		HistogramRange:   [2]float64{0, 100},
		Overlays: []types.OverlaySpec{{
			Name:   "ks-vs-pop",
			Kind:   types.OverlayKindKSVsPop,
			Scope:  types.OverlayScopeGroup,
			Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
			Params: json.RawMessage(`{"field":"score"}`),
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

	// SERIES layer: the parameters ride each entry, never the layer.
	row := crosstabOverlayLayer(t, types.OverlayKindChiSqRow, types.OverlayScopeRow)
	if row.Summary != nil && row.Summary.Parameters != nil {
		t.Fatalf("OVERLAY_CHISQ_ROW carries layer-level parameters %v; the series arm of overlayLayerPath is untested", row.Summary.Parameters)
	}
	if !overlayLayerPath(row, "summary.parameters.df") {
		t.Errorf("series layer: summary.parameters.df did not resolve on the entries (%+v)", row.Payload.Series)
	}
	if overlayLayerPath(row, "summary.parameters.n_pop") {
		t.Error("series layer: summary.parameters.n_pop resolved, but no entry carries it")
	}
	partial := row
	partial.Payload.Series = &types.SeriesPayload{Entries: append([]types.SeriesEntry{{}}, row.Payload.Series.Entries...)}
	if overlayLayerPath(partial, "summary.parameters.df") {
		t.Error("series layer: summary.parameters.df resolved although one entry lacks it")
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
