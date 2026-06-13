package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the OVERLAY_INDEX_VS_SIBLING handler
// (processing/overlay_index_vs_sibling.go).
//
// E3-S5 scope:
//
//   - Second sibling-reference SERIES-host overlay kind. The handler is
//     wired into seriesOverlayHandlers via the dispatch entry in
//     processing/overlay_series.go.
//   - Acceptance: basic series math (per-group
//     `(value / sibling_val) * 100`), unknown-sibling emits
//     PULSE_OVERLAY_REF_UNKNOWN + NaN, ZERO-sibling emits
//     PULSE_OVERLAY_REF_ZERO + NaN (division by zero undefined),
//     absent-group passthrough stays absent, sibling-self-emission
//     yields 100.

// newIndexVsSiblingSpec returns a canonical OVERLAY_INDEX_VS_SIBLING
// spec naming the requested `(field, value)` sibling reference.
func newIndexVsSiblingSpec(name, field, value string) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindIndexVsSibling,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Sibling: &types.OverlaySiblingRef{Field: field, Value: value},
		},
	}
}

// TestOverlay_IndexVsSibling_BasicSeries pins the canonical happy path:
// three groups (US, CA, MX) with values 100, 200, 300 and sibling (region,
// US). US emits 100/100 * 100 = 100; CA emits 200/100 * 100 = 200;
// MX emits 300/100 * 100 = 300.
func TestOverlay_IndexVsSibling_BasicSeries(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}}
	values := []float64{100.0, 200.0, 300.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newIndexVsSiblingSpec("idx_sib", "region", "US")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}

	want := []float64{100.0, 200.0, 300.0}
	for i := range keys {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i, want[i], 1e-12)
	}

	// Layer-level Summary: baseline=100, Count=3, Min=100, Max=300.
	if got := *layers[0].Summary.Baseline; got != 100.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 100", got)
	}
	if got, w := *layers[0].Summary.Count, 3; got != w {
		t.Errorf("layer.Summary.Count = %v, want %v", got, w)
	}
	if got := *layers[0].Summary.Min; got != 100.0 {
		t.Errorf("layer.Summary.Min = %v, want 100", got)
	}
	if got := *layers[0].Summary.Max; got != 300.0 {
		t.Errorf("layer.Summary.Max = %v, want 300", got)
	}
}

// TestOverlay_IndexVsSibling_SelfEmits100 pins the self-vs-self
// emission: the sibling group's own entry surfaces exactly 100.0
// (sibling / sibling * 100 = 100).
func TestOverlay_IndexVsSibling_SelfEmits100(t *testing.T) {
	keys := []types.AxisKey{{"baseline"}, {"a"}, {"b"}}
	values := []float64{50.0, 75.0, 25.0}
	host := newStubSeriesHostWithField(keys, values, "grp")
	specs := []types.OverlaySpec{newIndexVsSiblingSpec("idx", "grp", "baseline")}

	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	entry := layers[0].Payload.Series.Entries[0]
	if entry.Summary.Statistic == nil {
		t.Fatalf("sibling self-emission: entries[0].Summary.Statistic nil; want 100.0")
	}
	if got := *entry.Summary.Statistic; got != 100.0 {
		t.Errorf("sibling self-emission: entries[0].Summary.Statistic = %v, want 100.0", got)
	}
}

// TestOverlay_IndexVsSibling_UnknownFieldEmitsWarn pins the unknown-
// sibling-field path: when Ref.Sibling.Field is not in the host's
// GroupFields slot the handler emits ONE PULSE_OVERLAY_REF_UNKNOWN
// warning and every present entry's Statistic is NaN.
func TestOverlay_IndexVsSibling_UnknownFieldEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{1.0, 2.0, 3.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newIndexVsSiblingSpec("idx", "bogus_field", "a")}

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

// TestOverlay_IndexVsSibling_UnknownValueEmitsWarn pins the unknown-
// sibling-value path: when Ref.Sibling.Field IS a grouper field but
// Ref.Sibling.Value does not match any observed axis-key value, the
// handler emits ONE PULSE_OVERLAY_REF_UNKNOWN warning and every
// present entry's Statistic is NaN.
func TestOverlay_IndexVsSibling_UnknownValueEmitsWarn(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{1.0, 2.0, 3.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newIndexVsSiblingSpec("idx", "region", "z")}

	_, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_UNKNOWN), 1)
}

// TestOverlay_IndexVsSibling_ZeroSiblingEmitsRefZero pins the zero-
// sibling path: when the sibling resolves but its value is zero,
// division is undefined and the handler emits ONE
// PULSE_OVERLAY_REF_ZERO warning + NaN per present entry.
// Distinct from the DELTA_VS_SIBLING twin which stays well-defined.
func TestOverlay_IndexVsSibling_ZeroSiblingEmitsRefZero(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{0.0, 7.0, 10.0}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newIndexVsSiblingSpec("idx", "region", "a")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertWarningCode(t, warnings, string(errors.PULSE_OVERLAY_REF_ZERO), 1)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d", len(entries), len(keys))
	}
	for i, e := range entries {
		if e.Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want NaN", i)
		}
		if !math.IsNaN(*e.Summary.Statistic) {
			t.Errorf("entries[%d].Summary.Statistic = %v, want NaN (zero-sibling path)", i, *e.Summary.Statistic)
		}
	}
}

// TestOverlay_IndexVsSibling_AbsentGroupsStayAbsent pins the absent-
// group contract.
func TestOverlay_IndexVsSibling_AbsentGroupsStayAbsent(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	values := []float64{10.0, math.NaN(), 30.0, math.NaN()}
	host := newStubSeriesHostWithField(keys, values, "region")
	specs := []types.OverlaySpec{newIndexVsSiblingSpec("idx", "region", "a")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	// a: 10/10 * 100 = 100 (sibling self)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 0, 100.0, 1e-12)
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1] (absent) Statistic = %v, want nil", *entries[1].Summary.Statistic)
	}
	// c: 30/10 * 100 = 300
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 300.0, 1e-12)
	if entries[3].Summary.Statistic != nil {
		t.Errorf("entries[3] (absent) Statistic = %v, want nil", *entries[3].Summary.Statistic)
	}
}

// TestOverlay_IndexVsSibling_DefaultLayerName pins the synthesised
// label when the spec omits Name.
func TestOverlay_IndexVsSibling_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHostWithField(keys, []float64{1.0, 2.0}, "region")
	specs := []types.OverlaySpec{{
		Kind:  types.OverlayKindIndexVsSibling,
		Scope: types.OverlayScopeGroup,
		Ref: types.OverlayRef{
			Sibling: &types.OverlaySiblingRef{Field: "region", Value: "a"},
		},
	}}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "index_vs_sibling"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}
