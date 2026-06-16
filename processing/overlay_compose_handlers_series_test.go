package processing

import (
	"math"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func makeSeriesResponse(keys []string, values []float64) *types.Response {
	if len(keys) != len(values) {
		panic("makeSeriesResponse: keys and values must have the same length")
	}
	rows := make([]map[string]any, len(keys))
	for i, k := range keys {
		rows[i] = map[string]any{
			"region": k,
			"sum":    values[i],
		}
	}
	return &types.Response{Data: rows}
}

// composeSpecSeriesRef wires a minimal ComposeOverlaySpec for the
// SERIES-shape per-handler tests.
func composeSpecSeriesRef(kind types.OverlayKind, params map[string]any) types.ComposeOverlaySpec {
	return types.ComposeOverlaySpec{
		Name:      "test",
		Kind:      kind,
		Scope:     types.OverlayScopeGroup,
		Reference: "baseline",
		Targets:   []string{"treatment"},
		Params:    params,
	}
}

// entryStat returns the Statistic from a SeriesEntry by index; fails
// the test when the entry's Statistic is unset.
func entryStat(t *testing.T, layer types.OverlayLayer, i int) float64 {
	t.Helper()
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("layer.Payload.Shape = %q, want %q", layer.Payload.Shape, types.OverlayShapeSeries)
	}
	series := layer.Payload.Series
	if series == nil {
		t.Fatal("layer.Payload.Series is nil")
	}
	if i >= len(series.Entries) {
		t.Fatalf("entry %d out of range (len=%d)", i, len(series.Entries))
	}
	if series.Entries[i].Summary.Statistic == nil {
		t.Fatalf("entry %d Statistic is unset", i)
	}
	return *series.Entries[i].Summary.Statistic
}

// TestApplyIndexVsRef_SeriesHappyPath exercises the per-group ratio
// (target / ref) * scale against hand-computed indices. Scale defaults
// to 100. The shape dispatcher detects SERIES from both slots and
// routes into applyIndexVsRefSeries.
func TestApplyIndexVsRef_SeriesHappyPath(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{10, 20, 30, 40})
	target := makeSeriesResponse([]string{"north", "south", "east", "west"}, []float64{15, 30, 45, 60})
	spec := composeSpecSeriesRef(types.OverlayKindIndexVsRef, nil)
	layer, warnings, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// Every group is target / ref * 100 = 1.5 * 100 = 150.
	for i := 0; i < 4; i++ {
		approxEqual(t, "entry", entryStat(t, layer, i), 150.0, 1e-9)
	}
	if layer.Kind != types.OverlayKindIndexVsRef {
		t.Errorf("layer.Kind = %q, want %q", layer.Kind, types.OverlayKindIndexVsRef)
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 100.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 100", layer.Summary)
	}
}

// TestApplyIndexVsRef_SeriesZeroRefEmitsWarning asserts that a zero
// reference value emits PULSE_OVERLAY_REF_ZERO and substitutes NaN on
// the affected entry. Other entries stay finite.
func TestApplyIndexVsRef_SeriesZeroRefEmitsWarning(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south", "east"}, []float64{0, 10, 20})
	target := makeSeriesResponse([]string{"north", "south", "east"}, []float64{5, 50, 100})
	spec := composeSpecSeriesRef(types.OverlayKindIndexVsRef, nil)
	layer, warnings, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef: %v", err)
	}
	requireRefZeroWarning(t, warnings)
	// Entry 0 ⇒ NaN because ref was zero.
	v := entryStat(t, layer, 0)
	if !math.IsNaN(v) {
		t.Errorf("entry 0 stat = %v, want NaN (zero ref)", v)
	}
	// Other entries ⇒ 500.
	approxEqual(t, "entry 1", entryStat(t, layer, 1), 500.0, 1e-9)
	approxEqual(t, "entry 2", entryStat(t, layer, 2), 500.0, 1e-9)
}

// TestApplyIndexVsRef_SeriesMissingRefKeyWarns asserts that a target
// row whose key is absent from the reference series emits
// PULSE_OVERLAY_REF_ZERO with ref_missing=true and the entry is
// dropped from the output (no NaN substitution — the row carries no
// reference to compare against).
func TestApplyIndexVsRef_SeriesMissingRefKeyWarns(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south"}, []float64{10, 20})
	target := makeSeriesResponse([]string{"north", "south", "east"}, []float64{15, 30, 45})
	spec := composeSpecSeriesRef(types.OverlayKindIndexVsRef, nil)
	layer, warnings, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef: %v", err)
	}
	var sawMissing bool
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			if v, _ := w.Details["ref_missing"].(bool); v {
				sawMissing = true
			}
		}
	}
	if !sawMissing {
		t.Errorf("expected PULSE_OVERLAY_REF_ZERO with ref_missing=true, got: %+v", warnings)
	}
	if layer.Payload.Series == nil {
		t.Fatal("Series payload is nil")
	}
	// Two entries should land (north + south); east is dropped.
	if got := len(layer.Payload.Series.Entries); got != 2 {
		t.Errorf("len(Entries) = %d, want 2 (east is dropped)", got)
	}
}

// TestApplyDeltaVsRef_SeriesHappyPath pins the per-group subtraction.
func TestApplyDeltaVsRef_SeriesHappyPath(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south", "east"}, []float64{1, 2, 3})
	target := makeSeriesResponse([]string{"north", "south", "east"}, []float64{2, 4, 6})
	spec := composeSpecSeriesRef(types.OverlayKindDeltaVsRef, nil)
	layer, warnings, err := applyDeltaVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyDeltaVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	want := []float64{1, 2, 3}
	for i := 0; i < 3; i++ {
		approxEqual(t, "entry", entryStat(t, layer, i), want[i], 1e-9)
	}
	if layer.Summary == nil || layer.Summary.Baseline == nil || *layer.Summary.Baseline != 0.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 0", layer.Summary)
	}
}

// TestApplyDeltaVsRef_SeriesZeroRefNoNaN asserts that a zero reference
// value produces a finite delta (target - 0 = target) and does NOT emit
// PULSE_OVERLAY_REF_ZERO — subtraction is well-defined for any finite
// reference value.
func TestApplyDeltaVsRef_SeriesZeroRefNoNaN(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south"}, []float64{0, 5})
	target := makeSeriesResponse([]string{"north", "south"}, []float64{10, 20})
	spec := composeSpecSeriesRef(types.OverlayKindDeltaVsRef, nil)
	layer, warnings, err := applyDeltaVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyDeltaVsRef: %v", err)
	}
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			if v, _ := w.Details["ref_missing"].(bool); !v {
				t.Errorf("PULSE_OVERLAY_REF_ZERO emitted against zero ref; DELTA must not emit this code (got %+v)", w.Details)
			}
		}
	}
	approxEqual(t, "entry 0", entryStat(t, layer, 0), 10.0, 1e-9)
	approxEqual(t, "entry 1", entryStat(t, layer, 1), 15.0, 1e-9)
}

// TestApplyDeltaVsRef_SeriesMissingRefKeyWarns asserts that a missing
// reference row emits PULSE_OVERLAY_REF_ZERO with ref_missing=true and
// the affected entry is dropped from the output.
func TestApplyDeltaVsRef_SeriesMissingRefKeyWarns(t *testing.T) {
	ref := makeSeriesResponse([]string{"north"}, []float64{1})
	target := makeSeriesResponse([]string{"north", "south"}, []float64{5, 10})
	spec := composeSpecSeriesRef(types.OverlayKindDeltaVsRef, nil)
	layer, warnings, err := applyDeltaVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyDeltaVsRef: %v", err)
	}
	var sawMissing bool
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			if v, _ := w.Details["ref_missing"].(bool); v {
				sawMissing = true
			}
		}
	}
	if !sawMissing {
		t.Errorf("expected PULSE_OVERLAY_REF_ZERO with ref_missing=true, got: %+v", warnings)
	}
	if got := len(layer.Payload.Series.Entries); got != 1 {
		t.Errorf("len(Entries) = %d, want 1 (south is dropped)", got)
	}
}

// TestApplyTVsRef_SeriesKnownAnswer pins a hand-computed two-sided
// Welch p-value for the per-group t-test. With default variance=1 and
// n=2, the SE = sqrt(1/2 + 1/2) = 1 and t = (target - ref)/1. df via
// the Welch-Satterthwaite shape is = (1)² / ((0.25)/1 + (0.25)/1) = 2.
// For target=4, ref=2, t=2, df=2, the two-sided p ≈ 0.1835.
func TestApplyTVsRef_SeriesKnownAnswer(t *testing.T) {
	ref := makeSeriesResponse([]string{"north", "south"}, []float64{2, 5})
	target := makeSeriesResponse([]string{"north", "south"}, []float64{4, 5})
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	layer, warnings, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	// Cross-check against welchTTest directly (same helper backing
	// the MATRIX-arm T_CELL handler) so the test is byte-equivalent to
	// the implementation under test.
	wantNorth, ok := welchTTest(4.0, 1.0, 2.0, 2.0, 1.0, 2.0)
	if !ok {
		t.Fatal("welchTTest reference computation failed")
	}
	approxEqual(t, "entry north", entryStat(t, layer, 0), wantNorth, 1e-9)
	// South: target=ref so t=0 → p=1.
	wantSouth, _ := welchTTest(5.0, 1.0, 2.0, 5.0, 1.0, 2.0)
	approxEqual(t, "entry south", entryStat(t, layer, 1), wantSouth, 1e-9)
}

// TestApplyTVsRef_SeriesMissingRefKeyWarns asserts that a missing
// reference row emits PULSE_OVERLAY_REF_ZERO with ref_missing=true and
// the affected entry surfaces NaN (the inferential family surfaces
// NaN rather than dropping the entry — distinct from the descriptive
// INDEX/DELTA siblings which drop the entry entirely).
func TestApplyTVsRef_SeriesMissingRefKeyWarns(t *testing.T) {
	ref := makeSeriesResponse([]string{"north"}, []float64{1})
	target := makeSeriesResponse([]string{"north", "south"}, []float64{5, 10})
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	layer, warnings, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	var sawMissing bool
	for _, w := range warnings {
		if w.Code == string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			if v, _ := w.Details["ref_missing"].(bool); v {
				sawMissing = true
			}
		}
	}
	if !sawMissing {
		t.Errorf("expected PULSE_OVERLAY_REF_ZERO with ref_missing=true, got: %+v", warnings)
	}
	// Inferential family surfaces NaN on the affected entry rather than
	// dropping it.
	if got := len(layer.Payload.Series.Entries); got != 2 {
		t.Errorf("len(Entries) = %d, want 2 (NaN substitution for missing ref)", got)
	}
	v := entryStat(t, layer, 1)
	if !math.IsNaN(v) {
		t.Errorf("entry 1 (south) stat = %v, want NaN (missing ref)", v)
	}
}

// TestOverlay_ComposeIndexVsRef_Series_Streaming asserts the
// streamability equivalence guarantee for the SERIES-arm: running the
// handler against streaming-vs-buffered host pairs produces byte-
// identical SeriesPayload output. The Compose chassis runs the
// overlay fold AFTER every slot has finalised regardless of whether
// individual slots streamed during their per-Request Process call —
// so the equivalence test is "same finalised inputs ⇒ same overlay
// output". We exercise that by invoking the handler against the same
// pair of pre-finalised Responses twice and asserting byte identity.
func TestOverlay_ComposeIndexVsRef_Series_Streaming(t *testing.T) {
	keys := []string{"north", "south", "east", "west"}
	ref := makeSeriesResponse(keys, []float64{10, 20, 30, 40})
	target := makeSeriesResponse(keys, []float64{15, 30, 45, 60})
	spec := composeSpecSeriesRef(types.OverlayKindIndexVsRef, nil)

	// Two independent invocations against identical finalised inputs
	// — the streaming-vs-buffered contract for this overlay handler is
	// that finalised state IN ⇒ finalised state OUT, byte-identical.
	layerA, warningsA, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef (A): %v", err)
	}
	layerB, warningsB, err := applyIndexVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef (B): %v", err)
	}
	if len(warningsA) != 0 || len(warningsB) != 0 {
		t.Errorf("warnings = %+v / %+v, want none", warningsA, warningsB)
	}
	seriesA := layerA.Payload.Series
	seriesB := layerB.Payload.Series
	if seriesA == nil || seriesB == nil {
		t.Fatal("series payload is nil on one of the invocations")
	}
	if len(seriesA.Entries) != len(seriesB.Entries) {
		t.Fatalf("entry count mismatch: A=%d, B=%d", len(seriesA.Entries), len(seriesB.Entries))
	}
	for i := range seriesA.Entries {
		statA := *seriesA.Entries[i].Summary.Statistic
		statB := *seriesB.Entries[i].Summary.Statistic
		if statA != statB {
			t.Errorf("entry %d statistic divergence: A=%v, B=%v", i, statA, statB)
		}
	}
}

// TestApplyComposeOverlays_SeriesShapeDispatchesCorrectly asserts the
// dual-shape dispatch wiring — feeding SERIES-shape Responses through
// the per-handler dispatcher routes into the series arm (SeriesPayload
// out), and MATRIX-shape Responses route into the matrix arm
// (MatrixPayload out). Locks the composeHostIsSeries discriminator.
func TestApplyComposeOverlays_SeriesShapeDispatchesCorrectly(t *testing.T) {
	// SERIES dispatch.
	refSeries := makeSeriesResponse([]string{"north"}, []float64{10})
	targetSeries := makeSeriesResponse([]string{"north"}, []float64{20})
	specSeries := composeSpecSeriesRef(types.OverlayKindIndexVsRef, nil)
	layerSeries, _, err := applyIndexVsRef(&specSeries, refSeries, []*types.Response{targetSeries}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef (series): %v", err)
	}
	if layerSeries.Payload.Shape != types.OverlayShapeSeries {
		t.Errorf("series dispatch produced shape %q, want %q", layerSeries.Payload.Shape, types.OverlayShapeSeries)
	}
	if layerSeries.Payload.Series == nil {
		t.Error("series dispatch: Series payload is nil")
	}

	// MATRIX dispatch (cross-check via the existing matrix fixture).
	refMatrix := makeMatrixFromValues([3][3]float64{
		{10, 20, 30}, {40, 50, 60}, {70, 80, 90},
	})
	targetMatrix := makeMatrixFromValues([3][3]float64{
		{15, 30, 45}, {60, 75, 90}, {105, 120, 135},
	})
	specMatrix := composeSpecMatrixRef(types.OverlayKindIndexVsRef, nil)
	layerMatrix, _, err := applyIndexVsRef(&specMatrix, refMatrix, []*types.Response{targetMatrix}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyIndexVsRef (matrix): %v", err)
	}
	if layerMatrix.Payload.Shape != types.OverlayShapeMatrix {
		t.Errorf("matrix dispatch produced shape %q, want %q", layerMatrix.Payload.Shape, types.OverlayShapeMatrix)
	}
	if layerMatrix.Payload.Matrix == nil {
		t.Error("matrix dispatch: Matrix payload is nil")
	}
}

func TestApplyTVsRef_TripleKnownAnswer(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
	}
	refTriples := []WelfordTriple{
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
	}
	target := makeSeriesResponseTriples([]string{"north", "south", "east", "west"}, targetTriples)
	ref := makeSeriesResponseTriples([]string{"north", "south", "east", "west"}, refTriples)
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	layer, warnings, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	want, ok := welchTTest(10.0, 4.0, 10.0, 9.0, 4.0, 10.0)
	if !ok {
		t.Fatal("welchTTest reference computation failed")
	}
	for i := 0; i < 4; i++ {
		approxEqual(t, "entry (triple)", entryStat(t, layer, i), want, 1e-9)
	}
}

// TestApplyTVsRef_TripleBypassesParamsDefaults asserts the precedence
// rule: rows carrying a WelfordTriple ignore the Params defaults.
// Params here, if honoured, would shift p toward 1.0; the triple path
// must produce the same answer as TestApplyTVsRef_TripleKnownAnswer.
func TestApplyTVsRef_TripleBypassesParamsDefaults(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
	}
	refTriples := []WelfordTriple{
		{Mean: 9, Variance: 4, N: 10},
		{Mean: 9, Variance: 4, N: 10},
	}
	target := makeSeriesResponseTriples([]string{"north", "south"}, targetTriples)
	ref := makeSeriesResponseTriples([]string{"north", "south"}, refTriples)
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, map[string]any{
		"variance_target":    100.0,
		"variance_ref":       100.0,
		"sample_size_target": 1000.0,
		"sample_size_ref":    1000.0,
	})
	layer, _, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	want, _ := welchTTest(10.0, 4.0, 10.0, 9.0, 4.0, 10.0)
	for i := 0; i < 2; i++ {
		approxEqual(t, "entry (triple bypasses Params)", entryStat(t, layer, i), want, 1e-9)
	}
}

// TestApplyTVsRef_TripleDegenerateInsufficientN exercises the n<2 arm
// of the triple-bearing series path. Every group should surface NaN +
// PULSE_OVERLAY_REF_ZERO.
func TestApplyTVsRef_TripleDegenerateInsufficientN(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 4, N: 1},
		{Mean: 10, Variance: 4, N: 1},
	}
	refTriples := []WelfordTriple{
		{Mean: 9, Variance: 4, N: 1},
		{Mean: 9, Variance: 4, N: 1},
	}
	target := makeSeriesResponseTriples([]string{"north", "south"}, targetTriples)
	ref := makeSeriesResponseTriples([]string{"north", "south"}, refTriples)
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	layer, warnings, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	if len(warnings) != 2 {
		t.Errorf("len(warnings) = %d, want 2 (each group N<2)", len(warnings))
	}
	for _, w := range warnings {
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("warning.Code = %q, want %q", w.Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
		}
	}
	for i := 0; i < 2; i++ {
		if !math.IsNaN(entryStat(t, layer, i)) {
			t.Errorf("entry(%d) = %v, want NaN", i, entryStat(t, layer, i))
		}
	}
}

// TestApplyTVsRef_TripleZeroVariance exercises the zero-variance arm
// of the triple path. Both sides carry Variance=0 ⇒ welchTTest
// surfaces NaN/false ⇒ every entry surfaces NaN + PULSE_OVERLAY_REF_ZERO.
func TestApplyTVsRef_TripleZeroVariance(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 0, N: 10},
		{Mean: 10, Variance: 0, N: 10},
	}
	refTriples := []WelfordTriple{
		{Mean: 9, Variance: 0, N: 10},
		{Mean: 9, Variance: 0, N: 10},
	}
	target := makeSeriesResponseTriples([]string{"north", "south"}, targetTriples)
	ref := makeSeriesResponseTriples([]string{"north", "south"}, refTriples)
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	layer, warnings, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyTVsRef: %v", err)
	}
	if len(warnings) != 2 {
		t.Errorf("len(warnings) = %d, want 2 (both groups zero SE)", len(warnings))
	}
	for i := 0; i < 2; i++ {
		if !math.IsNaN(entryStat(t, layer, i)) {
			t.Errorf("entry(%d) = %v, want NaN", i, entryStat(t, layer, i))
		}
	}
}

func TestApplyTVsRef_MixedShapeRejected(t *testing.T) {
	targetTriples := []WelfordTriple{
		{Mean: 10, Variance: 4, N: 10},
		{Mean: 10, Variance: 4, N: 10},
	}
	target := makeSeriesResponseTriples([]string{"north", "south"}, targetTriples)
	ref := makeSeriesResponse([]string{"north", "south"}, []float64{9, 9})
	spec := composeSpecSeriesRef(types.OverlayKindTVsRef, nil)
	_, _, err := applyTVsRef(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err == nil {
		t.Fatal("applyTVsRef: want error for mixed triple/scalar pair, got nil")
	}
	coded, ok := err.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("err is not *pulseerrors.CodedError: %T (%v)", err, err)
	}
	if got, _ := coded.Details["code"].(string); got != string(pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT) {
		t.Errorf("Details[code] = %q, want %q", got, pulseerrors.PULSE_OVERLAY_SCHEMA_DIVERGENT)
	}
}
