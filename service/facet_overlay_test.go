package service

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// FACET-host overlay integration tests (E5-S6). The handlers themselves
// land in E5-S2..S5 with byte-equality unit tests; this surface exercises
// the service-layer wiring — FacetSchema runs against the host cohort,
// resolves each spec's Ref.Population to a sibling FacetSchema call,
// dispatches the overlay handler via processing.ApplyOverlaysFacet, and
// attaches the resulting layers to FacetResult.Overlays in spec order.
//
// Coverage:
//
//   - The no-overlay byte-identity contract (empty slot keeps the
//     pre-E5 JSON output verbatim).
//   - Spec-order layer emission across mixed streamable / buffered kinds.
//   - End-to-end dispatch of all four kinds in one request.
//   - Service-layer ApplyOverlaysFacet hookup verified by populated
//     FacetResult.Overlays slices.
//   - Population cohort cycle: when Ref.Population.Cohort names the same
//     file as the host, the population materialisation produces the
//     unfiltered baseline (which is the same as the host's payload here
//     since no host filter is applied — the resolver still works as
//     expected and the index/zscore math reduces to the no-divergence
//     baseline).

// buildFacetOverlayCohort writes a fixture with a categorical "category"
// column and a numeric "score" column. Distinct values:
//
//   - category: {a, b, c, d}; counts per value {30, 30, 30, 10} over 100 rows.
//   - score: floats spanning [0, 100); used for the numeric KS arm.
//
// Returns the service handle and the cohort filename. The same fixture
// is used for host and population to drive the same-cohort population
// path — the per-kind handlers verify only structural / shape contracts
// here, not numerical divergence (the dedicated processing tests cover
// per-kind math).
func buildFacetOverlayCohort(t *testing.T) (*Service, string) {
	t.Helper()
	memFs := afero.NewMemMapFs()

	dict := encoding.NewDictionary()
	cats := []string{"a", "b", "c", "d"}
	for _, v := range cats {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "category", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}

	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)

	type row struct {
		cat   uint8
		score float64
	}
	rows := make([]row, 0, 100)
	// 30 "a" rows
	for i := 0; i < 30; i++ {
		rows = append(rows, row{0, float64(i)})
	}
	// 30 "b" rows
	for i := 0; i < 30; i++ {
		rows = append(rows, row{1, float64(30 + i)})
	}
	// 30 "c" rows
	for i := 0; i < 30; i++ {
		rows = append(rows, row{2, float64(60 + i)})
	}
	// 10 "d" rows
	for i := 0; i < 10; i++ {
		rows = append(rows, row{3, float64(90 + i)})
	}
	for _, r := range rows {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, uint64(r.cat))
		encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(r.score))
	}

	afero.WriteFile(memFs, "fixture.pulse", buf.Bytes(), 0644)
	cfg, _ := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	return New(cfg), "fixture.pulse"
}

// TestFacetWithOverlays_HostByteIdenticalToNoOverlays asserts that
// running FacetSchema with an empty Overlays slot produces JSON output
// byte-identical to the pre-E5 baseline. This is the additive byte-
// identity contract — viewers that don't set Overlays see no change in
// payload shape (no extra "overlays": null key, no spurious field).
func TestFacetWithOverlays_HostByteIdenticalToNoOverlays(t *testing.T) {
	svc, path := buildFacetOverlayCohort(t)
	ctx := context.Background()

	baseline, err := svc.FacetSchema(ctx, &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"category"},
	})
	if err != nil {
		t.Fatalf("FacetSchema baseline: %v", err)
	}
	baselineJSON, err := json.Marshal(baseline)
	if err != nil {
		t.Fatalf("marshal baseline: %v", err)
	}

	withEmpty, err := svc.FacetSchema(ctx, &types.FacetRequest{
		Cohort:   &types.Cohort{Filename: path},
		Fields:   []string{"category"},
		Overlays: nil,
	})
	if err != nil {
		t.Fatalf("FacetSchema with empty Overlays: %v", err)
	}
	withEmptyJSON, err := json.Marshal(withEmpty)
	if err != nil {
		t.Fatalf("marshal with empty Overlays: %v", err)
	}
	if string(baselineJSON) != string(withEmptyJSON) {
		t.Fatalf("byte-identity broken — empty Overlays slot produced divergent JSON\nbaseline: %s\n     got: %s", baselineJSON, withEmptyJSON)
	}
	if withEmpty.Overlays != nil {
		t.Errorf("FacetResult.Overlays should be nil when request carries no overlays; got %d entries", len(withEmpty.Overlays))
	}
	// Verify the "overlays" key does NOT appear in the JSON output — the
	// omitempty rule should drop the field entirely when the slice is
	// nil.
	if strings.Contains(string(withEmptyJSON), "\"overlays\"") {
		t.Errorf("FacetResult JSON should not carry the \"overlays\" key when the slice is nil; got: %s", withEmptyJSON)
	}
}

// TestFacetWithOverlays_EndToEndAllFourKinds asserts that a single
// FacetRequest carrying all four FACET-host overlay kinds emits four
// OverlayLayers in spec order — every kind is reachable end-to-end via
// the FacetSchema service-layer entry. The dedicated processing tests
// cover per-kind numerics; this test verifies the wiring.
func TestFacetWithOverlays_EndToEndAllFourKinds(t *testing.T) {
	svc, path := buildFacetOverlayCohort(t)
	ctx := context.Background()

	categoryField := json.RawMessage(`{"field":"category"}`)
	scoreField := json.RawMessage(`{"field":"score"}`)

	req := &types.FacetRequest{
		Cohort:             &types.Cohort{Filename: path},
		Fields:             []string{"category", "score"},
		IncludeHistogram:   true,
		HistogramBins:      10,
		HistogramRange:     [2]float64{0, 100},
		NumericPercentiles: []float64{0.25, 0.5, 0.75},
		Overlays: []types.OverlaySpec{
			{
				Name:   "index-vs-pop",
				Kind:   types.OverlayKindIndexVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
				Params: categoryField,
			},
			{
				Name:   "zscore-vs-pop",
				Kind:   types.OverlayKindZScoreVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
				Params: categoryField,
			},
			{
				Name:   "chisq-vs-pop",
				Kind:   types.OverlayKindChiSqVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
				Params: categoryField,
			},
			{
				Name:   "ks-vs-pop",
				Kind:   types.OverlayKindKSVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
				Params: scoreField,
			},
		},
	}

	result, err := svc.FacetSchema(ctx, req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if len(result.Overlays) != 4 {
		t.Fatalf("FacetResult.Overlays length = %d, want 4 (one per spec)", len(result.Overlays))
	}
	expectedKinds := []types.OverlayKind{
		types.OverlayKindIndexVsPop,
		types.OverlayKindZScoreVsPop,
		types.OverlayKindChiSqVsPop,
		types.OverlayKindKSVsPop,
	}
	for i, want := range expectedKinds {
		got := result.Overlays[i]
		if got.Kind != want {
			t.Errorf("Overlays[%d].Kind = %q, want %q (spec-order layer emission broken)", i, got.Kind, want)
		}
		if got.Scope != types.OverlayScopeGroup {
			t.Errorf("Overlays[%d].Scope = %q, want %q", i, got.Scope, types.OverlayScopeGroup)
		}
	}
}

// TestFacetWithOverlays_LayerOrderPreserved asserts that when a single
// FacetRequest mixes streamable (INDEX_VS_POP / ZSCORE_VS_POP) and
// buffered (CHISQ_VS_POP / KS_VS_POP) overlay kinds, the resulting layer
// order matches spec order element-for-element regardless of per-kind
// streamability. The buffered exit hook runs every kind through the same
// dispatch loop, so the layer order is purely spec-driven.
func TestFacetWithOverlays_LayerOrderPreserved(t *testing.T) {
	svc, path := buildFacetOverlayCohort(t)
	ctx := context.Background()

	categoryField := json.RawMessage(`{"field":"category"}`)
	scoreField := json.RawMessage(`{"field":"score"}`)

	// Mix the order: buffered → streamable → buffered → streamable.
	req := &types.FacetRequest{
		Cohort:             &types.Cohort{Filename: path},
		Fields:             []string{"category", "score"},
		IncludeHistogram:   true,
		HistogramBins:      10,
		HistogramRange:     [2]float64{0, 100},
		NumericPercentiles: []float64{0.5},
		Overlays: []types.OverlaySpec{
			{Kind: types.OverlayKindChiSqVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}}, Params: categoryField},
			{Kind: types.OverlayKindIndexVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}}, Params: categoryField},
			{Kind: types.OverlayKindKSVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}}, Params: scoreField},
			{Kind: types.OverlayKindZScoreVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}}, Params: categoryField},
		},
	}

	result, err := svc.FacetSchema(ctx, req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if len(result.Overlays) != 4 {
		t.Fatalf("FacetResult.Overlays length = %d, want 4", len(result.Overlays))
	}
	expected := []types.OverlayKind{
		types.OverlayKindChiSqVsPop,
		types.OverlayKindIndexVsPop,
		types.OverlayKindKSVsPop,
		types.OverlayKindZScoreVsPop,
	}
	for i, want := range expected {
		if result.Overlays[i].Kind != want {
			t.Errorf("Overlays[%d].Kind = %q, want %q (spec-order layer emission broken across mixed streamable/buffered)", i, result.Overlays[i].Kind, want)
		}
	}
}

// TestFacetWithOverlays_StreamingPathEngagesStreamableKinds asserts that
// running OVERLAY_INDEX_VS_POP (a streamable FACET kind) against a
// single-field FacetSchema host produces a populated overlay layer
// without errors. Streamable kinds run at the same post-finalize exit as
// buffered kinds (the host FacetSchema path is structurally buffered);
// the streamability flag is informational at the wiring level.
func TestFacetWithOverlays_StreamingPathEngagesStreamableKinds(t *testing.T) {
	svc, path := buildFacetOverlayCohort(t)
	ctx := context.Background()

	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
			},
		},
	}
	result, err := svc.FacetSchema(ctx, req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if len(result.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(result.Overlays))
	}
	if result.Overlays[0].Kind != types.OverlayKindIndexVsPop {
		t.Errorf("Overlays[0].Kind = %q, want %q", result.Overlays[0].Kind, types.OverlayKindIndexVsPop)
	}
}

// TestFacetWithOverlays_BufferedPathEngagesBufferedKinds asserts that
// running OVERLAY_CHISQ_VS_POP (a buffered FACET kind) against a
// categorical host produces a populated overlay layer without errors.
// The buffered FACET path is the canonical engagement surface for the
// inferential kinds.
func TestFacetWithOverlays_BufferedPathEngagesBufferedKinds(t *testing.T) {
	svc, path := buildFacetOverlayCohort(t)
	ctx := context.Background()

	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindChiSqVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
			},
		},
	}
	result, err := svc.FacetSchema(ctx, req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if len(result.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(result.Overlays))
	}
	if result.Overlays[0].Kind != types.OverlayKindChiSqVsPop {
		t.Errorf("Overlays[0].Kind = %q, want %q", result.Overlays[0].Kind, types.OverlayKindChiSqVsPop)
	}
}

// TestFacetWithOverlays_SingleFieldDefaultsToHost asserts that when the
// FacetRequest declares exactly one Field, an OverlaySpec without
// Params["field"] resolves to that single field automatically.
func TestFacetWithOverlays_SingleFieldDefaultsToHost(t *testing.T) {
	svc, path := buildFacetOverlayCohort(t)
	ctx := context.Background()

	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
				// No Params — single-field default should apply.
			},
		},
	}
	result, err := svc.FacetSchema(ctx, req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if len(result.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(result.Overlays))
	}
}

// TestFacetWithOverlays_MultiFieldRequiresParamsField asserts that when
// a FacetRequest declares multiple Fields and a spec omits
// Params["field"], the service-side dispatch surfaces a coded error
// (the runtime mirror of the predict-time PULSE_OVERLAY_PARAM_MISSING).
func TestFacetWithOverlays_MultiFieldRequiresParamsField(t *testing.T) {
	svc, path := buildFacetOverlayCohort(t)
	ctx := context.Background()

	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"category", "score"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
				// Multi-field FacetRequest without a field param —
				// runtime dispatch should reject.
			},
		},
	}
	_, err := svc.FacetSchema(ctx, req)
	if err == nil {
		t.Fatalf("FacetSchema must reject multi-field overlay without Params[\"field\"]")
	}
	if !strings.Contains(err.Error(), "PULSE_OVERLAY_PARAM_MISSING") &&
		!strings.Contains(err.Error(), "multiple fields") {
		t.Errorf("unexpected error shape: %v", err)
	}
}
