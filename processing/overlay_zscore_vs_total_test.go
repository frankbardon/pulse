package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newZScoreVsTotalSpec returns the canonical happy-path SERIES
// OVERLAY_ZSCORE_VS_TOTAL spec the per-test fixtures consume — GROUP
// scope, empty Ref (implicit-grand-total), and a deterministic name so
// the layer's renderer-facing label is pinned in the assertions.
func newZScoreVsTotalSpec(name string) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindZScoreVsTotal,
		Scope: types.OverlayScopeGroup,
		Ref:   types.OverlayRef{},
	}
}

// classicalPopulationMeanSD is the two-pass classical reference the
// ULP-parity test compares the single-pass Welford handler against.
// First pass: mean = Σ x / N. Second pass: M2 = Σ (x - mean)². SD =
// sqrt(M2 / N) (population). The classical formula is numerically less
// stable than Welford for values of disparate magnitudes but on benign
// inputs (the small, well-conditioned slices the tests feed) the two
// formulae must agree within a handful of ULPs.
func classicalPopulationMeanSD(values []float64) (mean, sd float64) {
	n := len(values)
	if n == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(n)
	if n < 2 {
		return mean, 0
	}
	var ss float64
	for _, v := range values {
		d := v - mean
		ss += d * d
	}
	sd = math.Sqrt(ss / float64(n))
	return mean, sd
}

// TestOverlay_ZScoreVsTotal_BasicSeries pins the canonical happy path:
// three groups with values 1, 2, 3 produce mean = 2, sd =
// sqrt((1+0+1)/3) = sqrt(2/3) ≈ 0.81650. Z-scores: (1-2)/0.81650 ≈
// -1.2247, 0, 1.2247. Tolerance is tight because the math is plain f64
// Welford — the only source of error is floating-point rounding on the
// recurrence + sqrt.
func TestOverlay_ZScoreVsTotal_BasicSeries(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{1.0, 2.0, 3.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	// mean = 2, sd = sqrt(2/3); standardised: -1/sd, 0, 1/sd.
	sd := math.Sqrt(2.0 / 3.0)
	want := []float64{-1.0 / sd, 0.0, 1.0 / sd}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}

	// Layer-level summary: baseline = 0 (distinct from INDEX_VS_TOTAL's
	// baseline = 100 and SHARE_OF_TOTAL SERIES' baseline = 1.0; ZSCORE
	// centres on 0), Count = 3, Min == want[0], Max == want[2].
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 0.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 0.0", got)
	}
	if got, w := *layers[0].Summary.Count, 3; got != w {
		t.Errorf("layer.Summary.Count = %v, want %v", got, w)
	}
	if got := *layers[0].Summary.Min; math.Abs(got-want[0]) > 1e-12 {
		t.Errorf("layer.Summary.Min = %v, want %v", got, want[0])
	}
	if got := *layers[0].Summary.Max; math.Abs(got-want[2]) > 1e-12 {
		t.Errorf("layer.Summary.Max = %v, want %v", got, want[2])
	}

	// Payload shape: SERIES with one entry per host ordinal.
	if got, w := layers[0].Payload.Shape, types.OverlayShapeSeries; got != w {
		t.Errorf("layer.Payload.Shape = %q, want %q", got, w)
	}
	if layers[0].Payload.Series == nil {
		t.Fatalf("layer.Payload.Series nil; want populated")
	}
	if got, w := len(layers[0].Payload.Series.Entries), len(keys); got != w {
		t.Errorf("len(Entries) = %d, want %d (parallel-slice contract)", got, w)
	}
}

// TestOverlay_ZScoreVsTotal_SymmetricDistribution exercises a four-
// group symmetric distribution: 10, 20, 30, 40. mean = 25,
// sd = sqrt((225+25+25+225)/4) = sqrt(125) ≈ 11.18034. Z-scores are
// symmetric around 0 — pin both the magnitude and the symmetry to
// catch sign-flip regressions.
func TestOverlay_ZScoreVsTotal_SymmetricDistribution(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	values := []float64{10.0, 20.0, 30.0, 40.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	// mean = 25, sd = sqrt(125).
	sd := math.Sqrt(125.0)
	want := []float64{(10 - 25) / sd, (20 - 25) / sd, (30 - 25) / sd, (40 - 25) / sd}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}

	// Symmetry contract: z(a) + z(d) ≈ 0 and z(b) + z(c) ≈ 0 because the
	// distribution is symmetric around mean = 25.
	entries := layers[0].Payload.Series.Entries
	sumAD := *entries[0].Summary.Statistic + *entries[3].Summary.Statistic
	if math.Abs(sumAD) > 1e-12 {
		t.Errorf("z(a) + z(d) = %v, want ≈ 0 (symmetric distribution)", sumAD)
	}
	sumBC := *entries[1].Summary.Statistic + *entries[2].Summary.Statistic
	if math.Abs(sumBC) > 1e-12 {
		t.Errorf("z(b) + z(c) = %v, want ≈ 0 (symmetric distribution)", sumBC)
	}
}

// TestOverlay_ZScoreVsTotal_WelfordULP is the explicit ULP-parity test
// the story names: feed values that the naive two-pass classical
// formula can also compute (a benign, well-conditioned distribution)
// and verify the single-pass Welford handler stays within a handful of
// ULPs of the classical reference. Pins the numerical-convention
// contract — if a future refactor accidentally swaps Welford for the
// classical formula (or vice-versa) the cross-mode equivalence tests
// stay green. Tolerance is loose at 1e-10 rather than ULP-tight because
// the classical reference itself accumulates rounding across two passes.
func TestOverlay_ZScoreVsTotal_WelfordULP(t *testing.T) {
	// A handful of values with disparate but not pathological
	// magnitudes — large enough to expose any catastrophic cancellation
	// in the classical formula, small enough that the two formulae
	// still agree to many ULPs.
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}}
	values := []float64{100.5, 200.25, 300.75, 400.125, 500.875, 600.5}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	// Two-pass classical reference.
	classicalMean, classicalSD := classicalPopulationMeanSD(values)

	// Per-entry: compare the handler's z-score against the classical
	// reference within 1e-10.
	for i, v := range values {
		want := (v - classicalMean) / classicalSD
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want, 1e-10)
	}
}

// TestOverlay_ZScoreVsTotal_ZeroVarianceEmitsWarn pins the zero-
// variance contract: when every group value is equal (the every-group-
// equal case — sd legitimately resolves to 0 with N >= 2 present) the
// handler emits ONE PULSE_OVERLAY_REF_ZERO warning and every present
// group entry carries a NaN Statistic. Mirrors the INDEX_VS_TOTAL /
// SHARE_OF_TOTAL SERIES zero-grand-total emission shape (one warning
// per layer, not per cell).
func TestOverlay_ZScoreVsTotal_ZeroVarianceEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{42.0, 42.0, 42.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

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
			t.Fatalf("entry[%d].Summary.Statistic nil; want NaN (zero variance path emits NaN per present entry)",
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

// TestOverlay_ZScoreVsTotal_AllZerosEmitsWarn exercises the every-
// group-zero degenerate case: every value is exactly 0 so mean = 0 and
// sd = 0. Same emission shape as the every-group-equal case: ONE
// PULSE_OVERLAY_REF_ZERO warning and NaN statistics across present
// entries.
func TestOverlay_ZScoreVsTotal_AllZerosEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{0.0, 0.0, 0.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)",
			len(entries), len(keys))
	}
	for i, entry := range entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entry[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*entry.Summary.Statistic) {
			t.Fatalf("entry[%d].Summary.Statistic = %v, want NaN", i,
				*entry.Summary.Statistic)
		}
	}
}

// TestOverlay_ZScoreVsTotal_SinglePresentGroupEmitsWarn exercises the
// single-present-group degenerate case: N < 2 leaves the Welford SD at
// the helper's zero default so the handler emits ONE
// PULSE_OVERLAY_REF_ZERO warning and the lone present entry carries a
// NaN Statistic. The remaining absent entries stay absent (nil
// Statistic) under the absent-group contract.
func TestOverlay_ZScoreVsTotal_SinglePresentGroupEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{42.0, math.NaN(), math.NaN()} // only "a" is present
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)",
			len(entries), len(keys))
	}
	// "a" is present: NaN statistic under the zero-variance path.
	if entries[0].Summary.Statistic == nil {
		t.Fatalf("entries[0].Summary.Statistic nil; want NaN")
	}
	if !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Errorf("entries[0].Summary.Statistic = %v, want NaN (single-present-group degenerate)",
			*entries[0].Summary.Statistic)
	}
	// "b", "c" are absent: nil Statistic.
	for i := 1; i < 3; i++ {
		if entries[i].Summary.Statistic != nil {
			t.Errorf("entries[%d].Summary.Statistic = %v, want nil (absent group)",
				i, *entries[i].Summary.Statistic)
		}
	}
}

func TestOverlay_ZScoreVsTotal_AbsentGroupsStayAbsent(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	// NaN signals absent (newStubSeriesHost's resolver returns
	// (0, false) for NaN inputs).
	values := []float64{10.0, math.NaN(), 20.0, math.NaN(), 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

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

	// Present groups: Welford fold over (10, 20, 30) — mean = 20,
	// sd = sqrt(200/3).
	sd := math.Sqrt(200.0 / 3.0)
	wantA := (10.0 - 20.0) / sd
	wantC := 0.0
	wantE := (30.0 - 20.0) / sd
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, wantA, 1e-12)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, wantC, 1e-12)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 4, wantE, 1e-12)

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

func TestOverlay_ZScoreVsTotal_StreamingBufferedByteIdentical(t *testing.T) {
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

	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

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

// TestOverlay_ZScoreVsTotal_ZeroGroupsClean exercises the degenerate
// zero-group host (every group filtered out, or no groupers at all
// surfacing through the SERIES host's structural shape). The handler
// must not panic; it surfaces an empty Entries slice + a warning
// because N == 0 means sd == 0 in the every-group-absent case.
//
// This is the edge-case companion to
// TestOverlay_ZScoreVsTotal_ZeroVarianceEmitsWarn — the warning fires
// for the same reason (zero denominator / sd) but the entries slice is
// empty rather than full-of-NaN.
func TestOverlay_ZScoreVsTotal_ZeroGroupsClean(t *testing.T) {
	host := newStubSeriesHost(nil, nil)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

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

// TestOverlay_ZScoreVsTotal_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. ZSCORE_VS_TOTAL is
// implicit-grand-total (no Ref family); the synthesiser surfaces the
// lower-case bare-kind string "zscore_vs_total" mirroring the
// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES family.
func TestOverlay_ZScoreVsTotal_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHost(keys, []float64{1.0, 2.0})
	// No Name on the spec — exercises the synthesiser.
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindZScoreVsTotal,
		Scope: types.OverlayScopeGroup,
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "zscore_vs_total"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_ZScoreVsTotal_MeanZero pins the per-entry math when the
// distribution itself is mean-zero: values -2, -1, 0, 1, 2 produce
// mean = 0 and sd = sqrt(10/5) = sqrt(2). The z-scores are exactly the
// values divided by sqrt(2). Establishes that the mean-subtraction step
// is honoured (a regression that omits it would emit values / sd
// regardless of the mean's sign, which would happen to pass this test
// only when mean is genuinely zero — the BasicSeries test catches the
// non-zero-mean case).
func TestOverlay_ZScoreVsTotal_MeanZero(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	values := []float64{-2.0, -1.0, 0.0, 1.0, 2.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsTotalSpec("zscore_total")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	sd := math.Sqrt(2.0)
	want := []float64{-2.0 / sd, -1.0 / sd, 0.0, 1.0 / sd, 2.0 / sd}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}
}
