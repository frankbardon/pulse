package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

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

func TestFacetWithOverlays_HostByteIdenticalToNoOverlays(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// TestFacetWithOverlays_ChiSqVsPopMissingCategoryObservedZero runs E1-S2
// end to end: the subset filters out "d", so its facet listing is
// complete (truncated_at 0) without d, and OVERLAY_CHISQ_VS_POP compares
// d at observed 0 — R chisq.test(c(30,30,30,0), p = c(.3,.3,.3,.1))
// gives X-squared 10 on 3 df. discrete_top_k 3 still lists every subset
// value (no truncation), so the result is the same; discrete_top_k 2
// truncates the listing to {a, b} and the comparison stays over the
// listed values (X-squared 0 on 1 df), since c is cut, not absent.
func TestFacetWithOverlays_ChiSqVsPopMissingCategoryObservedZero(t *testing.T) {
	t.Parallel()
	svc, path := buildFacetOverlayCohort(t)
	run := func(topK int) types.OverlayLayer {
		t.Helper()
		res, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
			Cohort:       &types.Cohort{Filename: path},
			Fields:       []string{"category"},
			DiscreteTopK: topK,
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXCLUDE, Field: "category", Values: []string{"d"}},
			},
			Overlays: []types.OverlaySpec{{
				Kind:  types.OverlayKindChiSqVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
			}},
		})
		if err != nil {
			t.Fatalf("FacetSchema(top_k %d): %v", topK, err)
		}
		if len(res.Overlays) != 1 || res.Overlays[0].Summary == nil || res.Overlays[0].Summary.Statistic == nil {
			t.Fatalf("top_k %d: want one χ² layer with a statistic, got %+v", topK, res.Overlays)
		}
		return res.Overlays[0]
	}
	for _, tc := range []struct {
		topK     int
		wantStat float64
		wantDF   float64
	}{
		{0, 10, 3},
		{3, 10, 3},
		{2, 0, 1},
	} {
		layer := run(tc.topK)
		if got := *layer.Summary.Statistic; math.Abs(got-tc.wantStat) > 1e-9 {
			t.Errorf("top_k %d: statistic = %v, want %v", tc.topK, got, tc.wantStat)
		}
		if got := layer.Summary.Parameters["df"]; got != tc.wantDF {
			t.Errorf("top_k %d: df = %v, want %v", tc.topK, got, tc.wantDF)
		}
	}
}

// TestFacetWithOverlays_VsPopUnderReplaceLabels runs every discrete
// *_VS_POP kind with a caller-supplied replace-mode label binding on the
// host field (E1-S7). The host's values are relabelled before the
// overlays run, so the population must be keyed the same way: the
// figures equal the unlabelled run's once each labelled key is mapped
// back to its raw value, and no spurious PULSE_OVERLAY_REF_ZERO fires.
// The table leaves "d" unlabelled (a partly labelled host), and the
// subset drops "a" so CHISQ_VS_POP also compares a population category
// at observed 0.
func TestFacetWithOverlays_VsPopUnderReplaceLabels(t *testing.T) {
	t.Parallel()
	table := map[string]string{"a": "Alpha", "b": "Beta", "c": "Gamma"}
	raw := map[string]string{}
	for k, v := range table {
		raw[v] = k
	}
	run := func(t *testing.T, kind types.OverlayKind, labelled bool) *types.FacetResult {
		t.Helper()
		svc, path := buildFacetOverlayCohort(t)
		attachLabelExtensions(t, svc, map[string]processing.LabelTable{"cat_names": {Rows: table}})
		req := &types.FacetRequest{
			Cohort: &types.Cohort{Filename: path},
			Fields: []string{"category"},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXCLUDE, Field: "category", Values: []string{"a"}},
			},
			Overlays: []types.OverlaySpec{{
				Kind:  kind,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}},
			}},
		}
		if labelled {
			req.Labels = []*types.LabelBinding{{Field: "category", Table: "cat_names", Mode: types.LabelModeReplace}}
		}
		res, err := svc.FacetSchema(context.Background(), req)
		if err != nil {
			t.Fatalf("FacetSchema(%s, labelled %v): %v", kind, labelled, err)
		}
		if len(res.Overlays) != 1 {
			t.Fatalf("%s: want one layer, got %d", kind, len(res.Overlays))
		}
		for _, w := range res.Warnings {
			if w.Code == string(errors.PULSE_OVERLAY_REF_ZERO) {
				t.Errorf("%s (labelled %v): spurious %s: %s %v", kind, labelled, w.Code, w.Message, w.Details)
			}
		}
		return res
	}
	marshal := func(t *testing.T, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsPop, types.OverlayKindZScoreVsPop, types.OverlayKindChiSqVsPop} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			plain := run(t, kind, false).Overlays[0]
			labelled := run(t, kind, true).Overlays[0]
			if got, want := marshal(t, labelled.Summary), marshal(t, plain.Summary); got != want {
				t.Errorf("layer summary differs under replace labels:\n labelled: %s\n    plain: %s", got, want)
			}
			if plain.Payload.Series == nil {
				return
			}
			if labelled.Payload.Series == nil {
				t.Fatalf("labelled run lost the series payload")
			}
			want := map[string]string{}
			for _, e := range plain.Payload.Series.Entries {
				want[fmt.Sprint(e.Key[0])] = marshal(t, e.Summary)
			}
			if len(labelled.Payload.Series.Entries) != len(want) {
				t.Fatalf("entry count = %d, want %d", len(labelled.Payload.Series.Entries), len(want))
			}
			for _, e := range labelled.Payload.Series.Entries {
				key := fmt.Sprint(e.Key[0])
				if r, ok := raw[key]; ok {
					key = r
				} else if _, rawLeak := table[key]; rawLeak {
					t.Errorf("entry key %q is the raw value of a labelled category", key)
				}
				if got := marshal(t, e.Summary); got != want[key] {
					t.Errorf("entry %q (raw %q): summary %s, want %s", e.Key[0], key, got, want[key])
				}
			}
		})
	}
}
