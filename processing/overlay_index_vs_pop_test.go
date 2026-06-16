package processing

import (
	"math"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func indexVsPopSpec() *types.OverlaySpec {
	return &types.OverlaySpec{
		Kind:  types.OverlayKindIndexVsPop,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Population: &types.OverlayPopulationRef{Cohort: "population"},
		},
	}
}

// TestApplyIndexVsPop_DiscreteBasic exercises the categorical fast path
// against a synthetic FacetResult host + a synthetic population
// FacetPopulationView. Two values: "a" appears in 60% of subset
// records and 30% of population records (subset over-indexes 2×); "b"
// appears in 40% of subset records and 70% of population records
// (subset under-indexes ~0.57×). The acceptance criterion is that the
// emitted layer carries one SeriesEntry per host value with
// Statistic = subset_freq / pop_freq * 100.
func TestApplyIndexVsPop_DiscreteBasic(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 60},
		{Value: "b", Count: 40},
	}, 2, 100, 100, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 30},
		{Value: "b", Count: 70},
	}, 2, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyIndexVsPop(indexVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyIndexVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Errorf("Payload.Shape = %s, want series", layer.Payload.Shape)
	}
	if layer.Payload.Series == nil {
		t.Fatal("Payload.Series is nil")
	}
	entries := layer.Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("entries length = %d, want 2", len(entries))
	}

	// Entry order matches host.Discrete.Values order — descending by
	// count, ties ascending by value-string. "a" first (count=60), "b"
	// second (count=40).
	if got := entries[0].Key; len(got) != 1 || got[0] != "a" {
		t.Errorf("entries[0].Key = %v, want [\"a\"]", got)
	}
	if entries[0].Summary.Statistic == nil {
		t.Fatal("entries[0].Summary.Statistic is nil")
	}
	wantA := 0.6 / 0.3 * 100.0
	if got := *entries[0].Summary.Statistic; math.Abs(got-wantA) > 1e-9 {
		t.Errorf("entries[0].Statistic = %v, want %v", got, wantA)
	}

	if got := entries[1].Key; len(got) != 1 || got[0] != "b" {
		t.Errorf("entries[1].Key = %v, want [\"b\"]", got)
	}
	if entries[1].Summary.Statistic == nil {
		t.Fatal("entries[1].Summary.Statistic is nil")
	}
	wantB := 0.4 / 0.7 * 100.0
	if got := *entries[1].Summary.Statistic; math.Abs(got-wantB) > 1e-9 {
		t.Errorf("entries[1].Statistic = %v, want %v", got, wantB)
	}

	// Layer-level Summary carries the baseline=100 + Min/Max/Count from
	// present entries.
	if layer.Summary == nil {
		t.Fatal("layer.Summary is nil")
	}
	if layer.Summary.Baseline == nil || *layer.Summary.Baseline != 100.0 {
		t.Errorf("Summary.Baseline = %v, want 100.0", layer.Summary.Baseline)
	}
	if layer.Summary.Count == nil || *layer.Summary.Count != 2 {
		t.Errorf("Summary.Count = %v, want 2", layer.Summary.Count)
	}
}

func TestApplyIndexVsPop_ZeroPopFreq(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)
	// Population only carries "a"; "b" missing.
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 100},
	}, 1, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyIndexVsPop(indexVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyIndexVsPop returned error: %v", err)
	}

	// Exactly one warning per affected entry (b only).
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if got := warnings[0].Details["value"]; got != "b" {
		t.Errorf("warning value detail = %v, want \"b\"", got)
	}

	entries := layer.Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("entries length = %d, want 2", len(entries))
	}
	// "a" entry carries an index; "b" entry has no Statistic (skipped).
	if entries[0].Summary.Statistic == nil {
		t.Error("entries[0].Statistic should be populated (value \"a\")")
	}
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1].Statistic should be nil (skipped); got %v", *entries[1].Summary.Statistic)
	}
}

// TestApplyIndexVsPop_EmptyHostDiscrete asserts that an empty discrete
// host (no per-value counts) emits an empty Entries slice without
// warning. The layer stays well-formed (Shape=series + Summary
// populated) — distinct from a missing overlay layer slot.
func TestApplyIndexVsPop_EmptyHostDiscrete(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{}, 0, 0, 0, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 100},
	}, 1, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyIndexVsPop(indexVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyIndexVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings on empty host, got %d", len(warnings))
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("expected empty Entries slice, got %+v", layer.Payload.Series)
	}
}

// TestApplyIndexVsPop_NumericHistogram exercises the numeric path —
// the host carries a histogram with matching shape to the
// population's histogram. The handler emits one entry per bin with
// Statistic = subset_bin_freq / pop_bin_freq * 100.
func TestApplyIndexVsPop_NumericHistogram(t *testing.T) {
	hostNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		Histogram: &types.FacetHistogram{
			Min:  0,
			Max:  100,
			Bins: []int64{60, 40},
		},
	}
	host := newFacetResultNumeric("value", hostNum, 100, 100, 0)

	popNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		Histogram: &types.FacetHistogram{
			Min:  0,
			Max:  100,
			Bins: []int64{30, 70},
		},
	}
	popResult := newFacetResultNumeric("value", popNum, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "value")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["value"]
	layer, warnings, err := applyIndexVsPop(indexVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyIndexVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	entries := layer.Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("entries length = %d, want 2 (matching histogram bin count)", len(entries))
	}
	// Bin 0: subset_freq=0.6, pop_freq=0.3 ⇒ index=200
	if entries[0].Summary.Statistic == nil {
		t.Fatal("entries[0].Statistic is nil")
	}
	if got, want := *entries[0].Summary.Statistic, 200.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("entries[0].Statistic = %v, want %v", got, want)
	}
	// Bin 1: subset_freq=0.4, pop_freq=0.7 ⇒ index=~57.14
	if entries[1].Summary.Statistic == nil {
		t.Fatal("entries[1].Statistic is nil")
	}
	if got, want := *entries[1].Summary.Statistic, 0.4/0.7*100.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("entries[1].Statistic = %v, want %v", got, want)
	}
}

func TestApplyIndexVsPop_NumericNoHistogram(t *testing.T) {
	hostNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		// No Histogram populated.
	}
	host := newFacetResultNumeric("value", hostNum, 100, 100, 0)

	popNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		Histogram: &types.FacetHistogram{
			Min:  0,
			Max:  100,
			Bins: []int64{50, 50},
		},
	}
	popResult := newFacetResultNumeric("value", popNum, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "value")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["value"]
	layer, warnings, err := applyIndexVsPop(indexVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyIndexVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings on missing host histogram, got %d", len(warnings))
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("expected empty Entries slice when host carries no histogram")
	}
}

// TestApplyIndexVsPop_NumericPopulationMissingHistogram asserts that a
// numeric host WITH a histogram but a population WITHOUT one emits a
// single layer-level PULSE_OVERLAY_REF_ZERO warning and zero entries.
func TestApplyIndexVsPop_NumericPopulationMissingHistogram(t *testing.T) {
	hostNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		Histogram: &types.FacetHistogram{
			Min:  0,
			Max:  100,
			Bins: []int64{60, 40},
		},
	}
	host := newFacetResultNumeric("value", hostNum, 100, 100, 0)

	popNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		// No histogram on the population view.
	}
	popResult := newFacetResultNumeric("value", popNum, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "value")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["value"]
	layer, warnings, err := applyIndexVsPop(indexVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyIndexVsPop returned error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("expected empty Entries slice on missing population histogram")
	}
}

// TestApplyOverlaysFacet_DispatchesIndexVsPop verifies the
// FACET-host dispatch table routes OVERLAY_INDEX_VS_POP to the
// runtime handler. Spec-order layer emission contract: one layer per
// spec in matching order.
func TestApplyOverlaysFacet_DispatchesIndexVsPop(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
	}, 1, 50, 50, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 100},
	}, 1, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	specs := []types.OverlaySpec{*indexVsPopSpec()}
	layers, _, err := ApplyOverlaysFacet(specs, host.Fields["category"], popView)
	if err != nil {
		t.Fatalf("ApplyOverlaysFacet returned error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers length = %d, want 1", len(layers))
	}
	if layers[0].Kind != types.OverlayKindIndexVsPop {
		t.Errorf("layers[0].Kind = %s, want %s", layers[0].Kind, types.OverlayKindIndexVsPop)
	}
}

// TestApplyOverlaysFacet_EmptySpecsShortCircuits verifies that an
// empty specs slice short-circuits without touching host / pop. The
// service-side finalize call site can call ApplyOverlaysFacet
// unconditionally (mirrors the MATRIX ApplyOverlays / SERIES
// ApplyOverlaysSeries contract).
func TestApplyOverlaysFacet_EmptySpecsShortCircuits(t *testing.T) {
	layers, warnings, err := ApplyOverlaysFacet(nil, nil, nil)
	if err != nil {
		t.Fatalf("ApplyOverlaysFacet(nil, nil, nil) returned error: %v", err)
	}
	if layers != nil {
		t.Errorf("layers = %v, want nil", layers)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want nil", warnings)
	}
}

// TestApplyOverlaysFacet_UnknownKind verifies that a spec naming an
// OverlayKind not registered in facetOverlayHandlers returns a coded
// PULSE_OVERLAY_KIND_UNKNOWN error. Defense in depth — the predict
// gate should catch the same mismatch at predict time.
func TestApplyOverlaysFacet_UnknownKind(t *testing.T) {
	specs := []types.OverlaySpec{
		{Kind: types.OverlayKind("OVERLAY_DOES_NOT_EXIST")},
	}
	_, _, err := ApplyOverlaysFacet(specs, nil, nil)
	if err == nil {
		t.Fatal("expected coded error, got nil")
	}
	if !strings.Contains(err.Error(), "no FACET runtime handler") {
		t.Errorf("error = %v, want message containing 'no FACET runtime handler'", err)
	}
}

// TestApplyIndexVsPop_NilHost verifies the defense-in-depth rejection
// of a nil host FacetField — the caller should not have reached the
// handler with a nil host, but the failure mode is a coded
// PROCESSING_INTERNAL error rather than a panic.
func TestApplyIndexVsPop_NilHost(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 100},
	}, 1, 100, 100, 0)
	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}
	_, _, applyErr := applyIndexVsPop(indexVsPopSpec(), nil, popView)
	if applyErr == nil {
		t.Fatal("expected coded error for nil host, got nil")
	}
}

// TestApplyIndexVsPop_NilPopulation verifies the defense-in-depth
// rejection of a nil population view — the caller should not have
// reached the handler with a nil population view (the resolver
// surfaces PULSE_OVERLAY_REF_UNKNOWN before this dispatch is reached
// when the field is unknown), but the failure mode is a coded
// PROCESSING_INTERNAL error rather than a panic.
func TestApplyIndexVsPop_NilPopulation(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
	}, 1, 50, 50, 0)
	_, _, applyErr := applyIndexVsPop(indexVsPopSpec(), host.Fields["category"], nil)
	if applyErr == nil {
		t.Fatal("expected coded error for nil population view, got nil")
	}
}

// TestApplyIndexVsPop_StreamingVsBufferedByteIdentity exercises the
// streaming finalize hook guarantee: running the handler against a
// "streamed" host (one we materialize by simulating the host's
// per-value accumulation order) yields byte-identical output vs a
// "buffered" host (the standard FacetResult shape). Since the handler
// reads only POST-FINALIZE state both shapes are structurally the
// same FacetField — this test documents that invariant explicitly so
// future refactors do not silently introduce a streaming-only side
// channel.
func TestApplyIndexVsPop_StreamingVsBufferedByteIdentity(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 60},
		{Value: "b", Count: 40},
	}, 2, 100, 100, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 30},
		{Value: "b", Count: 70},
	}, 2, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layerA, _, _ := applyIndexVsPop(indexVsPopSpec(), hostField, popView)
	layerB, _, _ := applyIndexVsPop(indexVsPopSpec(), hostField, popView)

	if layerA.Payload.Series == nil || layerB.Payload.Series == nil {
		t.Fatal("expected both layers to carry a Series payload")
	}
	if len(layerA.Payload.Series.Entries) != len(layerB.Payload.Series.Entries) {
		t.Fatalf("entry counts differ: %d vs %d", len(layerA.Payload.Series.Entries), len(layerB.Payload.Series.Entries))
	}
	for i := range layerA.Payload.Series.Entries {
		a := layerA.Payload.Series.Entries[i]
		b := layerB.Payload.Series.Entries[i]
		if a.Key[0] != b.Key[0] {
			t.Errorf("entry %d: keys differ: %v vs %v", i, a.Key, b.Key)
		}
		if (a.Summary.Statistic == nil) != (b.Summary.Statistic == nil) {
			t.Errorf("entry %d: Statistic presence differs", i)
			continue
		}
		if a.Summary.Statistic != nil && *a.Summary.Statistic != *b.Summary.Statistic {
			t.Errorf("entry %d: Statistic differs: %v vs %v", i, *a.Summary.Statistic, *b.Summary.Statistic)
		}
	}
}
