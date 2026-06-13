package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_INDEX_VS_BASELINE handler
// (processing/overlay_index_vs_baseline.go).
//
// E4-S2 scope:
//
//   - Second windowed-Process overlay in the catalog (first consumer of
//     the BaselineIndex.Position arm landed at E4-S1). The handler is
//     wired into seriesOverlayHandlers via the dispatch entry in
//     processing/overlay_series.go.
//   - Acceptance criteria covered: basic positional baseline math, mid-
//     series baseline, zero baseline PULSE_OVERLAY_REF_ZERO, absent host
//     ordinal NaN passthrough, negative + out-of-range Position propagate
//     PULSE_OVERLAY_REF_UNKNOWN from the resolver, buffered-path full
//     end-to-end via Processor.Process.

// newIndexVsBaselineSpec returns the canonical happy-path
// OVERLAY_INDEX_VS_BASELINE spec the per-test fixtures consume — GROUP
// scope, populated `Ref.BaselineIndex{Position}` (the windowed positional
// anchor), and a deterministic name so the layer's renderer-facing label
// is pinned in the assertions.
func newIndexVsBaselineSpec(name string, position int) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindIndexVsBaseline,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			BaselineIndex: &types.OverlayBaselineIndexRef{Position: position},
		},
	}
}

// TestOverlay_IndexVsBaseline_BasicBaselineAtZero pins the canonical
// happy path: four points [10, 15, 20, 30] anchored against Position 0
// produce indices [100, 150, 200, 300].
func TestOverlay_IndexVsBaseline_BasicBaselineAtZero(t *testing.T) {
	keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}, {"apr"}}
	values := []float64{10.0, 15.0, 20.0, 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsBaselineSpec("idx_baseline", 0)}

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
	// Baseline is values[0] = 10; indices = value / 10 * 100. The self-vs-
	// self ordinal at Position 0 lands at exactly 100 by construction.
	wants := []float64{
		100.0, // baseline-at-self
		150.0,
		200.0,
		300.0,
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, w, tol)
	}

	// Layer-level summary: baseline = 100, Count = 4 (every present
	// non-NaN entry contributes — including the self-vs-self 100 at the
	// baseline ordinal).
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 100.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 100", got)
	}
	if got, want := *layers[0].Summary.Count, 4; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_IndexVsBaseline_BaselineAtMiddle pins a mid-series anchor:
// [10, 20, 40] with Position 1 produces [50, 100, 200]. The baseline
// ordinal itself reads `100.0` (self-vs-self under the ratio scaling) and
// the surrounding ordinals lift / fall against it.
func TestOverlay_IndexVsBaseline_BaselineAtMiddle(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 20.0, 40.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsBaselineSpec("idx_baseline", 1)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	const tol = 1e-9
	// Baseline is values[1] = 20; indices = value / 20 * 100.
	wants := []float64{
		50.0,
		100.0, // baseline-at-self
		200.0,
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, w, tol)
	}
}

// TestOverlay_IndexVsBaseline_BaselineZeroEmitsRefZero pins the
// zero-baseline path: [0, 5, 10] anchored to Position 0 produces NaN on
// every entry plus ONE PULSE_OVERLAY_REF_ZERO warning. Mirrors the
// share / index / zscore family's zero-denominator contract.
func TestOverlay_IndexVsBaseline_BaselineZeroEmitsRefZero(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{0.0, 5.0, 10.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsBaselineSpec("idx_baseline", 0)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3", len(entries))
	}
	for i, entry := range entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN (zero baseline)", i)
		}
		if !math.IsNaN(*entry.Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN (zero baseline)", i,
				*entry.Summary.Statistic)
		}
	}

	// Layer-level Count = 0 — every entry is NaN, so no present non-NaN
	// entries contribute to Min / Max / Count.
	if got, want := *layers[0].Summary.Count, 0; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_IndexVsBaseline_AbsentPointEmitsNaN pins the absent-point
// passthrough: [10, absent, 30] anchored to Position 0 produces
// [100, nil, 300] on Summary.Statistic. The absent middle ordinal
// surfaces a SeriesEntry whose Summary leaves Statistic unset (the
// canonical "present slot, empty summary" shape from E3-S1) and DOES NOT
// emit a warning — absent passthrough is structurally distinct from a
// zero-denominator path.
//
// math.NaN signals absent (newStubSeriesHost's resolver returns
// (0, false) for NaN inputs).
func TestOverlay_IndexVsBaseline_AbsentPointEmitsNaN(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, math.NaN(), 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsBaselineSpec("idx_baseline", 0)}

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
	// entry[0]: baseline-at-self = 100.
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, 100.0, tol)
	// entry[1]: nil Statistic (absent — canonical "present slot, empty
	// summary" shape from E3-S1).
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1].Summary.Statistic = %v, want nil (absent group)",
			*entries[1].Summary.Statistic)
	}
	// entry[2]: 30/10*100 = 300.
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 300.0, tol)
}

// TestOverlay_IndexVsBaseline_NegativeBaselineRejected pins the
// negative-Position rejection: ResolveBaselineIndex returns a
// PULSE_OVERLAY_REF_UNKNOWN-coded error and the handler propagates it
// verbatim through ApplyOverlaysSeries.
func TestOverlay_IndexVsBaseline_NegativeBaselineRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 20.0, 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsBaselineSpec("idx_baseline", -1)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err == nil {
		t.Fatalf("ApplyOverlaysSeries err = nil; want PULSE_OVERLAY_REF_UNKNOWN. layers=%v warnings=%v",
			layers, warnings)
	}
	assertBaselineIndexCodedError(t, err, -1, len(keys))
}

// TestOverlay_IndexVsBaseline_OutOfRangeBaselineRejected pins the
// out-of-range-Position rejection: Position 10 against a 5-point series
// fires PULSE_OVERLAY_REF_UNKNOWN with `series_length: 5`.
func TestOverlay_IndexVsBaseline_OutOfRangeBaselineRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsBaselineSpec("idx_baseline", 10)}

	_, _, err := ApplyOverlaysSeries(specs, host)
	if err == nil {
		t.Fatalf("ApplyOverlaysSeries err = nil; want PULSE_OVERLAY_REF_UNKNOWN")
	}
	assertBaselineIndexCodedError(t, err, 10, len(keys))
}

// TestOverlay_IndexVsBaseline_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. INDEX_VS_BASELINE is
// the windowed positional-baseline kind (no axis dispatch — the baseline
// ordinal is the only knob); the synthesiser surfaces the lower-case
// bare-kind string "index_vs_baseline" mirroring the INDEX_VS_TOTAL /
// SHARE_OF_TOTAL SERIES / INDEX_VS_PRIOR convention.
func TestOverlay_IndexVsBaseline_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}}
	host := newStubSeriesHost(keys, []float64{1.0})
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindIndexVsBaseline,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0},
		},
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "index_vs_baseline"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_IndexVsBaseline_BufferedPath drives Processor.Process
// against the canonical (region, score) integration fixture end-to-end
// to assert the overlay layer is populated and the per-entry indices
// match the resolved baseline. INDEX_VS_BASELINE is buffered (the
// streamability flag is false), so we force the buffered path via
// AGG_MEDIAN to mirror the smoke-test convention used by the sibling-
// family E2E tests.
//
// Fixture row order is alphabetic: east(30), north(150), west(90). With
// Position=0 the east (30) ordinal is the baseline, so:
//
//	east  (30):  30 / 30  * 100 = 100
//	north (150): 150 / 30 * 100 = 500
//	west  (90):  90 / 30  * 100 = 300
func TestOverlay_IndexVsBaseline_BufferedPath(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsBaselineSpec("idx_baseline", 0),
	}, true /*forceBuffered*/)
	if path == PathStreaming {
		t.Fatalf("LastPath() = %s, want non-streaming (INDEX_VS_BASELINE is buffered + AGG_MEDIAN forces buffered)",
			path)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
	}
	// Baseline is east (30); indices = value / 30 * 100.
	want := []float64{
		100.0, // east  — baseline-at-self
		500.0, // north
		300.0, // west
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
}

// TestOverlay_IndexVsBaseline_NilHostReturnsCoded is the defense-in-depth
// nil-host guard: a nil SeriesHostView surfaces the resolver's coded
// PULSE_OVERLAY_REF_UNKNOWN error rather than panicking. The branch is
// unreachable in practice because ApplyOverlaysSeries short-circuits
// empty specs before dispatch, but the defense matches the
// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES / INDEX_VS_PRIOR safety pattern.
func TestOverlay_IndexVsBaseline_NilHostReturnsCoded(t *testing.T) {
	spec := newIndexVsBaselineSpec("idx_baseline", 0)
	_, _, err := applyIndexVsBaseline(&spec, nil)
	if err == nil {
		t.Fatalf("expected coded error for nil host, got nil")
	}
	// The resolver fires its negative/out-of-range arm because
	// host.GroupCount() returns 0 for a nil host and 0 >= 0 trips the
	// out-of-range path with series_length=0.
	assertBaselineIndexCodedError(t, err, 0, 0)
}
