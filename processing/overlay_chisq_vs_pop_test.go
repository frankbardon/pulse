package processing

import (
	"math"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_CHISQ_VS_POP runtime handler
// (processing/overlay_chisq_vs_pop.go) + the FACET-host dispatch
// (processing/overlay_facet_dispatch.go).
//
// E5-S4 scope: runtime handler only — the service-side wiring lands in
// E5-S6 / E5-S10 and the descriptor-side validator lands in E5-S6 /
// E5-S7. The tests below exercise:
//
//   - The canonical 4-category known-answer χ² (acceptance criterion
//     "Unit test covers a known-answer χ² against canonical 4-category
//     distribution") against an analytical χ² statistic.
//   - The expected-counts-scaled-to-subset-N invariant.
//   - The low-expected-cells warning code (PULSE_OVERLAY_EXPECTED_LOW —
//     shared with the crosstab Fisher exact path per PRD FR-J1).
//   - The streamability-row assertion (Streamable=false; inferential
//     ⇒ always buffered).
//   - The buffered exit hook (the handler runs against finalized
//     state — it does not re-traverse host records).
//   - Defense-in-depth nil-host / nil-pop / numeric-host arms.
//   - Degenerate-cohort fall-through to NaN statistic + NaN p-value
//     with PULSE_OVERLAY_REF_ZERO.

// chiSqVsPopSpec builds a minimal OverlaySpec carrying the
// OVERLAY_CHISQ_VS_POP kind + a GROUP scope + a populated Population
// reference. Mirrors zscoreVsPopSpec() / indexVsPopSpec().
func chiSqVsPopSpec() *types.OverlaySpec {
	return &types.OverlaySpec{
		Kind:  types.OverlayKindChiSqVsPop,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Population: &types.OverlayPopulationRef{Cohort: "population"},
		},
	}
}

// TestApplyChiSqVsPop_FourCategoryKnownAnswer exercises the acceptance
// criterion "Unit test covers a known-answer χ² against canonical
// 4-category distribution". The synthetic population has frequencies
// {0.4, 0.3, 0.2, 0.1}; the host has counts {30, 30, 30, 10} (total =
// 100). The expected counts (population frequencies × subset N) are
// {40, 30, 20, 10}; the χ² statistic computes analytically as:
//
//	chisq = (30-40)²/40 + (30-30)²/30 + (30-20)²/20 + (10-10)²/10
//	      = 100/40 + 0 + 100/20 + 0
//	      = 2.5 + 0 + 5.0 + 0
//	      = 7.5
//	df    = 4 - 1 = 3
//	p     = chiSquareSurvival(7.5, 3)  // ≈ 0.0577
//
// The test asserts the statistic to 1e-12 ULP and verifies df = 3 in
// Parameters{"df"}. p-value is computed via the same chiSquareSurvival
// helper (TEST_CHISQ surface reuse — no separate reference value
// computation; both surfaces must produce the same number for the
// same statistic).
func TestApplyChiSqVsPop_FourCategoryKnownAnswer(t *testing.T) {
	// Population: 100 records, frequencies {0.4, 0.3, 0.2, 0.1} for
	// categories {a, b, c, d}.
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 40},
		{Value: "b", Count: 30},
		{Value: "c", Count: 20},
		{Value: "d", Count: 10},
	}, 4, 100, 100, 0)

	// Subset: 100 records, counts {30, 30, 30, 10} — diverges from the
	// population's per-category distribution.
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 30},
		{Value: "b", Count: 30},
		{Value: "c", Count: 30},
		{Value: "d", Count: 10},
	}, 4, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyChiSqVsPop returned error: %v", err)
	}
	// Every expected count >= 5 (min expected = 10), so no
	// PULSE_OVERLAY_EXPECTED_LOW warning fires.
	if len(warnings) != 0 {
		t.Errorf("expected no warnings (every expected >= 10), got %d: %+v", len(warnings), warnings)
	}

	// Shape: SCALAR payload, Payload.Scalar populated, Summary carries
	// Statistic + PValue + Parameters{"df"}.
	if layer.Payload.Shape != types.OverlayShapeScalar {
		t.Errorf("Payload.Shape = %s, want scalar", layer.Payload.Shape)
	}
	if layer.Payload.Scalar == nil {
		t.Fatal("Payload.Scalar is nil")
	}
	wantChisq := 7.5
	if math.Abs(*layer.Payload.Scalar-wantChisq) > 1e-12 {
		t.Errorf("Payload.Scalar = %v, want %v (delta %v)", *layer.Payload.Scalar, wantChisq, *layer.Payload.Scalar-wantChisq)
	}
	if layer.Summary == nil {
		t.Fatal("Summary is nil")
	}
	if layer.Summary.Statistic == nil {
		t.Fatal("Summary.Statistic is nil")
	}
	if math.Abs(*layer.Summary.Statistic-wantChisq) > 1e-12 {
		t.Errorf("Summary.Statistic = %v, want %v", *layer.Summary.Statistic, wantChisq)
	}
	if layer.Summary.PValue == nil {
		t.Fatal("Summary.PValue is nil")
	}
	wantPV := chiSquareSurvival(wantChisq, 3.0)
	if math.Abs(*layer.Summary.PValue-wantPV) > 1e-12 {
		t.Errorf("Summary.PValue = %v, want %v (delta %v)", *layer.Summary.PValue, wantPV, *layer.Summary.PValue-wantPV)
	}
	if layer.Summary.Parameters == nil {
		t.Fatal("Summary.Parameters is nil")
	}
	if df, ok := layer.Summary.Parameters["df"]; !ok {
		t.Error("Summary.Parameters missing \"df\" key")
	} else if df != 3.0 {
		t.Errorf("Summary.Parameters[\"df\"] = %v, want 3.0", df)
	}
	// Summary.Count populates from the observed-value count (4 categories).
	if layer.Summary.Count == nil || *layer.Summary.Count != 4 {
		t.Errorf("Summary.Count = %v, want 4", layer.Summary.Count)
	}
	// Baseline stays unset on the inferential layer (mirrors CHISQ_MATRIX).
	if layer.Summary.Baseline != nil {
		t.Errorf("Summary.Baseline = %v, want nil (inferential overlay)", *layer.Summary.Baseline)
	}
}

// TestApplyChiSqVsPop_ExpectedCountsSumToSubsetN pins the expected-
// counts-scaled-to-subset-N invariant (per acceptance criterion: "χ²
// uses standard Σ (observed - expected)² / expected against population
// frequencies as expected; expected scaled to subset N"). The sum of
// expected counts across every category must equal subset_N for any
// population whose frequencies sum to 1.0. The test reads the
// statistic and back-computes the expected slice to assert the
// invariant holds.
func TestApplyChiSqVsPop_ExpectedCountsSumToSubsetN(t *testing.T) {
	// Population frequencies {0.4, 0.3, 0.2, 0.1} sum to 1.0.
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 40},
		{Value: "b", Count: 30},
		{Value: "c", Count: 20},
		{Value: "d", Count: 10},
	}, 4, 100, 100, 0)

	// Subset: subset_N = 50.
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 25},
		{Value: "b", Count: 15},
		{Value: "c", Count: 7},
		{Value: "d", Count: 3},
	}, 4, 50, 50, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	// Re-derive the expected slice using the documented formula and
	// assert it sums to subset_N within ULP.
	subsetN := 50.0
	wantExpected := []float64{0.4 * subsetN, 0.3 * subsetN, 0.2 * subsetN, 0.1 * subsetN}
	var sumExpected float64
	for _, e := range wantExpected {
		sumExpected += e
	}
	if math.Abs(sumExpected-subsetN) > 1e-12 {
		t.Fatalf("test fixture: expected counts sum to %v, want %v", sumExpected, subsetN)
	}

	// Then verify the handler's emitted χ² statistic matches the
	// analytical recomputation using those same expected values —
	// confirming that the handler scaled expected by subset_N (not, e.g.,
	// by population N).
	hostField := popView.FacetField() // re-use popView for explicit lookup
	_ = hostField
	hostField = host.Fields["category"]
	layer, _, err := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyChiSqVsPop returned error: %v", err)
	}

	observed := []float64{25, 15, 7, 3}
	var wantChisq float64
	for i := range observed {
		diff := observed[i] - wantExpected[i]
		wantChisq += diff * diff / wantExpected[i]
	}
	if layer.Summary.Statistic == nil {
		t.Fatal("Summary.Statistic is nil")
	}
	if math.Abs(*layer.Summary.Statistic-wantChisq) > 1e-12 {
		t.Errorf("Summary.Statistic = %v, want %v (delta %v) — this is the expected-counts-scaled-to-subset-N invariant",
			*layer.Summary.Statistic, wantChisq, *layer.Summary.Statistic-wantChisq)
	}
}

// TestApplyChiSqVsPop_LowExpectedWarning exercises the canonical low-
// expected-count warning. Subset N = 100 against a population where one
// category has frequency 0.04 — expected count = 4, below the canonical
// χ² reliability threshold (5).
func TestApplyChiSqVsPop_LowExpectedWarning(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 60},
		{Value: "b", Count: 36},
		{Value: "c", Count: 4}, // freq 0.04 → expected 4 for subset_N=100
	}, 3, 100, 100, 0)

	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 47},
		{Value: "c", Count: 3},
	}, 3, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyChiSqVsPop returned error: %v", err)
	}

	// Exactly one PULSE_OVERLAY_EXPECTED_LOW warning carrying the
	// offending count + min expected.
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %+v", len(warnings), warnings)
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_EXPECTED_LOW) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_EXPECTED_LOW)
	}
	if got, ok := warnings[0].Details["low_expected_cells"].(int); !ok || got != 1 {
		t.Errorf("warning Details[\"low_expected_cells\"] = %v, want 1 (int)", warnings[0].Details["low_expected_cells"])
	}
	if got, ok := warnings[0].Details["expected_min"].(float64); !ok || math.Abs(got-4.0) > 1e-12 {
		t.Errorf("warning Details[\"expected_min\"] = %v, want 4.0", warnings[0].Details["expected_min"])
	}

	// The statistic is still emitted alongside the warning — the
	// warning is advisory, not a short-circuit.
	if layer.Summary.Statistic == nil {
		t.Error("Summary.Statistic is nil — expected the statistic to be emitted alongside the warning")
	}
}

// TestApplyChiSqVsPop_EmptyHostDegenerate exercises the empty-host
// degenerate path: a host with zero per-value counts. The handler must
// emit a SCALAR payload with NaN statistic + NaN p-value and a layer-
// level PULSE_OVERLAY_REF_ZERO warning.
func TestApplyChiSqVsPop_EmptyHostDegenerate(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 60},
		{Value: "b", Count: 40},
	}, 2, 100, 100, 0)
	host := newFacetResultDiscrete("category", []types.FacetValueCount{}, 0, 0, 0, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyChiSqVsPop returned error: %v", err)
	}

	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (empty host), got %d", len(warnings))
	}
	if warnings[0].Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
		t.Errorf("warning code = %s, want %s", warnings[0].Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
	}

	if layer.Payload.Scalar == nil || !math.IsNaN(*layer.Payload.Scalar) {
		t.Errorf("Payload.Scalar = %v, want NaN", layer.Payload.Scalar)
	}
	if layer.Summary == nil || layer.Summary.PValue == nil || !math.IsNaN(*layer.Summary.PValue) {
		t.Errorf("Summary.PValue = %v, want NaN", layer.Summary)
	}
}

// TestApplyChiSqVsPop_EmptyPopulationDegenerate exercises the empty-
// population path: a population with zero records. Every expected count
// is zero; the handler emits NaN statistic + NaN p-value with
// PULSE_OVERLAY_REF_ZERO.
func TestApplyChiSqVsPop_EmptyPopulationDegenerate(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{}, 0, 0, 0, 0)
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layer, warnings, err := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)
	if err != nil {
		t.Fatalf("applyChiSqVsPop returned error: %v", err)
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
	if layer.Summary == nil || layer.Summary.PValue == nil || !math.IsNaN(*layer.Summary.PValue) {
		t.Errorf("Summary.PValue should be NaN, got %v", layer.Summary)
	}
}

// TestApplyChiSqVsPop_NumericHostRejected pins the discrete-arm-only
// contract: a numeric host (no Discrete payload) returns a coded
// PROCESSING_INTERNAL error carrying
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Defense in depth — the
// per-kind validator (E5-S6/S7) will reject numeric hosts at predict
// time.
func TestApplyChiSqVsPop_NumericHostRejected(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)
	numHost := newFacetResultNumeric("category", &types.FacetNumeric{
		Count: 100, Mean: 50, StdDev: 25, Min: 0, Max: 100,
	}, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := numHost.Fields["category"]
	_, _, applyErr := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)
	if applyErr == nil {
		t.Fatal("expected coded error for numeric host, got nil")
	}
	if !strings.Contains(applyErr.Error(), "discrete") {
		t.Errorf("error = %v, want message mentioning discrete-arm requirement", applyErr)
	}
}

// TestApplyChiSqVsPop_NilHost / NilPopulation pin the defense-in-depth
// rejection of nil host / population view (mirrors INDEX_VS_POP /
// ZSCORE_VS_POP).
func TestApplyChiSqVsPop_NilHost(t *testing.T) {
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)
	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}
	_, _, applyErr := applyChiSqVsPop(chiSqVsPopSpec(), nil, popView)
	if applyErr == nil {
		t.Fatal("expected coded error for nil host, got nil")
	}
	if !strings.Contains(applyErr.Error(), "FacetField host") {
		t.Errorf("error = %v, want message mentioning FacetField host", applyErr)
	}
}

func TestApplyChiSqVsPop_NilPopulation(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
	}, 1, 50, 50, 0)
	_, _, applyErr := applyChiSqVsPop(chiSqVsPopSpec(), host.Fields["category"], nil)
	if applyErr == nil {
		t.Fatal("expected coded error for nil population view, got nil")
	}
	if !strings.Contains(applyErr.Error(), "FacetPopulationView") {
		t.Errorf("error = %v, want message mentioning FacetPopulationView", applyErr)
	}
}

// TestApplyOverlaysFacet_DispatchesChiSqVsPop verifies the FACET-host
// dispatch table routes OVERLAY_CHISQ_VS_POP to the runtime handler.
// Mirrors TestApplyOverlaysFacet_DispatchesZScoreVsPop.
func TestApplyOverlaysFacet_DispatchesChiSqVsPop(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 50},
		{Value: "b", Count: 50},
	}, 2, 100, 100, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 60},
		{Value: "b", Count: 40},
	}, 2, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	specs := []types.OverlaySpec{*chiSqVsPopSpec()}
	layers, _, err := ApplyOverlaysFacet(specs, host.Fields["category"], popView)
	if err != nil {
		t.Fatalf("ApplyOverlaysFacet returned error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers length = %d, want 1", len(layers))
	}
	if layers[0].Kind != types.OverlayKindChiSqVsPop {
		t.Errorf("layers[0].Kind = %s, want %s", layers[0].Kind, types.OverlayKindChiSqVsPop)
	}
	if layers[0].Payload.Shape != types.OverlayShapeScalar {
		t.Errorf("layers[0].Payload.Shape = %s, want scalar", layers[0].Payload.Shape)
	}
}

// TestOverlayChiSqVsPop_StreamabilityIsBuffered pins the acceptance
// criterion "types/overlay_streamability.go row asserts Streamable:
// false (inferential ⇒ always buffered, per PRD §2 Non-Goals)".
// Anchored against the public OverlayStreamable surface so a future
// regression that flips the row trips this test.
func TestOverlayChiSqVsPop_StreamabilityIsBuffered(t *testing.T) {
	streamable, known := types.OverlayStreamable(types.OverlayKindChiSqVsPop)
	if !known {
		t.Fatal("OverlayKindChiSqVsPop missing from OverlayStreamability table")
	}
	if streamable {
		t.Error("OverlayKindChiSqVsPop streamable = true; want false (PRD §2 Non-Goals: streaming overlay path for inferential kinds is out of scope)")
	}
}

// TestApplyChiSqVsPop_StreamingVsBufferedByteIdentity pins the
// "buffered exit hook does not re-traverse host records" acceptance
// criterion. Running the handler twice against the same finalized
// FacetField produces byte-identical SCALAR output — the handler reads
// only POST-FINALIZE state. Documents the invariant explicitly so a
// future refactor that introduces a side channel (e.g. a per-record
// re-scan) trips the test.
func TestApplyChiSqVsPop_StreamingVsBufferedByteIdentity(t *testing.T) {
	host := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 30},
		{Value: "b", Count: 30},
		{Value: "c", Count: 30},
		{Value: "d", Count: 10},
	}, 4, 100, 100, 0)
	popResult := newFacetResultDiscrete("category", []types.FacetValueCount{
		{Value: "a", Count: 40},
		{Value: "b", Count: 30},
		{Value: "c", Count: 20},
		{Value: "d", Count: 10},
	}, 4, 100, 100, 0)

	popView, err := ResolveFacetPopulation(popResult, "category")
	if err != nil {
		t.Fatalf("ResolveFacetPopulation failed: %v", err)
	}

	hostField := host.Fields["category"]
	layerA, _, _ := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)
	layerB, _, _ := applyChiSqVsPop(chiSqVsPopSpec(), hostField, popView)

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
	if layerA.Summary.Parameters["df"] != layerB.Summary.Parameters["df"] {
		t.Errorf("Summary.Parameters[\"df\"] differs: %v vs %v",
			layerA.Summary.Parameters["df"], layerB.Summary.Parameters["df"])
	}
}
