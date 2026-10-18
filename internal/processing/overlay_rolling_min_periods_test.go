package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// rollingSpec is a rolling-family spec with arbitrary params.
func rollingSpec(t *testing.T, kind types.OverlayKind, params map[string]any) types.OverlaySpec {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return types.OverlaySpec{Name: "r", Kind: kind, Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}, Params: b}
}

// rollingAbsent marks an expected absent entry (no statistic) in
// rollingFixtureWant; NaN marks an expected null statistic.
const rollingAbsent = -999.0

// rollingFixture is the hand-computed series: [10, 20, absent, 30, 50,
// 40, 60], window 3. The absent point never advances the carrier, so the
// fourth entry (30) sees only {10, 20}.
var rollingFixture = []float64{10, 20, math.NaN(), 30, 50, 40, 60}

func rollingRun(t *testing.T, spec types.OverlaySpec) []*float64 {
	t.Helper()
	keys := make([]types.AxisKey, len(rollingFixture))
	for i := range keys {
		keys[i] = types.AxisKey{string(rune('a' + i))}
	}
	layers, warnings, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, newStubSeriesHost(keys, rollingFixture))
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	out := make([]*float64, 0, len(keys))
	for _, e := range layers[0].Payload.Series.Entries {
		out = append(out, e.Summary.Statistic)
	}
	return out
}

func rollingCheck(t *testing.T, got []*float64, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d entries, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		switch {
		case w == rollingAbsent:
			if g != nil {
				t.Errorf("entry %d: %v, want no statistic (absent point)", i, *g)
			}
		case g == nil:
			t.Errorf("entry %d: no statistic, want %v", i, w)
		case math.IsNaN(w):
			if !math.IsNaN(*g) {
				t.Errorf("entry %d: %v, want null (warm-up)", i, *g)
			}
		case math.IsNaN(*g) || math.Abs(*g-w) > 1e-9:
			t.Errorf("entry %d: %v, want %v", i, *g, w)
		}
	}
}

// TestRollingOverlays_MinPeriods (U50 E2-S3) pins both rolling kinds'
// warm-up over the hand-computed fixture. Carrier contents at each
// present point (absent never advances it):
//
//	entry 0 (10): {}            entry 4 (50): {10, 20, 30}  mean 20,    sample SD 10
//	entry 1 (20): {10}          entry 5 (40): {20, 30, 50}  mean 100/3, sample SD sqrt(700/3)
//	entry 3 (30): {10, 20}      entry 6 (60): {30, 50, 40}  mean 40,    sample SD 10
//
// entry 3 under {10, 20}: mean 15, sample SD sqrt(50).
func TestRollingOverlays_MinPeriods(t *testing.T) {
	nan := math.NaN()
	z5 := (40.0 - 100.0/3.0) / math.Sqrt(700.0/3.0)
	for _, c := range []struct {
		name   string
		kind   types.OverlayKind
		params map[string]any
		want   []float64
	}{
		// Default = window: null until three present priors (the
		// z-score's behaviour break — it used to answer from entry 3).
		{"zscore default", types.OverlayKindZScoreVsRolling, map[string]any{"window": 3},
			[]float64{nan, nan, rollingAbsent, nan, 3, z5, 2}},
		{"zscore explicit window", types.OverlayKindZScoreVsRolling, map[string]any{"window": 3, "min_periods": 3},
			[]float64{nan, nan, rollingAbsent, nan, 3, z5, 2}},
		{"zscore min_periods 2", types.OverlayKindZScoreVsRolling, map[string]any{"window": 3, "min_periods": 2},
			[]float64{nan, nan, rollingAbsent, 15 / math.Sqrt(50), 3, z5, 2}},
		{"index default", types.OverlayKindIndexVsRollingMean, map[string]any{"window": 3},
			[]float64{nan, nan, rollingAbsent, nan, 250, 120, 150}},
		{"index min_periods 2", types.OverlayKindIndexVsRollingMean, map[string]any{"window": 3, "min_periods": 2},
			[]float64{nan, nan, rollingAbsent, 200, 250, 120, 150}},
		{"index min_periods 1", types.OverlayKindIndexVsRollingMean, map[string]any{"window": 3, "min_periods": 1},
			[]float64{nan, 200, rollingAbsent, 200, 250, 120, 150}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rollingCheck(t, rollingRun(t, rollingSpec(t, c.kind, c.params)), c.want)
		})
	}
}

// TestRollingOverlays_MinPeriodsRefused: an out-of-range or non-integer
// min_periods is PULSE_OVERLAY_LEVEL_OUT_OF_RANGE at runtime, naming the
// param and its bounds (the z-score's floor is 2, the index's 1; the
// ceiling is the window).
func TestRollingOverlays_MinPeriodsRefused(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHost(keys, []float64{1, 2})
	for _, c := range []struct {
		kind types.OverlayKind
		mp   any
		min  int
	}{
		{types.OverlayKindZScoreVsRolling, 1, 2},
		{types.OverlayKindZScoreVsRolling, 4, 2},
		{types.OverlayKindZScoreVsRolling, 2.5, 2},
		{types.OverlayKindIndexVsRollingMean, 0, 1},
		{types.OverlayKindIndexVsRollingMean, 4, 1},
		{types.OverlayKindIndexVsRollingMean, "2", 1},
	} {
		spec := rollingSpec(t, c.kind, map[string]any{"window": 3, "min_periods": c.mp})
		_, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, host)
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE {
			t.Fatalf("%s min_periods %v: want LEVEL_OUT_OF_RANGE, got %v", c.kind, c.mp, err)
		}
		if ce.Details["param"] != "min_periods" || ce.Details["min"] != c.min || ce.Details["max"] != 3 {
			t.Errorf("%s min_periods %v: details %#v", c.kind, c.mp, ce.Details)
		}
	}
}
