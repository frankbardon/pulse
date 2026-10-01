package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// newDeltaVsPriorSpec returns the canonical happy-path
// OVERLAY_DELTA_VS_PRIOR spec the per-test fixtures consume — GROUP
// scope, empty Ref (the implicit-default authoring shape for the lag-1
// windowed kind), and a deterministic name so the layer's renderer-
// facing label is pinned in the assertions. Mirrors
// newIndexVsPriorSpec.
func newDeltaVsPriorSpec(name string) types.OverlaySpec {
	return types.OverlaySpec{
		Name:  name,
		Kind:  types.OverlayKindDeltaVsPrior,
		Scope: types.OverlayScopeGroup,
		Ref:   types.OverlayRef{},
	}
}

// TestOverlay_DeltaVsPrior_BasicLag1 pins the canonical happy path:
// five points [10, 20, 30, 40, 50] produce deltas
// [NaN, +10, +10, +10, +10]. First point has no prior → NaN (without
// warning). Subsequent points emit the lag-1 difference in the metric's
// own units.
func TestOverlay_DeltaVsPrior_BasicLag1(t *testing.T) {
	keys := []types.AxisKey{{"jan"}, {"feb"}, {"mar"}, {"apr"}, {"may"}}
	values := []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsPriorSpec("delta_prior")}

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

	const tol = 1e-9
	wants := []float64{10.0, 10.0, 10.0, 10.0}
	for i, w := range wants {
		assertSeriesEntryStatisticWithinTol(t, &layers[0], i+1, w, tol)
	}

	// Layer-level summary: baseline = 0 (the additive family, NOT the
	// index family's 100), Count = 4 (the first-point NaN does not
	// contribute).
	if layers[0].Summary == nil {
		t.Fatalf("layer.Summary nil; want populated")
	}
	if got := *layers[0].Summary.Baseline; got != 0.0 {
		t.Errorf("layer.Summary.Baseline = %v, want 0 (additive-delta family)", got)
	}
	if got, want := *layers[0].Summary.Count, 4; got != want {
		t.Errorf("layer.Summary.Count = %v, want %v", got, want)
	}
}

// TestOverlay_DeltaVsPrior_NegativeAndMixed pins that the kind is
// SIGNED. The index twin cannot express a decline as anything but a
// value below 100; a delta reports it as a negative number in the
// metric's own units, which is the whole reason both kinds exist.
func TestOverlay_DeltaVsPrior_NegativeAndMixed(t *testing.T) {
	keys := []types.AxisKey{{"q1"}, {"q2"}, {"q3"}, {"q4"}}
	values := []float64{95.8, 96.4, 94.1, 97.9}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsPriorSpec("delta_prior")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	const tol = 1e-9
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 1, 96.4-95.8, tol)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 94.1-96.4, tol)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 3, 97.9-94.1, tol)

	// The declining quarter must be strictly negative — a delta that
	// reported a fall as a positive number would be the single worst
	// failure this kind could have.
	if got := *layers[0].Payload.Series.Entries[2].Summary.Statistic; got >= 0 {
		t.Errorf("entries[2].Summary.Statistic = %v, want a negative delta for a declining point", got)
	}

	// Min/Max span the signed range rather than a ratio range.
	if got := *layers[0].Summary.Min; got >= 0 {
		t.Errorf("layer.Summary.Min = %v, want negative (the declining quarter)", got)
	}
	if got := *layers[0].Summary.Max; got <= 0 {
		t.Errorf("layer.Summary.Max = %v, want positive", got)
	}
}

// TestOverlay_DeltaVsPrior_FirstPointNaN pins the single-point host
// degenerate case: one present ordinal emits NaN (no prior available)
// with NO warnings. A delta of zero here would CLAIM that nothing
// changed about a comparison that was never made.
func TestOverlay_DeltaVsPrior_FirstPointNaN(t *testing.T) {
	keys := []types.AxisKey{{"only"}}
	values := []float64{42.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsPriorSpec("delta_prior")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(entries))
	}
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN", entries[0].Summary.Statistic)
	}
	if got := *layers[0].Summary.Count; got != 0 {
		t.Errorf("layer.Summary.Count = %v, want 0 (no non-NaN entries)", got)
	}
}

// TestOverlay_DeltaVsPrior_ZeroPriorIsValidNotAWarning is the assertion
// that separates this kind from its index twin, and the reason the twin
// cannot simply be reused with a different arithmetic step.
//
// A prior of exactly zero is a perfectly good subtrahend: `5 - 0` is
// `5`. INDEX_VS_PRIOR must emit NaN and PULSE_OVERLAY_REF_ZERO on this
// input because the ratio is undefined. DELTA_VS_PRIOR must emit the
// real difference and NO warning. If this test ever fails because a
// warning appeared, someone has "restored" a zero-ref branch that was
// deliberately left out.
func TestOverlay_DeltaVsPrior_ZeroPriorIsValidNotAWarning(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{4.0, 0.0, 5.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsPriorSpec("delta_prior")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}

	// No PULSE_OVERLAY_REF_ZERO — and in fact no warnings at all.
	for _, w := range warnings {
		if w.Code == string(errors.PULSE_OVERLAY_REF_ZERO) {
			t.Fatalf("delta overlay emitted PULSE_OVERLAY_REF_ZERO; a zero prior is a valid subtrahend for subtraction")
		}
	}
	assertNoOverlayWarnings(t, warnings)

	const tol = 1e-9
	// b - a = 0 - 4 = -4
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 1, -4.0, tol)
	// c - b = 5 - 0 = 5 (the branch the index twin cannot compute)
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 5.0, tol)
}

// TestOverlay_DeltaVsPrior_AbsentPointPreservesPriorCarrier pins the
// absent-point contract: an absent host ordinal emits a present entry
// with an unset Statistic and does NOT advance the lag carrier, so the
// next present point differences against the last PRESENT value.
func TestOverlay_DeltaVsPrior_AbsentPointPreservesPriorCarrier(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, math.NaN(), 30.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{newDeltaVsPriorSpec("delta_prior")}

	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	assertNoOverlayWarnings(t, warnings)

	entries := layers[0].Payload.Series.Entries
	if len(entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3", len(entries))
	}
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0].Summary.Statistic = %v, want NaN (first point)", entries[0].Summary.Statistic)
	}
	if entries[1].Summary.Statistic != nil {
		t.Errorf("entries[1].Summary.Statistic = %v, want nil (absent group)", *entries[1].Summary.Statistic)
	}
	// entry[2]: 30 - 10 = 20 (lag carrier survived the absent slot; it
	// is NOT 30 - 0).
	assertSeriesEntryStatisticWithinTol(t, &layers[0], 2, 20.0, 1e-9)
}

// TestOverlay_DeltaVsPrior_RefPriorPopulatedAccepted verifies the
// `Ref.Prior` populated authoring shape produces the same output as the
// empty-Ref shape — both are the lag-1 implicit-default spellings.
func TestOverlay_DeltaVsPrior_RefPriorPopulatedAccepted(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 25.0, 40.0}

	emptyRef := newDeltaVsPriorSpec("delta_prior")
	populated := newDeltaVsPriorSpec("delta_prior")
	populated.Ref = types.OverlayRef{Prior: &types.OverlayPriorRef{}}

	layersA, _, err := ApplyOverlaysSeries([]types.OverlaySpec{emptyRef}, newStubSeriesHost(keys, values))
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries(empty ref): %v", err)
	}
	layersB, _, err := ApplyOverlaysSeries([]types.OverlaySpec{populated}, newStubSeriesHost(keys, values))
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries(populated ref): %v", err)
	}

	entriesA := layersA[0].Payload.Series.Entries
	entriesB := layersB[0].Payload.Series.Entries
	if len(entriesA) != len(entriesB) {
		t.Fatalf("entry counts differ: %d vs %d", len(entriesA), len(entriesB))
	}
	for i := range entriesA {
		a, b := entriesA[i].Summary.Statistic, entriesB[i].Summary.Statistic
		switch {
		case a == nil && b == nil:
		case a == nil || b == nil:
			t.Errorf("entry %d: statistic presence differs (%v vs %v)", i, a, b)
		case math.IsNaN(*a) && math.IsNaN(*b):
		case *a != *b:
			t.Errorf("entry %d: %v != %v", i, *a, *b)
		}
	}
}

// TestOverlay_DeltaVsPrior_DefaultLayerName pins the synthesised
// renderer-facing label when the spec carries no explicit Name.
func TestOverlay_DeltaVsPrior_DefaultLayerName(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHost(keys, []float64{1.0, 2.0})
	spec := newDeltaVsPriorSpec("")

	layers, _, err := ApplyOverlaysSeries([]types.OverlaySpec{spec}, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if got, want := layers[0].Name, "delta_vs_prior"; got != want {
		t.Errorf("layer.Name = %q, want %q", got, want)
	}
}

// TestOverlay_DeltaVsPrior_NilHostReturnsCoded pins the defense-in-depth
// branch: a nil host is a coded PROCESSING_INTERNAL error rather than a
// panic.
func TestOverlay_DeltaVsPrior_NilHostReturnsCoded(t *testing.T) {
	spec := newDeltaVsPriorSpec("delta_prior")
	_, _, err := applyDeltaVsPrior(&spec, nil)
	if err == nil {
		t.Fatal("applyDeltaVsPrior(nil host) returned nil error; want coded PROCESSING_INTERNAL")
	}
}
