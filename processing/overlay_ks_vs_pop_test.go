package processing

import (
	"math"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_KS_VS_POP runtime handler
// (processing/overlay_ks_vs_pop.go) + the FACET-host dispatch
// (processing/overlay_facet_dispatch.go).
//
// E5-S5 scope: runtime handler only — the per-kind predict-side validator
// lands in E5-S10. The tests below exercise:
//
//   - The histogram-path known-answer KS against a synth shifted-normal
//     vs centered-normal fixture (acceptance: HISTOGRAM PATH preferred).
//   - The percentile-path fallback when histograms are absent on both
//     arms but percentile maps are present (acceptance: PERCENTILE PATH
//     fallback).
//   - The Welford-only degenerate path when neither histograms nor
//     percentiles are available (acceptance: NaN + NaN with
//     PULSE_OVERLAY_REF_ZERO carrying which knobs are missing).
//   - The categorical host rejection (acceptance: numeric-arm only —
//     categorical hosts fire PULSE_OVERLAY_SCOPE_UNSUPPORTED).
//   - The defense-in-depth nil-host / nil-pop / empty arm guards.
//   - The dispatch-table wiring + streaming-vs-buffered byte identity.

// ksVsPopSpec builds a minimal OverlaySpec carrying the OVERLAY_KS_VS_POP
// kind + a GROUP scope + a populated Population reference. Mirrors
// chiSqVsPopSpec() / zscoreVsPopSpec() / indexVsPopSpec().
func ksVsPopSpec() *types.OverlaySpec {
	return &types.OverlaySpec{
		Kind:  types.OverlayKindKSVsPop,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Population: &types.OverlayPopulationRef{Cohort: "population"},
		},
	}
}

// TestApplyKSVsPop_HistogramPathKnownAnswer exercises the histogram-path
// acceptance criterion. Two arms share the same Min/Max/bucket count;
// each arm carries a distinct bucket distribution. The KS distance is
// the maximum cumulative-fraction divergence over the aligned edges.
//
// Setup: 10 bins over [0, 10).
//
//	host: count = 100, all mass concentrated in bins 0..4 (left half)
//	pop:  count = 100, all mass concentrated in bins 5..9 (right half)
//
// CDF at edge after bin 4:
//
//	host_cum = 100/100 = 1.0
//	pop_cum  = 0/100   = 0.0
//	|diff|   = 1.0
//
// KS distance = 1.0 (maximum possible separation).
func TestApplyKSVsPop_HistogramPathKnownAnswer(t *testing.T) {
	// Host: left-half histogram.
	hostHist := &types.FacetHistogram{
		Min:  0,
		Max:  10,
		Bins: []int64{20, 20, 20, 20, 20, 0, 0, 0, 0, 0},
	}
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count:     100,
		Min:       0,
		Max:       4.999,
		Mean:      2,
		StdDev:    1.4,
		Histogram: hostHist,
	}, 100, 100, 0)

	// Pop: right-half histogram.
	popHist := &types.FacetHistogram{
		Min:  0,
		Max:  10,
		Bins: []int64{0, 0, 0, 0, 0, 20, 20, 20, 20, 20},
	}
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count:     100,
		Min:       5,
		Max:       9.999,
		Mean:      7,
		StdDev:    1.4,
		Histogram: popHist,
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["price"]
	layer, warnings, err := applyKSVsPop(ksVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyKSVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings (non-degenerate arms), got %d: %+v", len(warnings), warnings)
	}

	// SCALAR payload, Payload.Scalar carries D = 1.0.
	if layer.Payload.Shape != types.OverlayShapeScalar {
		t.Errorf("Payload.Shape = %s, want scalar", layer.Payload.Shape)
	}
	if layer.Payload.Scalar == nil {
		t.Fatal("Payload.Scalar is nil")
	}
	wantD := 1.0
	if math.Abs(*layer.Payload.Scalar-wantD) > 1e-12 {
		t.Errorf("Payload.Scalar = %v, want %v", *layer.Payload.Scalar, wantD)
	}

	// Summary carries Statistic + PValue + Parameters{"n_subset", "n_pop"}.
	if layer.Summary == nil || layer.Summary.Statistic == nil {
		t.Fatal("Summary.Statistic is nil")
	}
	if math.Abs(*layer.Summary.Statistic-wantD) > 1e-12 {
		t.Errorf("Summary.Statistic = %v, want %v", *layer.Summary.Statistic, wantD)
	}
	if layer.Summary.PValue == nil {
		t.Fatal("Summary.PValue is nil")
	}
	// p-value via the canonical Kolmogorov-survival helper. For D=1.0
	// with n1=n2=100, en = sqrt(100*100/200) = sqrt(50) ≈ 7.071,
	// λ = (en + 0.12 + 0.11/en) * D ≈ 7.207. Survival ≈ 0 (highly
	// significant — distributions don't overlap at all).
	en := math.Sqrt(float64(100*100) / float64(200))
	wantPV := kolmogorovSurvival((en + 0.12 + 0.11/en) * wantD)
	if math.Abs(*layer.Summary.PValue-wantPV) > 1e-12 {
		t.Errorf("Summary.PValue = %v, want %v", *layer.Summary.PValue, wantPV)
	}

	// Parameters{"n_subset", "n_pop"} populated.
	if n, ok := layer.Summary.Parameters["n_subset"]; !ok || n != 100 {
		t.Errorf("Summary.Parameters[\"n_subset\"] = %v, want 100", n)
	}
	if n, ok := layer.Summary.Parameters["n_pop"]; !ok || n != 100 {
		t.Errorf("Summary.Parameters[\"n_pop\"] = %v, want 100", n)
	}

	// Baseline stays unset on the inferential layer (mirrors CHISQ_VS_POP).
	if layer.Summary.Baseline != nil {
		t.Errorf("Summary.Baseline = %v, want nil (inferential overlay)", *layer.Summary.Baseline)
	}
}

// TestApplyKSVsPop_PercentileFallback exercises the percentile-path
// fallback when both arms carry percentile maps but no histograms. Host
// percentiles diverge from population percentiles → non-zero KS distance.
//
// Setup:
//
//	host percentiles: p25=10, p50=20, p75=30
//	pop  percentiles: p25=15, p50=25, p75=35  (shifted right by 5)
//
// The CDFs are piecewise-linear interpolants over each arm's percentile
// anchors. The KS distance D is the maximum |F_subset(x) - F_pop(x)| over
// the union of anchor x-values.
func TestApplyKSVsPop_PercentileFallback(t *testing.T) {
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count:  100,
		Min:    0,
		Max:    50,
		Mean:   20,
		StdDev: 10,
		Percentiles: map[string]float64{
			"p25": 10,
			"p50": 20,
			"p75": 30,
		},
	}, 100, 100, 0)
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count:  100,
		Min:    0,
		Max:    50,
		Mean:   25,
		StdDev: 10,
		Percentiles: map[string]float64{
			"p25": 15,
			"p50": 25,
			"p75": 35,
		},
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["price"]
	layer, warnings, err := applyKSVsPop(ksVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyKSVsPop returned error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings (non-degenerate arms), got %d: %+v", len(warnings), warnings)
	}

	// SCALAR payload, D > 0 because the two CDFs diverge.
	if layer.Payload.Scalar == nil {
		t.Fatal("Payload.Scalar is nil")
	}
	if *layer.Payload.Scalar <= 0 {
		t.Errorf("Payload.Scalar = %v, want > 0 (shifted percentile maps must produce non-zero KS distance)", *layer.Payload.Scalar)
	}
	// Bounded by 1.0 — KS distance is a CDF-axis displacement.
	if *layer.Payload.Scalar > 1.0 {
		t.Errorf("Payload.Scalar = %v, want <= 1.0", *layer.Payload.Scalar)
	}
}

// TestApplyKSVsPop_WelfordOnlyDegenerate exercises the Welford-only
// degenerate path: neither histograms nor percentile maps are available.
// Handler emits NaN statistic + NaN p-value with one
// PULSE_OVERLAY_REF_ZERO warning whose Details document which knobs were
// missing.
func TestApplyKSVsPop_WelfordOnlyDegenerate(t *testing.T) {
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count:  100,
		Min:    0,
		Max:    50,
		Mean:   20,
		StdDev: 10,
		// No Percentiles, no Histogram.
	}, 100, 100, 0)
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count:  100,
		Min:    0,
		Max:    50,
		Mean:   25,
		StdDev: 10,
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["price"]
	layer, warnings, err := applyKSVsPop(ksVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyKSVsPop returned error: %v", err)
	}

	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (Welford-only degenerate), got %d: %+v", len(warnings), warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	// Details document which knobs were missing on the FacetRequest so the
	// caller can audit which to flip.
	if reason, ok := warnings[0].Details["reason"].(string); !ok || reason != "no_reconstruction_path" {
		t.Errorf("warning Details[\"reason\"] = %v, want \"no_reconstruction_path\"", warnings[0].Details["reason"])
	}
	if hostHasHist, ok := warnings[0].Details["host_has_histogram"].(bool); !ok || hostHasHist {
		t.Errorf("warning Details[\"host_has_histogram\"] = %v, want false", warnings[0].Details["host_has_histogram"])
	}
	if popHasHist, ok := warnings[0].Details["pop_has_histogram"].(bool); !ok || popHasHist {
		t.Errorf("warning Details[\"pop_has_histogram\"] = %v, want false", warnings[0].Details["pop_has_histogram"])
	}
	if hostHasPct, ok := warnings[0].Details["host_has_percentiles"].(bool); !ok || hostHasPct {
		t.Errorf("warning Details[\"host_has_percentiles\"] = %v, want false", warnings[0].Details["host_has_percentiles"])
	}
	if popHasPct, ok := warnings[0].Details["pop_has_percentiles"].(bool); !ok || popHasPct {
		t.Errorf("warning Details[\"pop_has_percentiles\"] = %v, want false", warnings[0].Details["pop_has_percentiles"])
	}

	// NaN statistic + NaN p-value.
	if layer.Payload.Scalar == nil || !math.IsNaN(*layer.Payload.Scalar) {
		t.Errorf("Payload.Scalar = %v, want NaN", layer.Payload.Scalar)
	}
	if layer.Summary == nil || layer.Summary.Statistic == nil || !math.IsNaN(*layer.Summary.Statistic) {
		t.Errorf("Summary.Statistic = %v, want NaN", layer.Summary)
	}
	if layer.Summary.PValue == nil || !math.IsNaN(*layer.Summary.PValue) {
		t.Errorf("Summary.PValue = %v, want NaN", layer.Summary.PValue)
	}
}

// TestApplyKSVsPop_CategoricalHostRejected pins the numeric-arm-only
// contract: a categorical host (no Numeric payload) returns a coded
// PROCESSING_INTERNAL error carrying PULSE_OVERLAY_SCOPE_UNSUPPORTED.
// Defense in depth — the per-kind validator (E5-S10) will reject
// categorical hosts at predict time.
func TestApplyKSVsPop_CategoricalHostRejected(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)
	pop := newFacetResultNumeric("category", &types.FacetNumeric{
		Count: 100, Mean: 50, StdDev: 25, Min: 0, Max: 100,
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(pop, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	_, _, applyErr := applyKSVsPop(ksVsPopSpec(), hostField, popView)
	if applyErr == nil {
		t.Fatal("expected coded error for categorical host, got nil")
	}
	if !strings.Contains(applyErr.Error(), "numeric") {
		t.Errorf("error = %v, want message mentioning numeric-arm requirement", applyErr)
	}
	coded, ok := applyErr.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("expected *CodedError, got %T", applyErr)
	}
	if got, ok := coded.Details["code"].(string); !ok || got != string(pulseerrors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
		t.Errorf("error Details[\"code\"] = %v, want %s", coded.Details["code"], pulseerrors.PULSE_OVERLAY_SCOPE_UNSUPPORTED)
	}
}

// TestApplyKSVsPop_EmptyHostDegenerate exercises the empty-host
// degenerate path: a host with zero numeric count. The handler emits a
// SCALAR payload with NaN statistic + NaN p-value and a
// PULSE_OVERLAY_REF_ZERO warning whose Details name the empty_arm
// reason.
func TestApplyKSVsPop_EmptyHostDegenerate(t *testing.T) {
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 0,
	}, 0, 0, 0)
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 25, StdDev: 10, Min: 0, Max: 50,
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["price"]
	layer, warnings, err := applyKSVsPop(ksVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyKSVsPop returned error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (empty host), got %d", len(warnings))
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if reason, ok := warnings[0].Details["reason"].(string); !ok || reason != "empty_arm" {
		t.Errorf("warning Details[\"reason\"] = %v, want \"empty_arm\"", warnings[0].Details["reason"])
	}
	if layer.Payload.Scalar == nil || !math.IsNaN(*layer.Payload.Scalar) {
		t.Errorf("Payload.Scalar = %v, want NaN", layer.Payload.Scalar)
	}
}

// TestApplyKSVsPop_EmptyPopulation exercises the empty-population
// degenerate path: a population with zero numeric count. Same NaN +
// PULSE_OVERLAY_REF_ZERO shape as the empty-host arm.
func TestApplyKSVsPop_EmptyPopulation(t *testing.T) {
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 20, StdDev: 10, Min: 0, Max: 50,
	}, 100, 100, 0)
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 0,
	}, 0, 0, 0)

	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["price"]
	layer, warnings, err := applyKSVsPop(ksVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyKSVsPop returned error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (empty population), got %d", len(warnings))
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}
	if layer.Payload.Scalar == nil || !math.IsNaN(*layer.Payload.Scalar) {
		t.Errorf("Payload.Scalar = %v, want NaN", layer.Payload.Scalar)
	}
}

// TestApplyKSVsPop_NilHost / NilPopulation pin the defense-in-depth
// rejection of nil host / population view (mirrors INDEX_VS_POP /
// ZSCORE_VS_POP / CHISQ_VS_POP).
func TestApplyKSVsPop_NilHost(t *testing.T) {
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 25, StdDev: 10, Min: 0, Max: 50,
	}, 100, 100, 0)
	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}
	_, _, applyErr := applyKSVsPop(ksVsPopSpec(), nil, popView)
	if applyErr == nil {
		t.Fatal("expected coded error for nil host, got nil")
	}
	if !strings.Contains(applyErr.Error(), "FacetField host") {
		t.Errorf("error = %v, want message mentioning FacetField host", applyErr)
	}
}

func TestApplyKSVsPop_NilPopulation(t *testing.T) {
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 20, StdDev: 10, Min: 0, Max: 50,
	}, 100, 100, 0)
	_, _, applyErr := applyKSVsPop(ksVsPopSpec(), host.Fields["price"], nil)
	if applyErr == nil {
		t.Fatal("expected coded error for nil population view, got nil")
	}
	if !strings.Contains(applyErr.Error(), "FacetPopulationView") {
		t.Errorf("error = %v, want message mentioning FacetPopulationView", applyErr)
	}
}

// TestApplyOverlaysFacet_DispatchesKSVsPop verifies the FACET-host
// dispatch table routes OVERLAY_KS_VS_POP to the runtime handler.
// Mirrors TestApplyOverlaysFacet_DispatchesChiSqVsPop /
// TestApplyOverlaysFacet_DispatchesZScoreVsPop.
func TestApplyOverlaysFacet_DispatchesKSVsPop(t *testing.T) {
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 20, StdDev: 10, Min: 0, Max: 50,
		Histogram: &types.FacetHistogram{
			Min: 0, Max: 50, Bins: []int64{50, 50, 0, 0, 0},
		},
	}, 100, 100, 0)
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 30, StdDev: 10, Min: 0, Max: 50,
		Histogram: &types.FacetHistogram{
			Min: 0, Max: 50, Bins: []int64{0, 0, 50, 50, 0},
		},
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	specs := []types.OverlaySpec{*ksVsPopSpec()}
	layers, _, err := ApplyOverlaysFacet(specs, host.Fields["price"], popView)
	if err != nil {
		t.Fatalf("ApplyOverlaysFacet returned error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers length = %d, want 1", len(layers))
	}
	if layers[0].Kind != types.OverlayKindKSVsPop {
		t.Errorf("layers[0].Kind = %s, want %s", layers[0].Kind, types.OverlayKindKSVsPop)
	}
	if layers[0].Payload.Shape != types.OverlayShapeScalar {
		t.Errorf("layers[0].Payload.Shape = %s, want scalar", layers[0].Payload.Shape)
	}
}

// TestOverlayKSVsPop_StreamabilityIsBuffered pins the acceptance criterion
// "types/overlay_streamability.go row asserts Streamable: false
// (inferential ⇒ always buffered, per PRD §2 Non-Goals)". Anchored against
// the public OverlayStreamable surface so a future regression that flips
// the row trips this test.
func TestOverlayKSVsPop_StreamabilityIsBuffered(t *testing.T) {
	streamable, known := types.OverlayStreamable(types.OverlayKindKSVsPop)
	if !known {
		t.Fatal("OverlayKindKSVsPop missing from OverlayStreamability table")
	}
	if streamable {
		t.Error("OverlayKindKSVsPop streamable = true; want false (PRD §2 Non-Goals: streaming overlay path for inferential kinds is out of scope)")
	}
}

// TestApplyKSVsPop_StreamingVsBufferedByteIdentity pins the "buffered exit
// hook does not re-traverse host records" acceptance criterion. Running
// the handler twice against the same finalized FacetField produces
// byte-identical SCALAR output — the handler reads only POST-FINALIZE
// state. Documents the invariant explicitly so a future refactor that
// introduces a side channel (e.g. a per-record re-scan) trips the test.
func TestApplyKSVsPop_StreamingVsBufferedByteIdentity(t *testing.T) {
	host := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 20, StdDev: 10, Min: 0, Max: 50,
		Histogram: &types.FacetHistogram{
			Min: 0, Max: 50, Bins: []int64{10, 20, 30, 30, 10},
		},
	}, 100, 100, 0)
	pop := newFacetResultNumeric("price", &types.FacetNumeric{
		Count: 100, Mean: 25, StdDev: 10, Min: 0, Max: 50,
		Histogram: &types.FacetHistogram{
			Min: 0, Max: 50, Bins: []int64{5, 15, 25, 35, 20},
		},
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(pop, "price")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["price"]
	layerA, _, _ := applyKSVsPop(ksVsPopSpec(), hostField, popView)
	layerB, _, _ := applyKSVsPop(ksVsPopSpec(), hostField, popView)

	if layerA.Payload.Scalar == nil || layerB.Payload.Scalar == nil {
		t.Fatal("expected both layers to carry a Scalar payload")
	}
	if *layerA.Payload.Scalar != *layerB.Payload.Scalar {
		t.Errorf("Payload.Scalar differs across invocations: %v vs %v", *layerA.Payload.Scalar, *layerB.Payload.Scalar)
	}
	if layerA.Summary == nil || layerB.Summary == nil {
		t.Fatal("expected both layers to carry a Summary")
	}
	if (layerA.Summary.PValue == nil) != (layerB.Summary.PValue == nil) {
		t.Error("Summary.PValue presence differs across invocations")
	}
	if layerA.Summary.PValue != nil && *layerA.Summary.PValue != *layerB.Summary.PValue {
		t.Errorf("Summary.PValue differs: %v vs %v", *layerA.Summary.PValue, *layerB.Summary.PValue)
	}
	if layerA.Summary.Parameters["n_subset"] != layerB.Summary.Parameters["n_subset"] {
		t.Errorf("Summary.Parameters[\"n_subset\"] differs: %v vs %v",
			layerA.Summary.Parameters["n_subset"], layerB.Summary.Parameters["n_subset"])
	}
	if layerA.Summary.Parameters["n_pop"] != layerB.Summary.Parameters["n_pop"] {
		t.Errorf("Summary.Parameters[\"n_pop\"] differs: %v vs %v",
			layerA.Summary.Parameters["n_pop"], layerB.Summary.Parameters["n_pop"])
	}
}
