package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_DELTA_VS_BASELINE handler
// (processing/overlay_delta_vs_baseline.go).
//
// E4-S3 scope:
//
//   - Third windowed-Process overlay in the catalog (E4-S3) and the
//     absolute-difference twin of OVERLAY_INDEX_VS_BASELINE (E4-S2). The
//     handler is wired into seriesOverlayHandlers via the dispatch entry
//     in processing/overlay_series.go.
//   - Acceptance criteria covered: basic positional baseline math, mid-
//     series baseline, **NO** PULSE_OVERLAY_REF_ZERO on zero baseline
//     (the key contrast vs INDEX_VS_BASELINE), negative-delta sign
//     preservation, absent host ordinal NaN-Statistic passthrough,
//     negative + out-of-range Position propagate PULSE_OVERLAY_REF_UNKNOWN
//     from the resolver, default-name synthesis, buffered-path full
//     end-to-end via Processor.Process, and the structural nil-host
//     coded-error defense.

// newDeltaVsBaselineSpec returns the canonical happy-path
// OVERLAY_DELTA_VS_BASELINE spec the per-test fixtures consume — GROUP
// scope, populated `Ref.BaselineIndex{Position}` (the windowed positional
// anchor), and a deterministic name so the layer's renderer-facing label
// is pinned in the assertions.
func newDeltaVsBaselineSpec(name string, position int) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindDeltaVsBaseline,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			BaselineIndex: &types.OverlayBaselineIndexRef{Position: position},
		},
	}
}

// TestOverlay_DeltaVsBaseline_BasicBaselineAtZero pins the canonical
// happy path: four points [10, 15, 25, 30] anchored against Position 0
// produce deltas [0, 5, 15, 20]. The baseline ordinal lands at exactly
// 0.0 by construction (self-vs-self under subtraction).
func TestOverlay_DeltaVsBaseline_BasicBaselineAtZero(t *testing.T) {
	keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}, {"apr"}}
	values := []float64{10.0, 15.0, 25.0, 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsBaselineSpec("delta_baseline", 0)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d", len(entries), len(keys))
	}

	const tol = 1e-9
	// Baseline is values[0] = 10; deltas = value - 10. The self-vs-self
	// ordinal at Position 0 lands at exactly 0 by construction.
	wants := []float64{
		0.0,  // baseline-at-self
		5.0,  // 15 - 10
		15.0, // 25 - 10
		20.0, // 30 - 10
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, w, tol)
	}

	// Layer-level summary: baseline = 0 (the delta family centres
	// diverging colour ramps on 0). Count = 4 — every present entry
	// contributes (including the 0 at the baseline ordinal).
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 0.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 0", got)
	}
	if got, want := *layers[0].Summary.Count, 4; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_DeltaVsBaseline_BaselineAtMiddle pins a mid-series anchor:
// [10, 20, 40] with Position 1 produces [-10, 0, 20]. The baseline
// ordinal itself reads `0` (self-vs-self under subtraction) and the
// surrounding ordinals deviate against it with sign preserved.
func TestOverlay_DeltaVsBaseline_BaselineAtMiddle(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 20.0, 40.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsBaselineSpec("delta_baseline", 1)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	const tol = 1e-9
	// Baseline is values[1] = 20; deltas = value - 20.
	wants := []float64{
		-10.0, // 10 - 20 (sign preserved)
		0.0,   // baseline-at-self
		20.0,  // 40 - 20
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, w, tol)
	}
}

// TestOverlay_DeltaVsBaseline_ZeroBaselineEmitsNoWarning pins the KEY
// CONTRAST with OVERLAY_INDEX_VS_BASELINE: a zero baseline value does NOT
// emit PULSE_OVERLAY_REF_ZERO. Subtraction is total — there is no
// zero-denominator hazard for DELTA_VS_BASELINE. The series [0, 5, 10]
// anchored at Position 0 yields deltas [0, 5, 10] (the raw values pass
// through because subtracting zero is a no-op).
//
// Distinct from the INDEX_VS_BASELINE twin where the same fixture fires
// ONE PULSE_OVERLAY_REF_ZERO warning and emits NaN throughout.
func TestOverlay_DeltaVsBaseline_ZeroBaselineEmitsNoWarning(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{0.0, 5.0, 10.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsBaselineSpec("delta_baseline", 0)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	// Key contrast vs INDEX_VS_BASELINE: NO warnings on zero baseline.
	assertNoOverlayWarnings(t, warnings)

	const tol = 1e-9
	// Baseline is values[0] = 0; deltas = value - 0 = value (raw passthrough).
	wants := []float64{
		0.0,  // 0 - 0 (baseline-at-self)
		5.0,  // 5 - 0
		10.0, // 10 - 0
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, w, tol)
	}

	// Layer-level Count = 3 — every present entry contributes (vs
	// INDEX_VS_BASELINE which surfaces Count=0 on the same fixture because
	// every entry is NaN). Defensive assertion against any future
	// regression that mis-attributes the zero-baseline path.
	if got, want := *layers[0].Summary.Count, 3; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v (every present entry contributes; no NaN gating)", got, want)
	}
}

// TestOverlay_DeltaVsBaseline_NegativeDeltaPreserved pins the sign-
// preservation contract: a series whose tail descends below the baseline
// yields a negative delta verbatim. [10, 5, 20] with Position 0 produces
// [0, -5, 10]. Renderers branch on sign to drive colour ramps; the
// handler must NOT clamp or absolute-value.
func TestOverlay_DeltaVsBaseline_NegativeDeltaPreserved(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 5.0, 20.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsBaselineSpec("delta_baseline", 0)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	const tol = 1e-9
	// Baseline is values[0] = 10; deltas = value - 10.
	wants := []float64{
		0.0,  // baseline-at-self
		-5.0, // 5 - 10 (negative; sign preserved)
		10.0, // 20 - 10
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, w, tol)
	}

	// Layer-level Min must reflect the negative delta. Summary.Min /
	// Summary.Max bracket the present-non-NaN entries.
	if layers[0].Summary.Min == nil {
		t.Fatalf("layer.Summary.Min nil; want populated")
	}
	if got := *layers[0].Summary.Min; got != -5.0 {
		t.Errorf("layer.Summary.Min = %v, want -5", got)
	}
	if got := *layers[0].Summary.Max; got != 10.0 {
		t.Errorf("layer.Summary.Max = %v, want 10", got)
	}
}

// TestOverlay_DeltaVsBaseline_AbsentPointEmitsNaN pins the absent-point
// passthrough: [10, absent, 30] anchored at Position 0 produces
// [0, nil, 20] on Summary.Statistic. The absent middle ordinal surfaces a
// SeriesEntry whose Summary leaves Statistic unset (the canonical
// "present slot, empty summary" shape from E3-S1) and DOES NOT emit a
// warning — absent passthrough is structurally distinct from a zero-
// denominator path AND there is no zero-denominator path on this kind
// anyway.
//
// math.NaN signals absent (newStubSeriesHost's resolver returns
// (0, false) for NaN inputs).
func TestOverlay_DeltaVsBaseline_AbsentPointEmitsNaN(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, math.NaN(), 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsBaselineSpec("delta_baseline", 0)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3", len(entries))
	}

	const tol = 1e-9
	// entry[0]: baseline-at-self = 0.
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, 0.0, tol)
	// entry[1]: nil Statistic (absent — canonical "present slot, empty
	// summary" shape from E3-S1).
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1].Summary.Statistic = %v, want nil (absent group)",
			*entries[1].Summary.Statistic)
	}
	// entry[2]: 30 - 10 = 20.
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 20.0, tol)
}

// TestOverlay_DeltaVsBaseline_NegativeBaselineRejected pins the
// negative-Position rejection: ResolveBaselineIndex returns a
// PULSE_OVERLAY_REF_UNKNOWN-coded error and the handler propagates it
// verbatim through ApplyOverlaysSeries. Mirrors the
// INDEX_VS_BASELINE rejection contract — both kinds share the same
// resolver pipeline.
func TestOverlay_DeltaVsBaseline_NegativeBaselineRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 20.0, 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsBaselineSpec("delta_baseline", -1)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err == nil {
		t.Fatalf("ApplyOverlaysSeries err = nil; want PULSE_OVERLAY_REF_UNKNOWN. layers=%v warnings=%v",
			layers, warnings)
	}
	assertBaselineIndexCodedError(t, err, -1, len(keys))
}

// TestOverlay_DeltaVsBaseline_OutOfRangeBaselineRejected pins the
// out-of-range-Position rejection: Position 10 against a 5-point series
// fires PULSE_OVERLAY_REF_UNKNOWN with `series_length: 5` via the
// resolver. Identical to the INDEX_VS_BASELINE rejection shape.
func TestOverlay_DeltaVsBaseline_OutOfRangeBaselineRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsBaselineSpec("delta_baseline", 10)}

	_, _, err := ApplyOverlaysSeries(specs, host)
	if err == nil {
		t.Fatalf("ApplyOverlaysSeries err = nil; want PULSE_OVERLAY_REF_UNKNOWN")
	}
	assertBaselineIndexCodedError(t, err, 10, len(keys))
}

// TestOverlay_DeltaVsBaseline_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. DELTA_VS_BASELINE is
// the windowed positional-baseline absolute-difference kind (no axis
// dispatch — the baseline ordinal is the only knob); the synthesiser
// surfaces the lower-case bare-kind string "delta_vs_baseline" mirroring
// the INDEX_VS_BASELINE / INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES /
// INDEX_VS_PRIOR convention.
func TestOverlay_DeltaVsBaseline_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}}
	host := newStubSeriesHost(keys, []float64{1.0})
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindDeltaVsBaseline,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
		},
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "delta_vs_baseline"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_DeltaVsBaseline_BufferedPath drives Processor.Process
// against the canonical (region, score) integration fixture end-to-end
// to assert the overlay layer is populated and the per-entry deltas
// match the resolved baseline. DELTA_VS_BASELINE is buffered (the
// streamability flag is false), so we force the buffered path via
// AGG_MEDIAN to mirror the smoke-test convention used by the sibling-
// family E2E tests.
//
// Fixture row order is alphabetic: east(30), north(150), west(90). With
// Position=0 the east (30) ordinal is the baseline, so:
//
//	east  (30):  30 - 30  =   0
//	north (150): 150 - 30 = 120
//	west  (90):  90 - 30  =  60
func TestOverlay_DeltaVsBaseline_BufferedPath(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newDeltaVsBaselineSpec("delta_baseline", 0),
	}, true /*forceBuffered*/)
	if path == PathStreaming {
		t.Fatalf("LastPath() = %s, want non-streaming (DELTA_VS_BASELINE is buffered + AGG_MEDIAN forces buffered)",
			path)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
	}
	// Baseline is east (30); deltas = value - 30.
	want := []float64{
		0.0,   // east  — baseline-at-self
		120.0, // north — 150 - 30
		60.0,  // west  — 90 - 30
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
}

// TestOverlay_DeltaVsBaseline_NilHostReturnsCoded is the defense-in-depth
// nil-host guard: a nil SeriesHostView surfaces the resolver's coded
// PULSE_OVERLAY_REF_UNKNOWN error rather than panicking. The branch is
// unreachable in practice because ApplyOverlaysSeries short-circuits
// empty specs before dispatch, but the defense matches the
// INDEX_VS_BASELINE / INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES /
// INDEX_VS_PRIOR safety pattern.
func TestOverlay_DeltaVsBaseline_NilHostReturnsCoded(t *testing.T) {
	spec := newDeltaVsBaselineSpec("delta_baseline", 0)
	_, _, err := applyDeltaVsBaseline(&spec, nil)
	if err == nil {
		t.Fatalf("expected coded error for nil host, got nil")
	}
	// The resolver fires its negative/out-of-range arm because
	// host.GroupCount() returns 0 for a nil host and 0 >= 0 trips the
	// out-of-range path with series_length=0. Identical shape to
	// INDEX_VS_BASELINE's nil-host defense.
	assertBaselineIndexCodedError(t, err, 0, 0)
}
