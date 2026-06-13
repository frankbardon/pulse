package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_DELTA_VS_SIBLING handler
// (processing/overlay_delta_vs_sibling.go).
//
// E3-S5 scope:
//
//   - First sibling-reference SERIES-host overlay kind. The handler is
//     wired into seriesOverlayHandlers via the dispatch entry in
//     processing/overlay_series.go.
//   - Acceptance: basic series math (per-group `value - sibling_val`),
//     unknown-sibling emits PULSE_OVERLAY_REF_UNKNOWN + NaN, zero-
//     sibling stays a well-defined delta (no warning — subtraction by
//     zero recovers the raw value), absent-group passthrough stays
//     absent, sibling-self-emission yields 0.
//   - Tests reuse the SeriesHostView fixtures from
//     overlay_series_test.go (newStubSeriesHost) and the per-entry
//     assertion helpers established by the INDEX_VS_TOTAL tests
//     (assertSeriesEntryStatisticWithinTol).

// newDeltaVsSiblingSpec returns a canonical OVERLAY_DELTA_VS_SIBLING
// spec naming the requested `(field, value)` sibling reference. GROUP
// scope plus Ref.Sibling populated — the predict-time happy path.
func newDeltaVsSiblingSpec(name, field, value string) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindDeltaVsSibling,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Sibling: &types.OverlaySiblingRef{Field: field, Value: value},
		},
	}
}

// newStubSeriesHostWithField wraps the keys + values fixture with a
// matching grouper-field list so the sibling resolver matches by
// (Field, Value) against the host's GroupFields slot. Mirrors
// newStubSeriesHost but plumbs the field list through
// NewSeriesHostViewWithFields.
func newStubSeriesHostWithField(keys []types.AxisKey, values []float64, field string) *SeriesHostView {
	resolver := func(i int) (float64, bool) {
		if i < 0 || i >= len(values) {
			return 0, false
		}
		v := values[i]
		if math.IsNaN(v) {
			return 0, false
		}
		return v, true
	}
	return NewSeriesHostViewWithFields(keys, resolver, []string{field})
}

// TestOverlay_DeltaVsSibling_BasicSeries pins the canonical happy path:
// three groups (US, CA, MX) with values 100, 200, 300 and the sibling
// reference (region, US). US is the sibling so US emits 100 - 100 = 0;
// CA emits 200 - 100 = 100; MX emits 300 - 100 = 200.
func TestOverlay_DeltaVsSibling_BasicSeries(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}}
	values := []float64{100.0, 200.0, 300.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newDeltaVsSiblingSpec("delta_sibling", "region", "US")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	want := []float64{0.0, 100.0, 200.0}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}

	// Layer-level Summary: baseline=0, Count=3, Min=0, Max=200.
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 0.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 0", got)
	}
	if got, w := *layers[0].Summary.Count, 3; got != w {
		t.Errorf("layer.Summary.Count = %v, want %v", got, w)
	}
	if got := *layers[0].Summary.Min; got != 0.0 {
		t.Errorf("layer.Summary.Min = %v, want 0", got)
	}
	if got := *layers[0].Summary.Max; got != 200.0 {
		t.Errorf("layer.Summary.Max = %v, want 200", got)
	}
}

// TestOverlay_DeltaVsSibling_SelfEmitsZero pins the self-vs-self
// emission: the sibling group's own entry surfaces a delta of exactly
// 0.0, NOT a nil Statistic.
func TestOverlay_DeltaVsSibling_SelfEmitsZero(t *testing.T) {
	keys := []types.AxisKey{{"baseline"}, {"a"}, {"b"}}
	values := []float64{50.0, 75.0, 25.0}
	host := newStubSeriesHostWithField(keys, values, "grp")
	specs := []types.OverlaySpec{newDeltaVsSiblingSpec("delta", "grp", "baseline")}

	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	// Sibling self-emission: baseline group surfaces a present Statistic
	// of 0 (not a nil Statistic).
	entry := layers[0].Payload.Series.Entries[0]
	if entry.Summary.Statistic == nil {
		t.Fatalf("sibling self-emission: entries[0].Summary.Statistic nil; want 0.0 (self-vs-self)")
	}
	if got := *entry.Summary.Statistic; got != 0.0 {
		t.Errorf("sibling self-emission: entries[0].Summary.Statistic = %v, want 0.0", got)
	}
}

// TestOverlay_DeltaVsSibling_UnknownFieldEmitsWarn pins the unknown-
// sibling-field path: when Ref.Sibling.Field is not in the host's
// GroupFields slot the handler emits ONE PULSE_OVERLAY_REF_UNKNOWN
// warning and every present entry's Statistic is NaN.
func TestOverlay_DeltaVsSibling_UnknownFieldEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{1.0, 2.0, 3.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newDeltaVsSiblingSpec("delta", "bogus_field", "a")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_UNKNOWN), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)", len(entries), len(keys))
	}
	for i, e := range entries {
		if e.Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*e.Summary.Statistic) {
			t.Errorf("entries[%d].Summary.Statistic = %v, want NaN", i, *e.Summary.Statistic)
		}
	}
}

// TestOverlay_DeltaVsSibling_UnknownValueEmitsWarn pins the unknown-
// sibling-value path: when Ref.Sibling.Field IS a grouper field but
// Ref.Sibling.Value does not match any observed axis-key value, the
// handler emits ONE PULSE_OVERLAY_REF_UNKNOWN warning and every
// present entry's Statistic is NaN.
func TestOverlay_DeltaVsSibling_UnknownValueEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{1.0, 2.0, 3.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	// "z" is not in the observed key set
	specs := []types.OverlaySpec{newDeltaVsSiblingSpec("delta", "region", "z")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_UNKNOWN), 1)

	entries := layers[0].Payload.Series.Entries
	for i, e := range entries {
		if e.Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*e.Summary.Statistic) {
			t.Errorf("entries[%d].Summary.Statistic = %v, want NaN", i, *e.Summary.Statistic)
		}
	}
}

// TestOverlay_DeltaVsSibling_ZeroSiblingNoWarn pins the zero-sibling
// path: when the sibling resolves to a zero value, DELTA stays well-
// defined (each entry = value - 0 = value). No warning fires
// (distinguishing this kind from the INDEX_VS_SIBLING twin).
func TestOverlay_DeltaVsSibling_ZeroSiblingNoWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{0.0, 7.0, 10.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newDeltaVsSiblingSpec("delta", "region", "a")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	// Sibling resolves to 0 → each delta = value - 0 = value.
	want := []float64{0.0, 7.0, 10.0}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}
}

// TestOverlay_DeltaVsSibling_AbsentGroupsStayAbsent pins the absent-
// group contract: groups for which the resolver reports (0, false)
// surface a SeriesEntry whose Summary leaves Statistic unset.
func TestOverlay_DeltaVsSibling_AbsentGroupsStayAbsent(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	// NaN signals absent for the stub host.
	values := []float64{10.0, math.NaN(), 30.0, math.NaN()}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newDeltaVsSiblingSpec("delta", "region", "a")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d", len(entries), len(keys))
	}
	// a: 10 - 10 = 0 (sibling self)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, 0.0, 1e-12)
	// b: absent
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1] (absent) Statistic = %v, want nil", *entries[1].Summary.Statistic)
	}
	// c: 30 - 10 = 20
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 20.0, 1e-12)
	// d: absent
	if entries[3].Summary.Statistic != nil {
		t.Errorf("entries[3] (absent) Statistic = %v, want nil", *entries[3].Summary.Statistic)
	}
}

// TestOverlay_DeltaVsSibling_DefaultLayerName pins the synthesised
// label when the spec omits Name. DELTA_VS_SIBLING uses the bare
// lower-case kind string.
func TestOverlay_DeltaVsSibling_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHostWithField(keys, []float64{1.0, 2.0}, "region")
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindDeltaVsSibling,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Sibling: &types.OverlaySiblingRef{Field: "region", Value: "a"},
		},
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "delta_vs_sibling"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_DeltaVsSibling_FallbackResolverScansKeys exercises the
// E3-S5 fallback resolver path: when the SeriesHostView does NOT carry
// a GroupFields slot (legacy NewSeriesHostView constructor), the
// sibling resolver scans every axis-key element for a match against
// the requested Value. With a single-axis host this is byte-equivalent
// to the field-driven lookup — entry 0 ("US") still matches Ref.Sibling.
// Value="US".
func TestOverlay_DeltaVsSibling_FallbackResolverScansKeys(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}}
	values := []float64{100.0, 200.0, 300.0}
	// newStubSeriesHost uses NewSeriesHostView (no GroupFields slot).
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsSiblingSpec("delta", "region", "US")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	// No warning — the fallback resolver still finds "US" in the
	// single-axis key tuples even though the field list is empty.
	assertNoOverlayWarnings(t, warnings)

	want := []float64{0.0, 100.0, 200.0}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}
}
