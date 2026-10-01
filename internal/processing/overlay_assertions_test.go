package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func assertOverlayLayerNaN(t *testing.T, layer *types.OverlayLayer, idx int) {
	t.Helper()
	if layer == nil {
		t.Fatalf("assertOverlayLayerNaN: layer is nil")
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("assertOverlayLayerNaN: layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("assertOverlayLayerNaN: layer.Payload.Series is nil")
	}
	entries := layer.Payload.Series.Entries
	if idx < 0 || idx >= len(entries) {
		t.Fatalf("assertOverlayLayerNaN: idx=%d out of range [0,%d)", idx, len(entries))
	}
	stat := entries[idx].Summary.Statistic
	if stat == nil {
		t.Fatalf("assertOverlayLayerNaN: entries[%d].Summary.Statistic is nil; want NaN (use assertOverlayLayerStatistic for the absent-host path)",
			idx)
	}
	if !math.IsNaN(*stat) {
		t.Fatalf("assertOverlayLayerNaN: entries[%d].Summary.Statistic = %v; want NaN", idx, *stat)
	}
}

// assertOverlayLayerValue asserts the SeriesEntry at `idx` carries a
// populated Summary.Statistic within `ulp` of `want`. Thin alias over
// the existing assertSeriesEntryStatisticWithinTol so per-kind tests
// can pick the more self-documenting name in new test code without
// disturbing the existing per-kind suites.
//
// Failure modes match assertSeriesEntryStatisticWithinTol (the alias
// target): layer nil / non-SERIES / Series nil / idx out of range /
// Statistic nil / |Statistic - want| > ulp.
func assertOverlayLayerValue(t *testing.T, layer *types.OverlayLayer, idx int, want, ulp float64) {
	t.Helper()
	assertSeriesEntryStatisticWithinTol(t, layer, idx, want, ulp)
}

func assertOverlayLayerNoWarnings(t *testing.T, warnings []types.OverlayWarning) {
	t.Helper()
	assertNoOverlayWarnings(t, warnings)
}

func assertOverlayLayerHasWarning(t *testing.T, warnings []types.OverlayWarning, code errors.Code) {
	t.Helper()
	assertWarningCode(t, warnings, string(code), 1)
}

func assertOverlayLayerStatistic(t *testing.T, layer *types.OverlayLayer, idx int, state string) {
	t.Helper()
	if layer == nil {
		t.Fatalf("assertOverlayLayerStatistic: layer is nil")
	}
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("assertOverlayLayerStatistic: layer.Payload.Shape = %q, want %q",
			layer.Payload.Shape, types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("assertOverlayLayerStatistic: layer.Payload.Series is nil")
	}
	entries := layer.Payload.Series.Entries
	if idx < 0 || idx >= len(entries) {
		t.Fatalf("assertOverlayLayerStatistic: idx=%d out of range [0,%d)", idx, len(entries))
	}
	stat := entries[idx].Summary.Statistic
	switch state {
	case "present":
		if stat == nil {
			t.Fatalf("assertOverlayLayerStatistic: entries[%d].Summary.Statistic is nil; want present", idx)
		}
	case "nan":
		if stat == nil {
			t.Fatalf("assertOverlayLayerStatistic: entries[%d].Summary.Statistic is nil; want NaN", idx)
		}
		if !math.IsNaN(*stat) {
			t.Fatalf("assertOverlayLayerStatistic: entries[%d].Summary.Statistic = %v; want NaN", idx, *stat)
		}
	case "absent":
		if stat != nil {
			t.Fatalf("assertOverlayLayerStatistic: entries[%d].Summary.Statistic = %v; want nil (absent host point)",
				idx, *stat)
		}
	default:
		t.Fatalf("assertOverlayLayerStatistic: state = %q; want one of [present, nan, absent]", state)
	}
}

func TestOverlay_LayerAssertionHelpers_Smoke(t *testing.T) {
	// assertOverlayLayerNaN: first-point NaN on INDEX_VS_PRIOR.
	t.Run("nan", func(t *testing.T) {
		keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}}
		values := []float64{10.0, 20.0, 30.0}
		host := newStubSeriesHost(keys, values)
		specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}
		layers, _, err := ApplyOverlaysSeries(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlaysSeries: %v", err)
		}
		assertOverlayLayerNaN(t, &layers[0], 0)
	})

	// assertOverlayLayerValue: lag-1 ratio on INDEX_VS_PRIOR's second
	// entry (20/10*100 = 200).
	t.Run("value", func(t *testing.T) {
		keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}}
		values := []float64{10.0, 20.0, 30.0}
		host := newStubSeriesHost(keys, values)
		specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}
		layers, _, err := ApplyOverlaysSeries(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlaysSeries: %v", err)
		}
		assertOverlayLayerValue(t, &layers[0], 1, 200.0, 1e-9)
	})

	// assertOverlayLayerNoWarnings: clean-path INDEX_VS_PRIOR.
	t.Run("no_warnings", func(t *testing.T) {
		keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}}
		values := []float64{10.0, 20.0, 30.0}
		host := newStubSeriesHost(keys, values)
		specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}
		_, warnings, err := ApplyOverlaysSeries(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlaysSeries: %v", err)
		}
		assertOverlayLayerNoWarnings(t, warnings)
	})

	// assertOverlayLayerHasWarning: zero-prior PULSE_OVERLAY_REF_ZERO
	// on INDEX_VS_PRIOR.
	t.Run("has_warning", func(t *testing.T) {
		keys := []types.AxisKey{{"a"}, {"b"}}
		values := []float64{0.0, 5.0}
		host := newStubSeriesHost(keys, values)
		specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}
		_, warnings, err := ApplyOverlaysSeries(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlaysSeries: %v", err)
		}
		assertOverlayLayerHasWarning(t, warnings, errors.PULSE_OVERLAY_REF_ZERO)
	})

	// assertOverlayLayerStatistic three-state coverage. "present" on
	// entry 1 (lag-1 ratio populated); "nan" on entry 0 (first-point
	// semantics); "absent" on entry 1 of an absent-middle fixture
	// (the canonical present-slot-empty-summary shape).
	t.Run("statistic_present", func(t *testing.T) {
		keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}}
		values := []float64{10.0, 20.0, 30.0}
		host := newStubSeriesHost(keys, values)
		specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}
		layers, _, err := ApplyOverlaysSeries(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlaysSeries: %v", err)
		}
		assertOverlayLayerStatistic(t, &layers[0], 1, "present")
	})
	t.Run("statistic_nan", func(t *testing.T) {
		keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}}
		values := []float64{10.0, 20.0, 30.0}
		host := newStubSeriesHost(keys, values)
		specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}
		layers, _, err := ApplyOverlaysSeries(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlaysSeries: %v", err)
		}
		assertOverlayLayerStatistic(t, &layers[0], 0, "nan")
	})
	t.Run("statistic_absent", func(t *testing.T) {
		keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
		values := []float64{10.0, math.NaN(), 30.0}
		host := newStubSeriesHost(keys, values)
		specs := []types.OverlaySpec{newIndexVsPriorSpec("idx_prior")}
		layers, _, err := ApplyOverlaysSeries(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlaysSeries: %v", err)
		}
		assertOverlayLayerStatistic(t, &layers[0], 1, "absent")
	})
}
