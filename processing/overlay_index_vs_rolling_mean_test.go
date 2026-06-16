package processing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newIndexVsRollingMeanSpec returns the canonical happy-path
// OVERLAY_INDEX_VS_ROLLING_MEAN spec the per-test fixtures consume —
// GROUP scope, `Ref.RollingMean` populated as the empty marker, a
// caller-supplied `window` value, and a deterministic Name so the
// renderer-facing label is pinned in the assertions.
func newIndexVsRollingMeanSpec(name string, window int) types.OverlaySpec {
	params, _ := json.Marshal(map[string]any{"window": window})
	return types.OverlaySpec{
		Name:   name,
		Kind:   types.OverlayKindIndexVsRollingMean,
		Scope:  types.OverlayScopeGroup,
		Ref:    types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
		Params: params,
	}
}

// TestOverlay_IndexVsRollingMean_Basic_Window3 pins the canonical happy
// path: ten points [10, 20, 30, 40, 50, 60, 70, 80, 90, 100] with
// window=3 produce indices [NaN, NaN, NaN, 200, 166.66..., 150,
// 140, 133.33..., 128.57..., 125].
//
// First 3 points: NaN (window not yet filled).
// point[3]=40 / mean(10,20,30)=20 * 100 = 200
// point[4]=50 / mean(20,30,40)=30 * 100 = 166.66...
// point[5]=60 / mean(30,40,50)=40 * 100 = 150
// point[6]=70 / mean(40,50,60)=50 * 100 = 140
// point[7]=80 / mean(50,60,70)=60 * 100 = 133.33...
func TestOverlay_IndexVsRollingMean_Basic_Window3(t *testing.T) {
	keys := []types.AxisKey{
		{"jan"}, {"feb"}, {"mar"}, {"apr"}, {"may"},
		{"jun"}, {"jul"}, {"aug"}, {"sep"}, {"oct"},
	}
	values := []float64{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsRollingMeanSpec("idx_rolling", 3)}

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

	// First three entries: NaN — window not yet filled.
	for i := 0; i < 3; i++ {
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN (window not yet filled)", i)
		}
		if !math.IsNaN(*entries[i].Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN (window not yet filled)",
				i, *entries[i].Summary.Statistic)
		}
	}

	// Entries 3, 4 — pin within tight tolerance.
	const tol = 1e-9
	wants := []float64{
		40.0 / 20.0 * 100.0, // 200
		50.0 / 30.0 * 100.0, // 166.66...
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i+3, w, tol)
	}

	// Layer-level summary: baseline = 100; Count = 7 (the 7 non-NaN entries).
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 100.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 100", got)
	}
	if got, want := *layers[0].Summary.Count, 7; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_IndexVsRollingMean_Window1 verifies that window=1 collapses
// to INDEX_VS_PRIOR behaviour: every point past the first divides by the
// immediately preceding present value (the "rolling mean" of a 1-element
// window IS that element).
func TestOverlay_IndexVsRollingMean_Window1(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	values := []float64{10.0, 20.0, 30.0, 40.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsRollingMeanSpec("idx_rolling", 1)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	// entry[0]: NaN (no prior — window not yet filled with even 1 entry
	// at the divide step).
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (first point, window unfilled)",
			entries[0].Summary.Statistic)
	}

	// Entries 1..3: 20/10*100, 30/20*100, 40/30*100.
	const tol = 1e-9
	wants := []float64{
		20.0 / 10.0 * 100.0, // 200
		30.0 / 20.0 * 100.0, // 150
		40.0 / 30.0 * 100.0, // 133.33...
	}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i+1, w, tol)
	}
}

// TestOverlay_IndexVsRollingMean_WindowExceedsSeries pins the degenerate
// case: a 5-point series with window=10 never sees a full window, so
// every entry's Statistic is NaN with no warning (mirrors the
// INDEX_VS_PRIOR first-point semantics: "no comparison available" is
// structurally distinct from "denominator was zero").
func TestOverlay_IndexVsRollingMean_WindowExceedsSeries(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	values := []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsRollingMeanSpec("idx_rolling", 10)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 5 {
		t.Fatalf("len(Entries) = %d, want 5", len(entries))
	}
	for i, entry := range entries {
		if entry.Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*entry.Summary.Statistic) {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want NaN", i,
				*entry.Summary.Statistic)
		}
	}

	// Count = 0 — no non-NaN entries.
	if got, want := *layers[0].Summary.Count, 0; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_IndexVsRollingMean_ZeroRollingMeanEmitsRefZero pins the
// zero-rolling-mean path: [0, 0, 0, 10] with window=3 produces [NaN,
// NaN, NaN, NaN] — the first 3 entries are NaN because the window is
// unfilled, and the 4th entry hits the zero-mean arm (mean of three
// zeros is 0) and emits NaN + PULSE_OVERLAY_REF_ZERO.
func TestOverlay_IndexVsRollingMean_ZeroRollingMeanEmitsRefZero(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	values := []float64{0.0, 0.0, 0.0, 10.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsRollingMeanSpec("idx_rolling", 3)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 4 {
		t.Fatalf("len(Entries) = %d, want 4", len(entries))
	}
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

// TestOverlay_IndexVsRollingMean_AbsentPointDoesNotAdvanceCarrier
// pins the ring-carrier-doesn't-advance rule for absent points.
// Series [10, 20, absent, 30, 40] with window=2 — when we reach the
// fourth present point (value=30) the ring should still be [10, 20]
// (absent point did not advance the carrier), so the index is
// 30 / mean(10, 20) * 100 = 30 / 15 * 100 = 200. Then the fifth point
// (value=40) sees ring = [20, 30] and computes 40 / mean(20, 30) * 100
// = 40 / 25 * 100 = 160.
//
// math.NaN signals absent (newStubSeriesHost's resolver returns
// (0, false) for NaN inputs).
func TestOverlay_IndexVsRollingMean_AbsentPointDoesNotAdvanceCarrier(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}}
	values := []float64{10.0, 20.0, math.NaN(), 30.0, 40.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newIndexVsRollingMeanSpec("idx_rolling", 2)}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 5 {
		t.Fatalf("len(Entries) = %d, want 5", len(entries))
	}
	// entry[0]: NaN — first present, window unfilled.
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (first point, window unfilled)",
			entries[0].Summary.Statistic)
	}
	// entry[1]: NaN — second present, window still unfilled (ring count=1
	// after push of 10; at point[1] window is unfilled at the divide step
	// because count=1 < window=2 BEFORE push, then push 20 → count=2).
	if entries[1].Summary.Statistic == nil || !math.IsNaN(*entries[1].Summary.Statistic) {
		t.Fatalf("entries[1].Summary.Statistic = %v, want NaN (second point, window unfilled)",
			entries[1].Summary.Statistic)
	}
	if entries[2].Summary.Statistic != nil {
		t.Errorf("entries[2].Summary.Statistic = %v, want nil (absent group)",
			*entries[2].Summary.Statistic)
	}
	// entry[3]: 30 / mean(10, 20) * 100 = 30 / 15 * 100 = 200.
	const tol = 1e-9
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 3, 200.0, tol)
	// entry[4]: 40 / mean(20, 30) * 100 = 40 / 25 * 100 = 160.
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 4, 160.0, tol)
}

// TestOverlay_IndexVsRollingMean_MissingWindowParamRejected pins the
// missing-Params arm: spec without Params (or with empty Params) is
// rejected with a coded error carrying PULSE_OVERLAY_PARAM_MISSING.
func TestOverlay_IndexVsRollingMean_MissingWindowParamRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHost(keys, []float64{10.0, 20.0})

	cases := []struct {
		name string
		spec types.OverlaySpec
	}{
		{
			name: "Params nil",
			spec: types.OverlaySpec{
				Name:  "idx_rolling",
				Kind:  types.OverlayKindIndexVsRollingMean,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
				// Params intentionally absent.
			},
		},
		{
			name: "Params empty object missing window",
			spec: types.OverlaySpec{
				Name:   "idx_rolling",
				Kind:   types.OverlayKindIndexVsRollingMean,
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

// TestOverlay_IndexVsRollingMean_NonPositiveWindowRejected pins the
// window<=0 arm: spec with window=0 or window=-1 is rejected with a
// coded error carrying PULSE_OVERLAY_LEVEL_OUT_OF_RANGE.
func TestOverlay_IndexVsRollingMean_NonPositiveWindowRejected(t *testing.T) {
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
			spec := newIndexVsRollingMeanSpec("idx_rolling", tc.window)
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

// TestOverlay_IndexVsRollingMean_DefaultLayerName pins the synthesised
// renderer-facing label when the spec omits Name. INDEX_VS_ROLLING_MEAN
// is the windowed rolling-mean kind (no axis dispatch — the ordered
// host axis is fixed); the synthesiser surfaces the lower-case
// bare-kind string "index_vs_rolling_mean" mirroring the
// INDEX_VS_PRIOR / INDEX_VS_BASELINE convention.
func TestOverlay_IndexVsRollingMean_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	host := newStubSeriesHost(keys, []float64{10.0, 20.0, 30.0})
	params, _ := json.Marshal(map[string]any{"window": 2})
	// No Name on the spec — exercises the synthesiser.
	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindIndexVsRollingMean,
		Scope:  types.OverlayScopeGroup,
		Ref:    types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
		Params: params,
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "index_vs_rolling_mean"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_IndexVsRollingMean_BufferedPath drives a small grouped
// Process through the buffered path (forced via AGG_MEDIAN) and asserts
// the SERIES overlay layer surfaces the expected per-group indices.
// INDEX_VS_ROLLING_MEAN is buffered per types.OverlayStreamability so
// the orchestrator routes through processRecords (PathBuffered).
//
// Fixture: three regions {east, west, north} with per-region AGG_SUM
// (score) = {30, 90, 150}. Fixture row order is alphabetic:
// east(30), north(150), west(90). With window=2:
//
//	east:  NaN (window unfilled)
//	north: NaN (window unfilled — count goes 1→2 inside this push)
//	west:  90 / mean(30, 150) * 100 = 90 / 90 * 100 = 100
func TestOverlay_IndexVsRollingMean_BufferedPath(t *testing.T) {
	resp, path := runOverlayE2E(t, []types.OverlaySpec{
		newIndexVsRollingMeanSpec("idx_rolling", 2),
	}, true /*forceBuffered*/)
	if path == PathStreaming {
		t.Fatalf("LastPath() = %s, want non-streaming (INDEX_VS_ROLLING_MEAN is buffered)", path)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("len(Overlays) = %d, want 1", len(resp.Overlays))
	}
	// west = 90 / mean(30,150) * 100 = 90 / 90 * 100 = 100
	const wantWest = 100.0
	want := []float64{
		math.NaN(), // east (window unfilled)
		math.NaN(), // north (window unfilled — count reaches 2 inside this push, NOT before the divide)
		wantWest,
	}
	assertOverlayLayerStatistics(t, &resp.Overlays[0], want)
}

// TestOverlay_IndexVsRollingMean_NilHostReturnsCoded pins the
// defense-in-depth nil-host arm: a nil SeriesHostView surfaces a coded
// PROCESSING_INTERNAL error rather than panicking. Mirrors the
// INDEX_VS_PRIOR / INDEX_VS_TOTAL safety pattern.
func TestOverlay_IndexVsRollingMean_NilHostReturnsCoded(t *testing.T) {
	spec := newIndexVsRollingMeanSpec("idx_rolling", 3)
	_, _, err := applyIndexVsRollingMean(&spec, nil)
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
