package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_INDEX_VS_TOTAL handler
// (processing/overlay_index_vs_total.go).
//
// E3-S2 scope:
//
//   - First registered SERIES-host overlay kind. The handler is wired
//     into seriesOverlayHandlers via the dispatch entry in
//     processing/overlay_series.go.
//   - The acceptance criteria call for: basic series math, zero-grand-
//     total emits PULSE_OVERLAY_REF_ZERO + NaN, absent-group passthrough
//     stays absent, streaming-vs-buffered byte-identity at the post-
//     finalize entry point.
//   - Tests reuse the SeriesHostView fixtures from
//     overlay_series_test.go (newStubSeriesHost) where possible.

// newIndexVsTotalSpec returns the canonical happy-path
// OVERLAY_INDEX_VS_TOTAL spec the per-test fixtures consume — GROUP
// scope, empty Ref (implicit-grand-total), and a deterministic name so
// the layer's renderer-facing label is pinned in the assertions.
func newIndexVsTotalSpec(name string) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindIndexVsTotal,
		Scope: types.OverlayScopeGroup,
		Ref:   types.OverlayRef{},
	}
}

// TestOverlay_IndexVsTotal_BasicSeries pins the canonical happy path:
// three groups with equal values share the grand total, so each
// surfaces an index of (1/3) * 100 ≈ 33.333%. Tolerance is tight
// because the math is plain f64 division — the only source of error is
// floating-point rounding on the divide step.
func TestOverlay_IndexVsTotal_BasicSeries(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}}
	values := []float64{100.0, 100.0, 100.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsTotalSpec("idx_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	const want = 100.0 / 3.0 // ≈ 33.3333%
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want, 1e-9)
	}
	// Layer-level summary: baseline = 100, Count = 3, Min == Max == want
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 100.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 100", got)
	}
	if got, want := *layers[0].Summary.Count, 3; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
	if got := *layers[0].Summary.Min; math.Abs(got-want) > 1e-9 {
		t.Errorf("layer.Summary.Min = %v, want %v", got, want)
	}
	if got := *layers[0].Summary.Max; math.Abs(got-want) > 1e-9 {
		t.Errorf("layer.Summary.Max = %v, want %v", got, want)
	}
}

// TestOverlay_IndexVsTotal_UnequalGroups exercises the non-uniform
// case: three groups with values 50 / 30 / 20 sum to 100 so the per-
// group indices are exactly 50% / 30% / 20%. Verifies the per-entry
// math without divisor-induced rounding.
func TestOverlay_IndexVsTotal_UnequalGroups(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{50.0, 30.0, 20.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsTotalSpec("idx_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	want := []float64{50.0, 30.0, 20.0}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-9)
	}
}

// TestOverlay_IndexVsTotal_ZeroGrandTotalEmitsWarn pins the zero-
// grand-total contract: when every group value sums to zero the
// handler emits ONE PULSE_OVERLAY_REF_ZERO warning and every present
// group entry carries a NaN Statistic. Mirrors the share / index
// family's missing-margin emission shape (one warning per layer, not
// per cell).
func TestOverlay_IndexVsTotal_ZeroGrandTotalEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{0.0, 0.0, 0.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsTotalSpec("idx_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	// Every present entry carries a NaN Statistic — Statistic pointer
	// is non-nil but the float is NaN.
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)",
			len(entries), len(keys))
	}
	for i, entry := range entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entry[%d].Summary.Statistic nil; want NaN (zero grand_total path emits NaN per present entry)",
				i)
		}
		if !math.IsNaN(*entry.Summary.Statistic) {
			t.Fatalf("entry[%d].Summary.Statistic = %v, want NaN", i,
				*entry.Summary.Statistic)
		}
	}

	// Layer-level summary: zero count (no non-NaN entries seen).
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated with Count=0")
	}
	if got, want := *layers[0].Summary.Count, 0; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_IndexVsTotal_AbsentGroupsStayAbsent exercises the
// absent-group contract: groups for which the resolver reports
// (0, false) surface a SeriesEntry whose Summary leaves Statistic
// unset (the canonical "present slot, empty summary" shape from
// E3-S1). Absent groups do NOT contribute to the grand total.
//
// Fixture: groups [a=100, b=absent, c=200, d=absent]. The grand total
// is 100 + 200 = 300 (absent groups excluded). Present entries:
// a → 100/300 ≈ 33.333; c → 200/300 ≈ 66.667. Absent entries: nil
// Statistic.
func TestOverlay_IndexVsTotal_AbsentGroupsStayAbsent(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	// math.NaN signals absent (newStubSeriesHost's resolver returns
	// (0, false) for NaN inputs).
	values := []float64{100.0, math.NaN(), 200.0, math.NaN()}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsTotalSpec("idx_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)",
			len(entries), len(keys))
	}

	// Present groups: 100/300 ≈ 33.333%, 200/300 ≈ 66.667%.
	const wantA = 100.0 / 300.0 * 100.0
	const wantC = 200.0 / 300.0 * 100.0
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, wantA, 1e-9)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, wantC, 1e-9)

	// Absent groups: Statistic pointer is nil (the canonical "present
	// slot, empty summary" shape from E3-S1).
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1].Summary.Statistic = %v, want nil (absent group)",
			*entries[1].Summary.Statistic)
	}
	if entries[3].Summary.Statistic != nil {
		t.Errorf("entries[3].Summary.Statistic = %v, want nil (absent group)",
			*entries[3].Summary.Statistic)
	}

	// Key alignment: every entry's Key still matches the host ordinal
	// element-for-element (parallel-slice contract; absent groups DO
	// carry the key, only the metric is missing).
	for i, want := range keys {
		got := entries[i].Key
		if len(got) != len(want) {
			t.Fatalf("entries[%d].Key length = %d, want %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("entries[%d].Key[%d] = %v, want %v", i, j, got[j], want[j])
			}
		}
	}
}

// TestOverlay_IndexVsTotal_StreamingBufferedByteIdentical pins the
// streaming-vs-buffered byte-identity contract the E3-S2 acceptance
// names: the post-host-finalize entry point (ApplyOverlaysSeries
// dispatch) produces byte-identical SeriesPayload output whether the
// host was built via a streaming Process pass or a buffered one,
// because in both cases the handler consumes the same finalised
// SeriesHostView.
//
// We model the two paths by constructing two SeriesHostView instances
// with IDENTICAL finalised group keys + values but distinct internal
// resolvers — one returns values from a buffered slice (the buffered
// path's classic shape) and the other returns the same values via a
// closure that mimics a streaming-Process finalize hook (a snapshot
// of the streamingly-built map). The handler must not branch on
// resolver identity — both paths feed the same handler entry point
// and the output must be structurally equivalent.
func TestOverlay_IndexVsTotal_StreamingBufferedByteIdentical(t *testing.T) {
	keys := []types.AxisKey{{"x"}, {"y"}, {"z"}}
	values := []float64{12.0, 34.0, 56.0}

	// Buffered host: classic resolver over the values slice.
	bufHost := newStubSeriesHost(keys, values)

	// "Streaming" host: same group keys, same values, but the resolver
	// is a snapshot-style closure that mirrors the shape a streaming
	// orchestrator would surface at finalize time (a map keyed by host
	// ordinal). The point is to assert the handler's output is purely
	// a function of (keys, values) — not the resolver's identity.
	finalizedMap := map[int]float64{0: 12.0, 1: 34.0, 2: 56.0}
	streamHost := NewSeriesHostView(keys, func(i int) (float64, bool) {
		v, ok := finalizedMap[i]
		return v, ok
	})

	specs := []types.OverlaySpec{newIndexVsTotalSpec("idx_total")}

	bufLayers, bufWarnings, err := ApplyOverlaysSeries(specs, bufHost)
	if err != nil {
		t.Fatalf("buffered ApplyOverlaysSeries: %v", err)
	}
	streamLayers, streamWarnings, err := ApplyOverlaysSeries(specs, streamHost)
	if err != nil {
		t.Fatalf("streaming ApplyOverlaysSeries: %v", err)
	}

	// Warnings: byte-identical (both empty).
	if len(bufWarnings) != len(streamWarnings) {
		t.Fatalf("warning slice lengths differ: buffered=%d streaming=%d",
			len(bufWarnings), len(streamWarnings))
	}

	// Layers: byte-identical.
	if len(bufLayers) != len(streamLayers) {
		t.Fatalf("layer slice lengths differ: buffered=%d streaming=%d",
			len(bufLayers), len(streamLayers))
	}
	for li := range bufLayers {
		bufEntries := bufLayers[li].Payload.Series.Entries
		streamEntries := streamLayers[li].Payload.Series.Entries
		if len(bufEntries) != len(streamEntries) {
			t.Fatalf("layer[%d] entry counts differ: buffered=%d streaming=%d",
				li, len(bufEntries), len(streamEntries))
		}
		for ei := range bufEntries {
			bufStat := bufEntries[ei].Summary.Statistic
			streamStat := streamEntries[ei].Summary.Statistic
			switch {
			case bufStat == nil && streamStat == nil:
				// both absent — fine
			case bufStat == nil || streamStat == nil:
				t.Fatalf("layer[%d].entry[%d] Statistic presence differs: buffered=%v streaming=%v",
					li, ei, bufStat, streamStat)
			default:
				if *bufStat != *streamStat {
					t.Fatalf("layer[%d].entry[%d] Statistic differs: buffered=%v streaming=%v",
						li, ei, *bufStat, *streamStat)
				}
			}
		}
		// Layer-level Summary: byte-identical scalar fields.
		if (bufLayers[li].Summary == nil) != (streamLayers[li].Summary == nil) {
			t.Fatalf("layer[%d].Summary presence differs: buffered=%v streaming=%v",
				li, bufLayers[li].Summary, streamLayers[li].Summary)
		}
		if bufLayers[li].Summary != nil {
			if *bufLayers[li].Summary.Baseline != *streamLayers[li].Summary.Baseline {
				t.Fatalf("layer[%d].Summary.Baseline differs: buffered=%v streaming=%v",
					li, *bufLayers[li].Summary.Baseline, *streamLayers[li].Summary.Baseline)
			}
			if *bufLayers[li].Summary.Count != *streamLayers[li].Summary.Count {
				t.Fatalf("layer[%d].Summary.Count differs: buffered=%v streaming=%v",
					li, *bufLayers[li].Summary.Count, *streamLayers[li].Summary.Count)
			}
		}
	}
}

// TestOverlay_IndexVsTotal_ZeroGroupsClean exercises the degenerate
// zero-group host (every group filtered out, or no groupers at all
// surfacing through the SERIES host's structural shape). The handler
// must not panic; it surfaces an empty Entries slice + a warning
// because grand_total == 0 in the every-group-absent case.
//
// This is the edge-case companion to
// TestOverlay_IndexVsTotal_ZeroGrandTotalEmitsWarn — the warning fires
// for the same reason (zero denominator) but the entries slice is
// empty rather than full-of-NaN.
func TestOverlay_IndexVsTotal_ZeroGroupsClean(t *testing.T) {
	host := newStubSeriesHost(nil, nil)
	specs := []types.OverlaySpec{newIndexVsTotalSpec("idx_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	if got := len(layers[0].Payload.Series.Entries); got != 0 {
		t.Errorf("len(Entries) = %d, want 0 (zero-group host)", got)
	}
}

// TestOverlay_IndexVsTotal_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. INDEX_VS_TOTAL is
// implicit-grand-total (no Ref family); the synthesiser surfaces the
// lower-case bare-kind string "index_vs_total" mirroring the
// CHISQ_* / FISHER_EXACT_CELL family.
func TestOverlay_IndexVsTotal_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}}
	host := newStubSeriesHost(keys, []float64{1.0})
	// No Name on the spec — exercises the synthesiser.
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindIndexVsTotal,
		Scope: types.OverlayScopeGroup,
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "index_vs_total"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// assertSeriesEntryStatisticWithinTol is a narrower helper than the
// shared assertSeriesEntryWithinTol — INDEX_VS_TOTAL emits only the
// Statistic field on each entry (no PValue, no Parameters), so the
// joint statistic+p-value assertion would fail on a missing PValue.
// Centralising the per-entry Statistic check here keeps the per-test
// fixtures readable without duplicating the failure-mode shape.
func assertSeriesEntryStatisticWithinTol(t *testing.T, layer *types.OverlayLayer, axisIdx int, want, tol float64) {
	t.Helper()
	if layer == nil {
		t.Fatalf("assertSeriesEntryStatisticWithinTol: layer is nil")
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("assertSeriesEntryStatisticWithinTol: layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("assertSeriesEntryStatisticWithinTol: layer.Payload.Series is nil")
	}
	entries := layer.Payload.Series.Entries
	if axisIdx < 0 || axisIdx >= len(entries) {
		t.Fatalf("assertSeriesEntryStatisticWithinTol: axisIdx=%d out of range [0,%d)",
			axisIdx, len(entries))
	}
	entry := entries[axisIdx]
	if entry.Summary.Statistic == nil {
		t.Fatalf("assertSeriesEntryStatisticWithinTol: entry[%d].Summary.Statistic is nil; want %v ± %v",
			axisIdx, want, tol)
	}
	if math.Abs(*entry.Summary.Statistic-want) > tol {
		t.Fatalf("assertSeriesEntryStatisticWithinTol: entry[%d].Summary.Statistic = %v, want %v ± %v",
			axisIdx, *entry.Summary.Statistic, want, tol)
	}
}
