package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E4-S9 shared overlay-layer assertion helpers — companion to the
// existing overlay_test_helpers_test.go (E2-S12) MATRIX-shape helpers.
// This file picks up the SERIES-shape assertion shapes the E4 per-kind
// tests rely on and lifts them to a single canonical naming scheme so
// new E4+ tests can reach for a self-documenting helper instead of
// re-rolling the per-entry nil-check / NaN-check / value-comparison
// boilerplate inline.
//
// Naming convention follows the E4-S9 story brief:
//
//   - assertOverlayLayerNaN(t, layer, idx)             — entry is NaN
//   - assertOverlayLayerValue(t, layer, idx, want, ulp) — entry within ulp
//   - assertOverlayLayerNoWarnings(t, layer)            — handler ran clean
//   - assertOverlayLayerHasWarning(t, layer, code)      — exactly one
//                                                         warning of code
//   - assertOverlayLayerStatistic(t, layer, idx, stat)  — entry Summary.
//                                                         Statistic kind
//
// Each helper is a thin wrapper over the existing
// overlay_test_helpers_test.go (or the inline pattern the per-kind
// tests already use), so the E4 tests can switch over incrementally
// without touching the per-kind suites that already pass. Aliasing
// rather than rewriting keeps the existing assertion-shape failure
// messages intact and avoids churning the per-kind suites.
//
// Failure-mode convention: every helper t.Fatalf — mirrors the existing
// per-kind suite shape so a failure in the test binary halts at the
// first mismatch rather than cascading through downstream assertions
// against a malformed payload.

// assertOverlayLayerNaN asserts the SeriesEntry at `idx` carries a
// populated Summary.Statistic equal to NaN. The standard
// "first-point semantics" / "carrier count < 2" / "no comparison
// available" path across the E4 SERIES handlers (INDEX_VS_PRIOR,
// INDEX_VS_BASELINE, DELTA_VS_BASELINE, INDEX_VS_ROLLING_MEAN,
// ZSCORE_VS_ROLLING, YOY) emits a populated Summary.Statistic NaN
// rather than leaving the Statistic field unset — that's structurally
// distinct from the "absent host point" path (Summary leaves
// Statistic nil; see assertOverlayLayerStatistic for that arm).
//
// Failure modes (all fatal via t.Fatalf):
//
//   - layer is nil or non-SERIES-shape (the caller asked for a SERIES
//     entry on a MATRIX / SCALAR layer)
//   - layer.Payload.Series is nil
//   - idx out of range
//   - entries[idx].Summary.Statistic is nil
//   - entries[idx].Summary.Statistic is finite (not NaN)
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

// assertOverlayLayerNoWarnings is the layer-level analogue of
// assertNoOverlayWarnings — when a per-kind test resolves the
// per-layer warnings from a single-spec ApplyOverlaysSeries call, it
// still threads a flat `[]types.OverlayWarning` slice through. This helper
// is a thin alias over assertNoOverlayWarnings; the alias lifts the
// brief's preferred naming for the E4 batch without disturbing the
// E2-S12 helper file's existing call sites.
//
// Failure mode (fatal via t.Fatalf): len(warnings) != 0.
func assertOverlayLayerNoWarnings(t *testing.T, warnings []types.OverlayWarning) {
	t.Helper()
	assertNoOverlayWarnings(t, warnings)
}

// assertOverlayLayerHasWarning asserts the flat warnings slice carries
// exactly one entry whose Code matches the supplied errors.Code
// constant. Differs from assertWarningCode in two ways:
//
//   - Takes an errors.Code typed constant (not a raw string) so a typo
//     in the call site fails at compile time, not at test time.
//   - Pinned to wantCount=1 — the canonical "single PULSE_OVERLAY_REF_ZERO
//     per layer" contract across the E4 family (the layer-level one-
//     warning-per-divergence rule). When a future kind emits multiple
//     warnings per layer the test reaches for assertWarningCode with
//     an explicit wantCount.
//
// Failure modes (all fatal via t.Fatalf):
//
//   - len(warnings) != 1
//   - warnings[0].Code != string(code)
func assertOverlayLayerHasWarning(t *testing.T, warnings []types.OverlayWarning, code errors.Code) {
	t.Helper()
	assertWarningCode(t, warnings, string(code), 1)
}

// assertOverlayLayerStatistic asserts the SeriesEntry at `idx` has its
// Summary.Statistic field in the requested presence state:
//
//   - state == "present": Statistic is non-nil (the standard "handler
//     emitted a value for this ordinal" arm). Does NOT compare value;
//     pair with assertOverlayLayerValue for the value comparison.
//   - state == "nan":     Statistic is non-nil and equal to NaN. Alias
//     over assertOverlayLayerNaN for callers that prefer the
//     three-state nomenclature.
//   - state == "absent":  Statistic is nil (the canonical "present
//     slot, empty summary" shape from E3-S1 — the host did not produce
//     a value for this ordinal, the entry slot exists for FR-A2
//     alignment but carries no Statistic).
//
// Any other `state` value is a programming error (caller passed a
// typo) and fails the test immediately.
//
// Failure modes (all fatal via t.Fatalf):
//
//   - state outside {"present", "nan", "absent"}
//   - layer nil / non-SERIES / Series nil / idx out of range
//   - state mismatch against the actual Statistic presence
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

// TestOverlay_LayerAssertionHelpers_Smoke exercises each helper at
// least once against real ApplyOverlaysSeries output. Mirrors the
// E2-S12 TestOverlay_SharedAssertionHelpers smoke pattern — guards
// the helpers against silent rot (staticcheck U1000 keeps them live)
// and documents the intended call shape for new E4+ per-kind tests.
//
// Each helper is exercised against a known-good code path the existing
// per-kind suites already cover, so the helper's pass-failure semantics
// are pinned to the same fixtures the production tests already
// validate independently.
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
