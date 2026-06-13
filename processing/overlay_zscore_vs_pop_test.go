package processing

import (
	"math"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_ZSCORE_VS_POP runtime handler
// (processing/overlay_zscore_vs_pop.go) + the FACET-host dispatch
// (processing/overlay_facet_dispatch.go).
//
// E5-S3 scope: runtime handler only — the service-side wiring lands in
// E5-S6 and the descriptor-side validator lands in E5-S6 / E5-S7. The
// tests below exercise the math (categorical + numeric + ULP-checkable
// known-sd cohort) + the streaming-finalize-shape guarantee (the handler
// reads only POST-FINALIZE state) + the zero-sd_pop + missing-population
// paths.

// zscoreVsPopSpec builds a minimal OverlaySpec carrying the
// OVERLAY_ZSCORE_VS_POP kind + a GROUP scope + a populated Population
// reference. Mirrors indexVsPopSpec() in overlay_index_vs_pop_test.go.
func zscoreVsPopSpec() *types.OverlaySpec {
	return &types.OverlaySpec{
		Kind:  types.OverlayKindZScoreVsPop,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Population: &types.OverlayPopulationRef{Cohort: "population"},
		},
	}
}

// TestApplyZScoreVsPop_DiscreteKnownSDULP exercises the canonical
// categorical fast path against a synthetic population where the per-
// category frequency SD is known analytically — three categories with
// frequencies {0.5, 0.3, 0.2}. The population mean of those three
// frequencies is `(0.5+0.3+0.2)/3 = 1/3`. The population variance is
// `((0.5-1/3)² + (0.3-1/3)² + (0.2-1/3)²)/3` and sd_pop is its sqrt.
// The acceptance criterion is "z-values checkable to ULP" — this test
// computes the analytical sd_pop and compares the emitted z-scores
// against `(subset_freq - pop_freq) / sd_pop` with `1e-12` tolerance
// (well below ULP for the f64 magnitudes involved).
func TestApplyZScoreVsPop_DiscreteKnownSDULP(t *testing.T) {
	// Population: 100 records, frequencies {0.5, 0.3, 0.2} for
	// categories {a, b, c}. Counts: 50, 30, 20.
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 30},
		{Value: "c", Count: 20},
	}, 3, 100, 100, 0)

	// Subset: 100 records, frequencies {0.6, 0.3, 0.1} for the same
	// categories — `a` over-indexes vs population, `b` matches,
	// `c` under-indexes.
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 60},
		{Value: "b", Count: 30},
		{Value: "c", Count: 10},
	}, 3, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	// Compute the analytical sd_pop. Population variance over per-
	// category frequencies {0.5, 0.3, 0.2}:
	freqs := []float64{0.5, 0.3, 0.2}
	var mean float64
	for _, f := range freqs {
		mean += f
	}
	mean /= float64(len(freqs))
	var variance float64
	for _, f := range freqs {
		variance += (f - mean) * (f - mean)
	}
	variance /= float64(len(freqs))
	wantSd := math.Sqrt(variance)

	// Sanity: the resolver's accessor must report the same sd_pop.
	gotSd, ok := popView.DiscreteFrequencyStdev()
	if !ok {
		t.Fatal("DiscreteFrequencyStdev returned ok=false")
	}
	if math.Abs(gotSd-wantSd) > 1e-12 {
		t.Errorf("resolver sd_pop = %v, want %v (delta %v)", gotSd, wantSd, gotSd-wantSd)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyZScoreVsPop returned error: %v", err)
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
	if len(entries) != 3 {
		t.Fatalf("entries length = %d, want 3", len(entries))
	}

	// Per-entry verification. Host order: a (count=60), b (30), c (10) —
	// descending by count (FacetSchema sort).
	checks := []struct {
		key        string
		subsetFreq float64
		popFreq    float64
	}{
		{"a", 0.6, 0.5},
		{"b", 0.3, 0.3},
		{"c", 0.1, 0.2},
	}
	for i, c := range checks {
		entry := entries[i]
		if len(entry.Key) != 1 || entry.Key[0] != c.key {
			t.Errorf("entries[%d].Key = %v, want [%q]", i, entry.Key, c.key)
			continue
		}
		if entry.Summary.Statistic == nil {
			t.Errorf("entries[%d].Statistic is nil", i)
			continue
		}
		wantZ := (c.subsetFreq - c.popFreq) / wantSd
		gotZ := *entry.Summary.Statistic
		if math.Abs(gotZ-wantZ) > 1e-12 {
			t.Errorf("entries[%d].Statistic = %v, want %v (delta %v)", i, gotZ, wantZ, gotZ-wantZ)
		}
	}

	// Layer-level Summary: baseline=0 (z-score family centres ramps on
	// zero) + Min/Max/Count populated from present entries.
	if layer.Summary == nil {
		t.Fatal("layer.Summary is nil")
	}
	if layer.Summary.Baseline == nil || *layer.Summary.Baseline != 0.0 {
		t.Errorf("Summary.Baseline = %v, want 0.0", layer.Summary.Baseline)
	}
	if layer.Summary.Count == nil || *layer.Summary.Count != 3 {
		t.Errorf("Summary.Count = %v, want 3", layer.Summary.Count)
	}
}

// TestApplyZScoreVsPop_DiscreteAbsentValueNoWarning asserts that an
// absent population value (host carries "d" with count=5; pop has no
// "d") produces a well-defined z-score `(subset_freq - 0) / sd_pop`
// WITHOUT emitting PULSE_OVERLAY_REF_ZERO. Distinct from
// INDEX_VS_POP which divides by pop_freq and emits a warning when the
// value is absent — ZSCORE_VS_POP subtracts so the math is well-defined.
func TestApplyZScoreVsPop_DiscreteAbsentValueNoWarning(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 30},
		{Value: "c", Count: 20},
	}, 3, 100, 100, 0)

	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "d", Count: 100}, // never appeared in the population
	}, 1, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyZScoreVsPop returned error: %v", err)
	}
	// No warnings — the absent-population-value path is well-defined for
	// ZSCORE_VS_POP because subtraction by zero is well-defined.
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	entries := layer.Payload.Series.Entries
	if len(entries) != 1 {
		t.Fatalf("entries length = %d, want 1", len(entries))
	}
	if entries[0].Summary.Statistic == nil {
		t.Fatal("entries[0].Statistic is nil — expected a finite z-score")
	}
}

// TestApplyZScoreVsPop_DiscreteZeroSDPop exercises the E5-S3
// acceptance criterion: "on sd_pop == 0 emit warning code
// PULSE_OVERLAY_REF_ZERO and skip the entry". Population has a single
// category — DiscreteFrequencyStdev returns 0 — every per-value entry
// hits the zero-denominator arm.
func TestApplyZScoreVsPop_DiscreteZeroSDPop(t *testing.T) {
	// Population: single category, sd_pop = 0 (n=1 — Welford variance
	// is zero).
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 100},
	}, 1, 100, 100, 0)

	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	// Sanity: the resolver reports sd_pop == 0.
	sd, ok := popView.DiscreteFrequencyStdev()
	if !ok {
		t.Fatal("DiscreteFrequencyStdev returned ok=false")
	}
	if sd != 0 {
		t.Errorf("sd_pop = %v, want 0 (single-entry population)", sd)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyZScoreVsPop returned error: %v", err)
	}

	// One warning per affected entry (a, b — both routed through the
	// zero-denominator arm).
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings (one per host value), got %d", len(warnings))
	}
	for i, w := range warnings {
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("warnings[%d].Code = %s, want %s", i, w.Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
		}
	}

	entries := layer.Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("entries length = %d, want 2", len(entries))
	}
	// Both entries should have unset Statistic (skipped).
	for i, e := range entries {
		if e.Summary.Statistic != nil {
			t.Errorf("entries[%d].Statistic should be nil (skipped on sd_pop=0); got %v", i, *e.Summary.Statistic)
		}
	}
}

// TestApplyZScoreVsPop_DiscreteEmptyHost asserts that an empty discrete
// host (no per-value counts) emits an empty Entries slice without
// warning — same shape as INDEX_VS_POP's empty-host arm.
func TestApplyZScoreVsPop_DiscreteEmptyHost(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{}, 0, 0, 0, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyZScoreVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings on empty host, got %d", len(warnings))
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("expected empty Entries slice, got %+v", layer.Payload.Series)
	}
}

// TestApplyZScoreVsPop_NumericBasic exercises the numeric arm against a
// synthetic FacetResult host with a histogram and a population with a
// known Welford summary. Each bin's centre value is standardised
// against the population's mean / stddev.
func TestApplyZScoreVsPop_NumericBasic(t *testing.T) {
	// Host histogram: 4 bins over [0, 100], so bin centres are
	// 12.5 / 37.5 / 62.5 / 87.5.
	hostNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		Histogram: &types.FacetHistogram{
			Min:  0,
			Max:  100,
			Bins: []int64{25, 25, 25, 25},
		},
	}
	host := newFacetResultNumeric("value", hostNum, 100, 100, 0)

	// Population summary: mean=50, stddev=25.
	popNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, StdDev: 25, Min: 0, Max: 100,
	}
	popResult := newFacetResultNumeric("value", popNum, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "value")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["value"]
	layer, warnings, err := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyZScoreVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	entries := layer.Payload.Series.Entries
	if len(entries) != 4 {
		t.Fatalf("entries length = %d, want 4 (matching histogram bin count)", len(entries))
	}

	// Each bin's centre value standardised against pop mean/sd.
	wantCenters := []float64{12.5, 37.5, 62.5, 87.5}
	for i, c := range wantCenters {
		if entries[i].Summary.Statistic == nil {
			t.Errorf("entries[%d].Statistic is nil", i)
			continue
		}
		wantZ := (c - 50.0) / 25.0
		gotZ := *entries[i].Summary.Statistic
		if math.Abs(gotZ-wantZ) > 1e-12 {
			t.Errorf("entries[%d].Statistic = %v, want %v (bin centre %v, delta %v)", i, gotZ, wantZ, c, gotZ-wantZ)
		}
	}
}

// TestApplyZScoreVsPop_NumericZeroPopSD exercises the numeric arm
// zero-sd_pop path: when the population's StdDev is zero (constant
// population) the handler emits ONE layer-level
// PULSE_OVERLAY_REF_ZERO warning and zero entries.
func TestApplyZScoreVsPop_NumericZeroPopSD(t *testing.T) {
	hostNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		Histogram: &types.FacetHistogram{
			Min:  0,
			Max:  100,
			Bins: []int64{50, 50},
		},
	}
	host := newFacetResultNumeric("value", hostNum, 100, 100, 0)

	popNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, StdDev: 0, Min: 50, Max: 50,
	}
	popResult := newFacetResultNumeric("value", popNum, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "value")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["value"]
	layer, warnings, err := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyZScoreVsPop returned error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (layer-level zero-sd_pop), got %d", len(warnings))
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("expected empty Entries slice on zero-sd_pop, got %+v", layer.Payload.Series)
	}
}

// TestApplyZScoreVsPop_NumericNoHistogram asserts that a numeric host
// without a histogram emits an empty layer without warning — same
// shape as INDEX_VS_POP's numeric-no-histogram arm.
func TestApplyZScoreVsPop_NumericNoHistogram(t *testing.T) {
	hostNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, Min: 0, Max: 100,
		// No Histogram populated.
	}
	host := newFacetResultNumeric("value", hostNum, 100, 100, 0)

	popNum := &types.FacetNumeric{
		Count: 100, Sum: 5000, Mean: 50, StdDev: 25, Min: 0, Max: 100,
	}
	popResult := newFacetResultNumeric("value", popNum, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "value")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["value"]
	layer, warnings, err := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyZScoreVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings on missing host histogram, got %d", len(warnings))
	}
	if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != 0 {
		t.Errorf("expected empty Entries slice when host carries no histogram")
	}
}

// TestApplyOverlaysFacet_DispatchesZScoreVsPop verifies the FACET-host
// dispatch table routes OVERLAY_ZSCORE_VS_POP to the runtime handler.
func TestApplyOverlaysFacet_DispatchesZScoreVsPop(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
	}, 1, 50, 50, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 70},
		{Value: "b", Count: 30},
	}, 2, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	specs := []types.OverlaySpec{*zscoreVsPopSpec()}
	layers, _, err := ApplyOverlaysFacet(specs, host.Fields["category"], popView)
	if err != nil {
		t.Fatalf("ApplyOverlaysFacet returned error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers length = %d, want 1", len(layers))
	}
	if layers[0].Kind != types.OverlayKindZScoreVsPop {
		t.Errorf("layers[0].Kind = %s, want %s", layers[0].Kind, types.OverlayKindZScoreVsPop)
	}
}

// TestApplyZScoreVsPop_NilHost / NilPopulation pin the defense-in-depth
// rejection of nil host / population view (mirrors INDEX_VS_POP).
func TestApplyZScoreVsPop_NilHost(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)
	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}
	_, _, applyErr := applyZScoreVsPop(zscoreVsPopSpec(), nil, popView)
	if applyErr == nil {
		t.Fatal("expected coded error for nil host, got nil")
	}
	if !strings.Contains(applyErr.Error(), "FacetField host") {
		t.Errorf("error = %v, want message mentioning FacetField host", applyErr)
	}
}

func TestApplyZScoreVsPop_NilPopulation(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
	}, 1, 50, 50, 0)
	_, _, applyErr := applyZScoreVsPop(zscoreVsPopSpec(), host.Fields["category"], nil)
	if applyErr == nil {
		t.Fatal("expected coded error for nil population view, got nil")
	}
	if !strings.Contains(applyErr.Error(), "FacetPopulationView") {
		t.Errorf("error = %v, want message mentioning FacetPopulationView", applyErr)
	}
}

// TestApplyZScoreVsPop_StreamingVsBufferedByteIdentity pins the
// streaming finalize hook guarantee: running the handler against a
// "streamed" host (the standard FacetResult shape — the streaming
// Facet pass emits the same FacetField post-finalize) yields byte-
// identical output. The handler reads only POST-FINALIZE state so the
// streaming-vs-buffered byte-identity is structural (mirrors the
// INDEX_VS_POP test). Documents the invariant explicitly so future
// refactors do not silently introduce a streaming-only side channel.
func TestApplyZScoreVsPop_StreamingVsBufferedByteIdentity(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 60},
		{Value: "b", Count: 30},
		{Value: "c", Count: 10},
	}, 3, 100, 100, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 30},
		{Value: "c", Count: 20},
	}, 3, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layerA, _, _ := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)
	layerB, _, _ := applyZScoreVsPop(zscoreVsPopSpec(), hostField, popView)

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

// TestFacetPopulationView_DiscreteFrequencyStdev exercises the S1
// resolver's new accessor — the additive surface E5-S3 added to
// expose population frequency SD. ULP check against the analytical
// Welford-Pébaÿ recurrence over the per-category frequencies.
func TestFacetPopulationView_DiscreteFrequencyStdev(t *testing.T) {
	t.Run("multi-category", func(t *testing.T) {
		result := newFacetResultDiscrete("category", []types.FacetValueCount{
			{Value: "a", Count: 50},
			{Value: "b", Count: 30},
			{Value: "c", Count: 20},
		}, 3, 100, 100, 0)
		view, err := ResolveFacetPopulation(result, "category")
		if err != nil {
			t.Fatalf("ResolveFacetPopulation failed: %v", err)
		}
		sd, ok := view.DiscreteFrequencyStdev()
		if !ok {
			t.Fatal("DiscreteFrequencyStdev ok = false")
		}
		// Manual: frequencies = {0.5, 0.3, 0.2}, mean = 1/3.
		freqs := []float64{0.5, 0.3, 0.2}
		var mean float64
		for _, f := range freqs {
			mean += f
		}
		mean /= float64(len(freqs))
		var variance float64
		for _, f := range freqs {
			variance += (f - mean) * (f - mean)
		}
		variance /= float64(len(freqs))
		want := math.Sqrt(variance)
		if math.Abs(sd-want) > 1e-12 {
			t.Errorf("sd = %v, want %v (delta %v)", sd, want, sd-want)
		}
	})

	t.Run("single-category-zero-sd", func(t *testing.T) {
		result := newFacetResultDiscrete("category", []types.FacetValueCount{
			{Value: "only", Count: 100},
		}, 1, 100, 100, 0)
		view, err := ResolveFacetPopulation(result, "category")
		if err != nil {
			t.Fatalf("ResolveFacetPopulation failed: %v", err)
		}
		sd, ok := view.DiscreteFrequencyStdev()
		if !ok {
			t.Fatal("DiscreteFrequencyStdev ok = false")
		}
		if sd != 0 {
			t.Errorf("sd = %v, want 0 (single-category population)", sd)
		}
	})

	t.Run("empty-payload-not-discrete", func(t *testing.T) {
		// Build a numeric facet result; the discrete accessor must
		// return ok=false.
		result := newFacetResultNumeric("value", &types.FacetNumeric{
			Count: 10, Mean: 5, StdDev: 2,
		}, 10, 10, 0)
		view, err := ResolveFacetPopulation(result, "value")
		if err != nil {
			t.Fatalf("ResolveFacetPopulation failed: %v", err)
		}
		sd, ok := view.DiscreteFrequencyStdev()
		if ok {
			t.Errorf("DiscreteFrequencyStdev ok = true on numeric field; want false (sd=%v)", sd)
		}
	})
}
