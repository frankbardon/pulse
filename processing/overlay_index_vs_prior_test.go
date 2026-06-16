package processing

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newIndexVsPriorSpec returns the canonical happy-path
// OVERLAY_INDEX_VS_PRIOR spec the per-test fixtures consume — GROUP
// scope, empty Ref (the implicit-default authoring shape for the lag-1
// windowed kind), and a deterministic name so the layer's renderer-
// facing label is pinned in the assertions.
func newIndexVsPriorSpec(name string) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindIndexVsPrior,
		Scope: types.OverlayScopeGroup,
		Ref:   types.OverlayRef{},
	}
}

// TestOverlay_IndexVsPrior_BasicLag1 pins the canonical happy path:
// five points [10, 20, 30, 40, 50] produce indices
// [NaN, 200, 150, 133.33..., 125]. First point has no prior → NaN
// (without warning). Subsequent points emit the lag-1 ratio scaled to
// 100.
func TestOverlay_IndexVsPrior_BasicLag1(t *testing.T) {
	keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}, {"apr"}, {"may"}}
	values := []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}

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

	// First entry: NaN — no prior available, no warning.
	if entries[0].Summary.Statistic == nil {
		t.Fatalf("entries[0].Summary.Statistic nil; want NaN (first-point semantics)")
	}
	if !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (first-point semantics)",
			*entries[0].Summary.Statistic)
	}

	// Subsequent entries: 20/10*100, 30/20*100, 40/30*100, 50/40*100.
	const tol = 1e-9
	wants := []float64{
		20.0 / 10.0 * 100.0,
		30.0 / 20.0 * 100.0,
		40.0 / 30.0 * 100.0,
		50.0 / 40.0 * 100.0,
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i+1, w, tol)
	}

	// Layer-level summary: baseline = 100, Count = 4 (the 4 non-NaN
	// entries — the first-point NaN does not contribute).
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

// TestOverlay_IndexVsPrior_FirstPointNaN pins the single-point host
// degenerate case: one present ordinal emits NaN (no prior available)
// with NO warnings. PULSE_OVERLAY_REF_ZERO is NOT emitted on this
// branch — "no comparison" is structurally distinct from "denominator
// was zero".
func TestOverlay_IndexVsPrior_FirstPointNaN(t *testing.T) {
	keys := []types.AxisKey{{"only"}}
	values := []float64{42.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	// First-point semantics: no warning because "no comparison available"
	// is NOT a zero-denominator path.
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(entries))
	}
	if entries[0].Summary.Statistic == nil {
		t.Fatalf("entries[0].Summary.Statistic nil; want NaN (first-point semantics)")
	}
	if !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN", *entries[0].Summary.Statistic)
	}

	// Layer-level summary: Count = 0 (no non-NaN entries seen).
	if got, want := *layers[0].Summary.Count, 0; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_IndexVsPrior_PriorZeroEmitsRefZero pins the zero-prior
// path: [0, 5] produces [NaN, NaN] with ONE PULSE_OVERLAY_REF_ZERO
// warning. The first entry is NaN because no prior exists yet (no
// warning); the second entry is NaN because the prior carrier is 0 at
// the divide step (the PULSE_OVERLAY_REF_ZERO branch).
func TestOverlay_IndexVsPrior_PriorZeroEmitsRefZero(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	values := []float64{0.0, 5.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(entries))
	}
	// entry[0]: NaN (first present, no prior). entry[1]: NaN (prior == 0).
	for i, entry := range entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*entry.Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i,
				*entry.Summary.Statistic)
		}
	}
}

// TestOverlay_IndexVsPrior_AbsentPointPreservesPriorCarrier exercises
// the single-state lag's "absent point does not advance the carrier"
// rule: [10, absent, 30] produces [NaN, NaN, 300]. The middle ordinal
// is absent so the lag carrier stays at 10; the third ordinal then
// computes 30/10*100 = 300.
//
// math.NaN signals absent (newStubSeriesHost's resolver returns
// (0, false) for NaN inputs).
func TestOverlay_IndexVsPrior_AbsentPointPreservesPriorCarrier(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, math.NaN(), 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3", len(entries))
	}
	// entry[0]: NaN (first present, no prior).
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (first point)",
			entries[0].Summary.Statistic)
	}
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1].Summary.Statistic = %v, want nil (absent group)",
			*entries[1].Summary.Statistic)
	}
	// entry[2]: 30/10*100 = 300 (lag carrier survived the absent slot).
	const want = 300.0
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, want, 1e-9)
}

// TestOverlay_IndexVsPrior_RefPriorPopulatedAccepted verifies the
// `Ref.Prior` populated authoring shape produces byte-identical output
// to the empty-Ref shape — both are the lag-1 implicit-default
// authoring spellings.
func TestOverlay_IndexVsPrior_RefPriorPopulatedAccepted(t *testing.T) {
	keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}}
	values := []float64{10.0, 20.0, 40.0}
	host := newStubSeriesHost(keys, values)

	emptyRefSpec := newIndexVsPriorSpec("idx_prior_a")
	priorRefSpec := types.OverlaySpec{
		Name:  "idx_prior_b",
		Kind:  types.OverlayKindIndexVsPrior,
		Scope: types.OverlayScopeGroup,
		Ref:   types.OverlayRef{Prior: &types.OverlayPriorRef{}},
	}

	emptyLayers, _, err := ApplyOverlaysSeries([]types.OverlaySpec{emptyRefSpec}, host)
	if err != nil {
		t.Fatalf("emptyRef ApplyOverlaysSeries: %v", err)
	}
	priorLayers, _, err := ApplyOverlaysSeries([]types.OverlaySpec{priorRefSpec}, host)
	if err != nil {
		t.Fatalf("priorRef ApplyOverlaysSeries: %v", err)
	}

	// Per-entry Statistic must match element-for-element. We compare the
	// Statistic floats directly because the layer-level Name / Ref echoes
	// will differ (different specs).
	emptyEntries := emptyLayers[0].Payload.Series.Entries
	priorEntries := priorLayers[0].Payload.Series.Entries
	if len(emptyEntries) != len(priorEntries) {
		t.Fatalf("entry count differs: emptyRef=%d priorRef=%d",
			len(emptyEntries), len(priorEntries))
	}
	for i := range emptyEntries {
		e, p := emptyEntries[i].Summary.Statistic, priorEntries[i].Summary.Statistic
		switch {
		case e == nil && p == nil:
			// both absent — fine
		case e == nil || p == nil:
			t.Fatalf("entry[%d] Statistic presence differs: empty=%v prior=%v",
				i, e, p)
		default:
			if math.IsNaN(*e) && math.IsNaN(*p) {
				continue
			}
			if *e != *p {
				t.Fatalf("entry[%d] Statistic differs: empty=%v prior=%v",
					i, *e, *p)
			}
		}
	}
}

// TestOverlay_IndexVsPrior_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. INDEX_VS_PRIOR is the
// windowed lag-1 kind (no axis dispatch — the ordered host axis is
// fixed); the synthesiser surfaces the lower-case bare-kind string
// "index_vs_prior" mirroring the INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES
// dispatch convention.
func TestOverlay_IndexVsPrior_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}}
	host := newStubSeriesHost(keys, []float64{1.0})
	// No Name on the spec — exercises the synthesiser.
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindIndexVsPrior,
		Scope: types.OverlayScopeGroup,
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "index_vs_prior"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_IndexVsPrior_Streaming is the smoke test the story spec
// requires: run a small grouped Process through both the streaming path
// AND the buffered path, assert the SERIES overlay layer is byte-
// equivalent. Mirrors the TestOverlay_IndexVsTotal_Streaming_E2E pattern
// (overlay_process_integration_test.go) but exercises the windowed lag-
// 1 handler.
//
// We reuse the canonical (region, score) integration fixture
// (overlayIntegrationSchema + overlayIntegrationRecords) — three regions
// {east, west, north} with per-region AGG_SUM(score) = {30, 90, 150}.
// Fixture row order is alphabetic: east(30), north(150), west(90).
//
// INDEX_VS_PRIOR expected output walking the ordered axis:
//
//	east (30):  NaN      (first point, no prior)
//	north (150): 150/30*100  = 500
//	west (90):   90/150*100  = 60
//
// Both the streaming-eligible (AGG_SUM only) and buffered (with
// AGG_MEDIAN forcing the buffered path) variants must produce
// byte-identical SeriesPayload output.
func TestOverlay_IndexVsPrior_Streaming(t *testing.T) {
	streamResp, streamPath := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsPriorSpec("idx_prior"),
	}, false /*forceBuffered*/)
	if streamPath != PathStreaming {
		t.Fatalf("streaming variant LastPath() = %s, want %s (INDEX_VS_PRIOR is streamable)",
			streamPath, PathStreaming)
	}
	bufferedResp, bufferedPath := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsPriorSpec("idx_prior"),
	}, true /*forceBuffered*/)
	if bufferedPath == PathStreaming {
		t.Fatalf("buffered variant LastPath() = %s, want non-streaming (forced via AGG_MEDIAN)",
			bufferedPath)
	}

	if len(streamResp.Overlays) != 1 || len(bufferedResp.Overlays) != 1 {
		t.Fatalf("Overlays count: streaming=%d buffered=%d, want 1 each",
			len(streamResp.Overlays), len(bufferedResp.Overlays))
	}

	// Expected statistics in alphabetic group-key order (east, north, west).
	wantStats := []float64{
		math.NaN(),           // east (first present, no prior)
		150.0 / 30.0 * 100.0, // north  / east  * 100 = 500
		90.0 / 150.0 * 100.0, // west   / north * 100 = 60
	}
	assertOverlayLayerStatistics(t, &streamResp.Overlays[0], wantStats)
	assertOverlayLayerStatistics(t, &bufferedResp.Overlays[0], wantStats)

	// Byte-equivalence of the SeriesPayload entries (Key + Summary) across
	// the two paths. reflect.DeepEqual handles AxisKey + Summary
	// (including pointer-to-NaN), so use a wider hand-rolled compare for
	// the Statistic floats to honour the NaN equivalence convention.
	streamEntries := streamResp.Overlays[0].Payload.Series.Entries
	bufferedEntries := bufferedResp.Overlays[0].Payload.Series.Entries
	if len(streamEntries) != len(bufferedEntries) {
		t.Fatalf("entry count differs: streaming=%d buffered=%d",
			len(streamEntries), len(bufferedEntries))
	}
	for i := range streamEntries {
		if !reflect.DeepEqual(streamEntries[i].Key, bufferedEntries[i].Key) {
			t.Fatalf("entry[%d] Key differs: streaming=%v buffered=%v",
				i, streamEntries[i].Key, bufferedEntries[i].Key)
		}
		s, b := streamEntries[i].Summary.Statistic, bufferedEntries[i].Summary.Statistic
		switch {
		case s == nil && b == nil:
			// both absent — fine
		case s == nil || b == nil:
			t.Fatalf("entry[%d] Statistic presence differs: streaming=%v buffered=%v",
				i, s, b)
		default:
			if math.IsNaN(*s) && math.IsNaN(*b) {
				continue
			}
			if *s != *b {
				t.Fatalf("entry[%d] Statistic differs: streaming=%v buffered=%v",
					i, *s, *b)
			}
		}
	}
}

// Defense-in-depth: a nil host must surface a coded
// PROCESSING_INTERNAL error rather than panicking. The branch is
// unreachable in practice because ApplyOverlaysSeries short-circuits
// empty specs before dispatch, but the defense matches the
// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES safety pattern.
func TestOverlay_IndexVsPrior_NilHostReturnsCoded(t *testing.T) {
	spec := newIndexVsPriorSpec("idx_prior")
	_, _, err := applyIndexVsPrior(&spec, nil)
	if err == nil {
		t.Fatalf("expected coded error for nil host, got nil")
	}
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("err type = %T, want *errors.CodedError", err)
	}
	if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE); got != want {
		t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
	}
	_ = context.Background // keep imports stable across small refactors
}
