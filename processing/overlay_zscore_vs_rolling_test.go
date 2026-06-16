package processing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newZScoreVsRollingSpec returns the canonical happy-path
// OVERLAY_ZSCORE_VS_ROLLING spec the per-test fixtures consume — GROUP
// scope, `Ref.RollingMean` populated as the empty marker, a caller-
// supplied `window` value, and a deterministic Name so the renderer-
// facing label is pinned in the assertions.
func newZScoreVsRollingSpec(name string, window int) types.OverlaySpec {
	params, _ := json.Marshal(map[string]any{"window": window})
	return types.OverlaySpec{
		Name:   name,
		Kind:   types.OverlayKindZScoreVsRolling,
		Scope:  types.OverlayScopeGroup,
		Ref:    types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
		Params: params,
	}
}

// TestOverlay_ZScoreVsRolling_Basic_Window5 pins the canonical happy
// path: a 20-point series with window=5 produces z-scores at known
// indices. The first two entries carry NaN (carrier `count < 2` — the
// Welford recurrence requires at least two observations to define a
// sample variance). From the third entry onward each point's z-score
// is `(point - rolling_mean(prior_W_present)) / rolling_sample_sd`.
//
// At each index `i >= 2` the carrier has been pushed `min(i, W)` prior
// PRESENT values, so the carrier surfaces the rolling sample SD across
// those prior observations. For i=2 the carrier holds {10, 20}, mean=15,
// M2=50, sample SD = sqrt(50/1) ≈ 7.07107, z = (30 - 15) / 7.07107 ≈
// 2.12132. For i=5 the carrier holds {10, 20, 30, 40, 50} (window full
// since i=4), mean=30, M2 = (10-30)² + (20-30)² + (30-30)² + (40-30)² +
// (50-30)² = 1000, sample SD = sqrt(1000/4) ≈ 15.8113883, z = (60 - 30)
// / 15.8113883 ≈ 1.8973666. For i=10 the carrier holds {60..100}
// (5-element window over the last five present priors), mean=80,
// M2=1000, sample SD ≈ 15.8113883, z = (110 - 80) / 15.8113883 ≈
// 1.8973666 — same z-score because the rolling window has shifted but
// the distribution shape is identical.
func TestOverlay_ZScoreVsRolling_Basic_Window5(t *testing.T) {
	keys := make([]types.AxisKey, 20)
	values := make([]float64, 20)
	for i := 0; i < 20; i++ {
		keys[i] = types.AxisKey{"k" + string(rune('a'+i))}
		values[i] = float64((i + 1) * 10) // 10, 20, 30, ..., 200
	}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsRollingSpec("zscore_rolling", 5)}

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

	// First two entries: NaN — carrier count < 2 at the divide step (need
	// at least 2 prior PRESENT points for a sample SD).
	for i := 0; i < 2; i++ {
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN (carrier count < 2)", i)
		}
		if !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN (carrier count < 2)",
				i, *entries[i].Summary.Statistic)
		}
	}

	// Entry at index 2 — carrier holds {10, 20}; mean=15, M2=50, sample
	// SD = sqrt(50/1) = sqrt(50) ≈ 7.0710678. z = (30 - 15) / sqrt(50)
	// ≈ 2.1213203.
	const tol = 1e-9
	want2 := (30.0 - 15.0) / math.Sqrt(50.0)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, want2, tol)

	// Entry at index 5 — carrier holds {10, 20, 30, 40, 50}; mean=30,
	// M2=1000, sample SD = sqrt(1000/4) = sqrt(250). z = (60 - 30) /
	// sqrt(250).
	want5 := (60.0 - 30.0) / math.Sqrt(250.0)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 5, want5, tol)

	// Entry at index 10 — carrier holds {60..100}; mean=80, M2=1000,
	// sample SD = sqrt(1000/4) = sqrt(250). z = (110 - 80) / sqrt(250).
	want10 := (110.0 - 80.0) / math.Sqrt(250.0)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 10, want10, tol)

	// Layer-level summary: baseline = 0 (mirrors ZSCORE family). Count =
	// 18 (the 18 non-NaN entries — first two are NaN).
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 0.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 0 (ZSCORE family centred on 0)", got)
	}
	if got, want := *layers[0].Summary.Count, 18; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_ZScoreVsRolling_WindowExceedsSeries pins the degenerate
// case: a 10-point series with window=20 — the carrier never fills the
// window, but more importantly the carrier count climbs 0, 1, 2, 3, ...,
// 9 across the divide steps. Entry 0 and 1 emit NaN (count < 2); from
// entry 2 onward the carrier has at least 2 priors and the z-score is
// computed against the running sample SD over all observed priors (the
// window cap of 20 is never reached so every prior present point is in
// the ring). Acceptance: every entry's Statistic is set; the first two
// are NaN; the rest are finite numbers.
//
// Note: this test deliberately uses the "window > series length" shape
// the story called out, but our handler's count<2 rule makes the first
// two entries NaN regardless — there is no separate "window not yet
// filled" arm because sample SD only requires count >= 2, not count ==
// W. This is structurally distinct from INDEX_VS_ROLLING_MEAN which
// requires the FULL window to be filled before computing the mean.
func TestOverlay_ZScoreVsRolling_WindowExceedsSeries(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"},
		{"f"}, {"g"}, {"h"}, {"i"}, {"j"}}
	values := []float64{10.0, 20.0, 30.0, 40.0, 50.0,
		60.0, 70.0, 80.0, 90.0, 100.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsRollingSpec("zscore_rolling", 20)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 10 {
		t.Fatalf("len(Entries) = %d, want 10", len(entries))
	}

	// First two entries: NaN — carrier count < 2.
	for i := 0; i < 2; i++ {
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i,
				*entries[i].Summary.Statistic)
		}
	}
	// Remaining entries: finite (the window cap of 20 is never hit so
	// the ring grows without evicting — sample SD becomes well-defined
	// as soon as count >= 2).
	for i := 2; i < 10; i++ {
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want finite z-score", i)
		}
		if math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = NaN; want finite z-score (carrier count >= 2)", i)
		}
	}
}

// TestOverlay_ZScoreVsRolling_CountLessThanTwoEmitsNaN pins the
// canonical "first two points emit NaN" rule. Sample variance requires
// at least 2 observations (the n-1 denominator is undefined for n=1);
// the handler emits NaN at both the count=0 evaluation (first present
// point — no priors) AND the count=1 evaluation (second present point
// — only one prior). No warning fires for either path because "no
// comparison available" is structurally distinct from "denominator was
// zero".
func TestOverlay_ZScoreVsRolling_CountLessThanTwoEmitsNaN(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 20.0, 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsRollingSpec("zscore_rolling", 5)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	// No warnings — "no comparison available" path emits NaN without
	// raising PULSE_OVERLAY_REF_ZERO.
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	// Entry 0: count == 0 at divide step (no priors) → NaN.
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (count == 0)",
			entries[0].Summary.Statistic)
	}
	// Entry 1: count == 1 at divide step (one prior) → NaN.
	if entries[1].Summary.Statistic == nil || !math.IsNaN(*entries[1].Summary.Statistic) {
		t.Fatalf("entries[1].Summary.Statistic = %v, want NaN (count == 1)",
			entries[1].Summary.Statistic)
	}
	// Entry 2: count == 2 at divide step → finite z-score.
	// mean = 15, M2 = 50, sample SD = sqrt(50) ≈ 7.071, z = (30-15)/7.071.
	const tol = 1e-9
	want2 := (30.0 - 15.0) / math.Sqrt(50.0)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, want2, tol)
}

// TestOverlay_ZScoreVsRolling_ZeroRollingSDEmitsRefZero pins the
// zero-SD path: a constant series [5, 5, 5, 5, 5] with window=3 — at
// i=2 the carrier holds {5, 5}, mean=5, M2=0, sample SD=0 → NaN +
// PULSE_OVERLAY_REF_ZERO. Subsequent entries (i=3, 4) continue to hit
// the zero-SD path because the ring rolls but the values stay constant;
// the layer still emits ONE warning per layer (not per entry).
func TestOverlay_ZScoreVsRolling_ZeroRollingSDEmitsRefZero(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	values := []float64{5.0, 5.0, 5.0, 5.0, 5.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsRollingSpec("zscore_rolling", 3)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	// Exactly one PULSE_OVERLAY_REF_ZERO warning regardless of how many
	// entries hit the zero-SD arm (the one-warning-per-layer contract).
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 5 {
		t.Fatalf("len(Entries) = %d, want 5", len(entries))
	}
	// Entry 0: count == 0 → NaN (no warning).
	// Entry 1: count == 1 → NaN (no warning).
	// Entries 2..4: count >= 2, sample SD == 0 → NaN + warning.
	for i := 0; i < 5; i++ {
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i,
				*entries[i].Summary.Statistic)
		}
	}
}

// TestOverlay_ZScoreVsRolling_AbsentPointDoesNotAdvanceCarrier pins the
// ring-carrier-doesn't-advance rule for absent points. Series
// [10, 20, 30, absent, 40, 50] with window=4 — when we reach the fifth
// present point (value=40 at index 4) the ring should still be {10, 20,
// 30} (absent point at index 3 did not advance the carrier), so the
// z-score is computed against mean=20, M2=200, sample SD=sqrt(200/2)=10,
// z = (40 - 20) / 10 = 2.0. Then the sixth present point (value=50 at
// index 5) sees ring={10, 20, 30, 40}, mean=25, M2=500, sample
// SD=sqrt(500/3) ≈ 12.91, z = (50 - 25) / 12.91 ≈ 1.936.
//
// math.NaN signals absent (newStubSeriesHost's resolver returns
// (0, false) for NaN inputs).
func TestOverlay_ZScoreVsRolling_AbsentPointDoesNotAdvanceCarrier(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}}
	values := []float64{10.0, 20.0, 30.0, math.NaN(), 40.0, 50.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsRollingSpec("zscore_rolling", 4)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 6 {
		t.Fatalf("len(Entries) = %d, want 6", len(entries))
	}
	// entry[0]: count == 0 → NaN.
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (count == 0)",
			entries[0].Summary.Statistic)
	}
	// entry[1]: count == 1 → NaN.
	if entries[1].Summary.Statistic == nil || !math.IsNaN(*entries[1].Summary.Statistic) {
		t.Fatalf("entries[1].Summary.Statistic = %v, want NaN (count == 1)",
			entries[1].Summary.Statistic)
	}
	// entry[2]: ring={10, 20}, mean=15, M2=50, SD=sqrt(50)≈7.071, z=(30-15)/sqrt(50).
	const tol = 1e-9
	want2 := (30.0 - 15.0) / math.Sqrt(50.0)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, want2, tol)
	if entries[3].Summary.Statistic != nil {
		t.Errorf("entries[3].Summary.Statistic = %v, want nil (absent group)",
			*entries[3].Summary.Statistic)
	}
	// entry[4]: ring={10, 20, 30} (absent did NOT advance), mean=20,
	// M2 = (10-20)² + (20-20)² + (30-20)² = 200, sample SD=sqrt(200/2)=10,
	// z=(40-20)/10=2.0.
	want4 := 2.0
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 4, want4, tol)
	// entry[5]: ring={10, 20, 30, 40}, mean=25,
	// M2 = (10-25)² + (20-25)² + (30-25)² + (40-25)² = 225+25+25+225 = 500,
	// sample SD = sqrt(500/3) ≈ 12.9099, z = (50 - 25) / sqrt(500/3).
	want5 := (50.0 - 25.0) / math.Sqrt(500.0/3.0)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 5, want5, tol)
}

// TestOverlay_ZScoreVsRolling_MissingWindowParamRejected pins the
// missing-Params arm: spec without Params (or with empty Params) is
// rejected with a coded error carrying PULSE_OVERLAY_PARAM_MISSING.
// Reuses the shared rolling-family helper (extractWindowParam) so the
// failure shape is identical to OVERLAY_INDEX_VS_ROLLING_MEAN.
func TestOverlay_ZScoreVsRolling_MissingWindowParamRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHost(keys, []float64{10.0, 20.0})

	cases := []struct {
		name string
		spec types.OverlaySpec
	}{
		{
			name: "Params nil",
			spec: types.OverlaySpec{
				Name:  "zscore_rolling",
				Kind:  types.OverlayKindZScoreVsRolling,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
				// Params intentionally absent.
			},
		},
		{
			name: "Params empty object missing window",
			spec: types.OverlaySpec{
				Name:   "zscore_rolling",
				Kind:   types.OverlayKindZScoreVsRolling,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
				Params: json.RawMessage(`{}`),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ApplyOverlaysSeries([]types.OverlaySpec{tc.spec}, host)
			if err == nil {
				t.Fatalf("expected coded error for missing window param, got nil")
			}
			coded, ok := err.(*errors.CodedError)
			if !ok {
				t.Fatalf("err type = %T, want *errors.CodedError", err)
			}
			if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_PARAM_MISSING); got != want {
				t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
			}
			if got, want := coded.Details["param"], "window"; got != want {
				t.Errorf("err.Details[\"param\"] = %v, want %v", got, want)
			}
		})
	}
}

// TestOverlay_ZScoreVsRolling_NonPositiveWindowRejected pins the
// window<=0 arm: spec with window=0 or window=-1 is rejected with a
// coded error carrying PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. Mirrors the
// OVERLAY_INDEX_VS_ROLLING_MEAN sibling test.
func TestOverlay_ZScoreVsRolling_NonPositiveWindowRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHost(keys, []float64{10.0, 20.0})

	cases := []struct {
		name   string
		window int
	}{
		{name: "window=0", window: 0},
		{name: "window=-1", window: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := newZScoreVsRollingSpec("zscore_rolling", tc.window)
			_, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, host)
			if err == nil {
				t.Fatalf("expected coded error for window=%d, got nil", tc.window)
			}
			coded, ok := err.(*errors.CodedError)
			if !ok {
				t.Fatalf("err type = %T, want *errors.CodedError", err)
			}
			if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE); got != want {
				t.Errorf("err.Details[\"code\"] = %v, want %v", got, want)
			}
		})
	}
}

// TestOverlay_ZScoreVsRolling_SampleSDDocumentedContrast pins the
// distinguishing arithmetic with hand-computed expected values: for
// series [1, 2, 3, 4, 5] with window=3, compute the z-scores from index
// 2 onward using SAMPLE SD (n-1 denominator) and verify within ULP.
//
// Math reference:
//
//	i=0 (push 1): carrier {1}, count=1 → no divide (count < 2)
//	i=1 (push 2): carrier {1,2}, count=2 → divide BEFORE push:
//	              at evaluation count was 1 → NaN.
//	i=2: carrier {1,2}, mean=1.5, M2 = (1-1.5)² + (2-1.5)² = 0.5,
//	     sample SD = sqrt(0.5/1) = sqrt(0.5) ≈ 0.7071,
//	     z = (3 - 1.5) / 0.7071 ≈ 2.1213.
//	i=3 (push 3 evicts 1): carrier {2,3,4}, mean=3,
//	     M2 = (2-3)² + (3-3)² + (4-3)² = 2, sample SD = sqrt(2/2) = 1,
//	     z = (4 - 3) / 1 = 1.0. (But the divide happens BEFORE the push
//	     so carrier at evaluation = {2, 3} from earlier... actually let
//	     me re-trace carefully.)
//
// Actually re-tracing the engine: the handler reads the carrier BEFORE
// pushing the current value. So at index i, the carrier holds the
// previous min(i, W) PRESENT values.
//
//	i=0: carrier={}, count=0 → NaN. Then push 1 → carrier={1}.
//	i=1: carrier={1}, count=1 → NaN. Then push 2 → carrier={1,2}.
//	i=2: carrier={1,2}, mean=1.5, M2=0.5, sample SD=sqrt(0.5),
//	     z=(3-1.5)/sqrt(0.5) ≈ 2.1213. Then push 3 → carrier evicts 1
//	     → carrier={2,3} after the push (window=3, but only 2 pushed
//	     so far had {1,2}; push 3 → {1,2,3} since count was 2 (< window
//	     of 3) so it just appended → carrier={1,2,3}, count=3).
//	i=3: carrier={1,2,3}, mean=2, M2=2, sample SD=sqrt(2/2)=1,
//	     z=(4-2)/1 = 2.0. Then push 4 evicts 1 → carrier={2,3,4}.
//	i=4: carrier={2,3,4}, mean=3, M2=2, sample SD=1, z=(5-3)/1=2.0.
//
// So expected: [NaN, NaN, 2.1213, 2.0, 2.0].
//
// Doc-comment contrast vs population SD (NOT a separate test — just an
// inline reminder of the distinguishing math): if this handler used
// POPULATION SD (sqrt(M2/N), N=2) at i=2, sd would be sqrt(0.5/2) =
// sqrt(0.25) = 0.5, z = (3-1.5)/0.5 = 3.0 — different from the sample
// SD answer of 2.1213. The factor between sample SD and population SD
// for n=2 is sqrt(2/1) ≈ 1.414, which matches the ratio 3.0/2.1213 ≈
// 1.414. The KIND CHOOSES sample SD; this is the distinguishing fact
// from OVERLAY_ZSCORE_VS_TOTAL (population SD).
func TestOverlay_ZScoreVsRolling_SampleSDDocumentedContrast(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newZScoreVsRollingSpec("zscore_rolling", 3)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 5 {
		t.Fatalf("len(Entries) = %d, want 5", len(entries))
	}

	// i=0: NaN (count == 0).
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0] = %v, want NaN", entries[0].Summary.Statistic)
	}
	// i=1: NaN (count == 1).
	if entries[1].Summary.Statistic == nil || !math.IsNaN(*entries[1].Summary.Statistic) {
		t.Fatalf("entries[1] = %v, want NaN", entries[1].Summary.Statistic)
	}

	// i=2: carrier={1,2}, mean=1.5, M2=0.5, sample SD=sqrt(0.5),
	// z=(3-1.5)/sqrt(0.5) = 1.5/sqrt(0.5) = 3/sqrt(2) ≈ 2.121320343.
	const tol = 1e-9
	want2 := (3.0 - 1.5) / math.Sqrt(0.5)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, want2, tol)

	// i=3: carrier={1,2,3}, mean=2, M2=(1-2)²+(2-2)²+(3-2)²=2,
	// sample SD=sqrt(2/2)=1, z=(4-2)/1=2.0.
	want3 := 2.0
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 3, want3, tol)

	// i=4: carrier={2,3,4} (push at i=3 evicts 1), mean=3,
	// M2 = (2-3)²+(3-3)²+(4-3)² = 2, sample SD=1, z=(5-3)/1=2.0.
	want4 := 2.0
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 4, want4, tol)
}

// TestOverlay_ZScoreVsRolling_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. ZSCORE_VS_ROLLING is
// the windowed rolling sample-SD z-score kind (no axis dispatch — the
// ordered host axis is fixed); the synthesiser surfaces the lower-case
// bare-kind string "zscore_vs_rolling" mirroring the
// INDEX_VS_ROLLING_MEAN / ZSCORE_VS_TOTAL convention.
func TestOverlay_ZScoreVsRolling_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	host := newStubSeriesHost(keys, []float64{10.0, 20.0, 30.0})
	params, _ := json.Marshal(map[string]any{"window": 2})
	// No Name on the spec — exercises the synthesiser.
	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindZScoreVsRolling,
		Scope:  types.OverlayScopeGroup,
		Ref:    types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
		Params: params,
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "zscore_vs_rolling"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_ZScoreVsRolling_BufferedPath drives a small grouped
// Process through the buffered path (forced via AGG_MEDIAN) and asserts
// the SERIES overlay layer surfaces the expected per-group z-scores.
// ZSCORE_VS_ROLLING is buffered per types.OverlayStreamability so the
// orchestrator routes through processRecords (PathBuffered).
//
// Fixture: three regions {east, west, north} with per-region AGG_SUM
// (score) = {30, 90, 150}. Fixture row order is alphabetic:
// east(30), north(150), west(90). With window=3:
//
//	east:   count=0 at divide → NaN (no priors)
//	north:  count=1 at divide → NaN (one prior — sample SD undefined)
//	west:   carrier={30, 150}, mean=90, M2=(30-90)²+(150-90)²=7200,
//	        sample SD=sqrt(7200/1)=sqrt(7200)≈84.853,
//	        z=(90-90)/84.853 = 0.
func TestOverlay_ZScoreVsRolling_BufferedPath(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newZScoreVsRollingSpec("zscore_rolling", 3),
	}, true /*forceBuffered*/)
	if path == PathStreaming {
		t.Fatalf("LastPath() = %s, want non-streaming (ZSCORE_VS_ROLLING is buffered)", path)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
	}
	// west = (90 - 90) / sqrt(7200) = 0.
	const wantWest = 0.0
	want := []float64{
		math.NaN(), // east (count == 0)
		math.NaN(), // north (count == 1)
		wantWest,
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
}

// TestOverlay_ZScoreVsRolling_NilHostReturnsCoded pins the defense-in-
// depth nil-host arm: a nil SeriesHostView surfaces a coded
// PROCESSING_INTERNAL error rather than panicking. Mirrors the
// INDEX_VS_ROLLING_MEAN / INDEX_VS_PRIOR / INDEX_VS_TOTAL safety
// pattern.
func TestOverlay_ZScoreVsRolling_NilHostReturnsCoded(t *testing.T) {
	spec := newZScoreVsRollingSpec("zscore_rolling", 3)
	_, _, err := applyZScoreVsRolling(&spec, nil)
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
