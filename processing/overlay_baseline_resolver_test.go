package processing

import (
	"math"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the baseline-index resolver helper
// (processing/overlay_baseline_resolver.go).
//
// E4-S1 scope: structural foundation for the windowed-Process overlay
// kinds that consume the BaselineIndex.Position arm in S2 / S3 / S5 /
// S7. The resolver maps a 0-based ordinal on the host's ordered SERIES
// axis to a single baseline value; out-of-range slots fail with the
// canonical PULSE_OVERLAY_REF_UNKNOWN code carrying the
// `{baseline_index, series_length}` Details map shape.

// TestResolveBaselineIndex_HappyPath exercises the canonical resolver
// success path against a three-group ordered series. Position 0 → 100,
// Position 1 → 200, Position 2 → 300 — same series, same Position,
// byte-equal value on every invocation.
func TestResolveBaselineIndex_HappyPath(t *testing.T) {
	keys := []types.AxisKey{{"2024-01"}, {"2024-02"}, {"2024-03"}}
	values := []float64{100.0, 200.0, 300.0}
	host := newStubSeriesHost(keys, values)

	cases := []struct {
		position int
		want     float64
	}{
		{0, 100.0},
		{1, 200.0},
		{2, 300.0},
	}
	for _, tc := range cases {
		got, err := ResolveBaselineIndex(host, &types.OverlayBaselineIndexRef{Position: tc.position})
		if err != nil {
			t.Fatalf("ResolveBaselineIndex(position=%d) err = %v; want nil", tc.position, err)
		}
		if got != tc.want {
			t.Errorf("ResolveBaselineIndex(position=%d) = %v; want %v", tc.position, got, tc.want)
		}
	}
}

// TestResolveBaselineIndex_NilRefShortCircuits pins the nil-ref branch:
// the resolver returns `(0, nil)` so the caller can short-circuit
// cleanly when no BaselineIndex ref was supplied.
func TestResolveBaselineIndex_NilRefShortCircuits(t *testing.T) {
	host := newStubSeriesHost([]types.AxisKey{{"x"}}, []float64{1.0})
	got, err := ResolveBaselineIndex(host, nil)
	if err != nil {
		t.Fatalf("ResolveBaselineIndex(nil ref) err = %v; want nil", err)
	}
	if got != 0 {
		t.Errorf("ResolveBaselineIndex(nil ref) value = %v; want 0", got)
	}
}

// TestResolveBaselineIndex_NegativeRejected pins the negative-Position
// rejection: any negative ordinal fires PULSE_OVERLAY_REF_UNKNOWN with
// `{baseline_index, series_length}` Details.
func TestResolveBaselineIndex_NegativeRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{1.0, 2.0, 3.0}
	host := newStubSeriesHost(keys, values)

	for _, position := range []int{-1, -42} {
		got, err := ResolveBaselineIndex(host, &types.OverlayBaselineIndexRef{Position: position})
		if err == nil {
			t.Fatalf("ResolveBaselineIndex(position=%d) err = nil; want PULSE_OVERLAY_REF_UNKNOWN", position)
		}
		if got != 0 {
			t.Errorf("ResolveBaselineIndex(position=%d) value = %v; want 0 on rejection", position, got)
		}
		assertBaselineIndexCodedError(t, err, position, 3)
	}
}

// TestResolveBaselineIndex_OutOfRangeRejected pins the out-of-range
// rejection: `Position >= host.GroupCount()` fires
// PULSE_OVERLAY_REF_UNKNOWN with the same Details shape.
func TestResolveBaselineIndex_OutOfRangeRejected(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 20.0, 30.0}
	host := newStubSeriesHost(keys, values)

	for _, position := range []int{3, 4, 99} {
		got, err := ResolveBaselineIndex(host, &types.OverlayBaselineIndexRef{Position: position})
		if err == nil {
			t.Fatalf("ResolveBaselineIndex(position=%d) err = nil; want PULSE_OVERLAY_REF_UNKNOWN", position)
		}
		if got != 0 {
			t.Errorf("ResolveBaselineIndex(position=%d) value = %v; want 0 on rejection", position, got)
		}
		assertBaselineIndexCodedError(t, err, position, 3)
	}
}

// TestResolveBaselineIndex_EmptyHostRejected pins the zero-length-host
// arm: Position 0 against an empty host series is still out-of-range
// (0 >= 0) and fires PULSE_OVERLAY_REF_UNKNOWN with `series_length: 0`.
func TestResolveBaselineIndex_EmptyHostRejected(t *testing.T) {
	host := newStubSeriesHost(nil, nil)
	got, err := ResolveBaselineIndex(host, &types.OverlayBaselineIndexRef{Position: 0})
	if err == nil {
		t.Fatalf("ResolveBaselineIndex(empty host) err = nil; want PULSE_OVERLAY_REF_UNKNOWN")
	}
	if got != 0 {
		t.Errorf("ResolveBaselineIndex(empty host) value = %v; want 0", got)
	}
	assertBaselineIndexCodedError(t, err, 0, 0)
}

// TestResolveBaselineIndex_AbsentHostValuePassesThrough pins the
// absent-host-value branch: the host's resolver reports the group as
// absent (NaN → present=false) but the requested ordinal is structurally
// in range. The resolver returns whatever the host produced (zero in
// this case) without raising — the per-kind handler decides whether to
// surface a layer-level warning. The structural validation is the
// resolver's only responsibility.
func TestResolveBaselineIndex_AbsentHostValuePassesThrough(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{math.NaN(), 2.0, 3.0}
	host := newStubSeriesHost(keys, values)

	got, err := ResolveBaselineIndex(host, &types.OverlayBaselineIndexRef{Position: 0})
	if err != nil {
		t.Fatalf("ResolveBaselineIndex(present=false) err = %v; want nil", err)
	}
	if got != 0 {
		t.Errorf("ResolveBaselineIndex(present=false) value = %v; want 0", got)
	}
}

// TestResolveBaselineIndex_Deterministic pins the order-stable contract:
// the resolver returns byte-equal values on repeated invocations against
// the same host + same Position. Guards against future drift where a
// caller might fold state into the resolver.
func TestResolveBaselineIndex_Deterministic(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	values := []float64{1.5, 2.5, 3.5, 4.5}
	host := newStubSeriesHost(keys, values)

	for position := 0; position < len(values); position++ {
		ref := &types.OverlayBaselineIndexRef{Position: position}
		first, err := ResolveBaselineIndex(host, ref)
		if err != nil {
			t.Fatalf("first call err = %v", err)
		}
		for i := 0; i < 8; i++ {
			again, err := ResolveBaselineIndex(host, ref)
			if err != nil {
				t.Fatalf("repeat call %d err = %v", i, err)
			}
			if again != first {
				t.Fatalf("repeat call %d position=%d = %v; first = %v",
					i, position, again, first)
			}
		}
	}
}

// TestResolveBaselineIndex_RowColumnIgnored pins the forward-compat
// contract: the MATRIX-host arm slots (Row + Column) are ignored by the
// SERIES-host resolver. A ref carrying both Position and Row/Column
// returns the Position-indexed value cleanly so future MATRIX kinds can
// land without breaking the SERIES handlers' resolver call shape.
func TestResolveBaselineIndex_RowColumnIgnored(t *testing.T) {
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	values := []float64{10.0, 20.0, 30.0}
	host := newStubSeriesHost(keys, values)

	ref := &types.OverlayBaselineIndexRef{
		Position: 1,
		Row:      []string{"reserved_row"},
		Column:   []string{"reserved_col"},
	}
	got, err := ResolveBaselineIndex(host, ref)
	if err != nil {
		t.Fatalf("ResolveBaselineIndex(Position + Row/Column) err = %v; want nil", err)
	}
	if got != 20.0 {
		t.Errorf("ResolveBaselineIndex(Position + Row/Column) = %v; want 20.0", got)
	}
}

// assertBaselineIndexCodedError asserts the err carries Details with
// the canonical `PULSE_OVERLAY_REF_UNKNOWN` code, the expected
// `baseline_index`, and the expected `series_length`. Defense-in-depth
// helper for the negative + out-of-range paths.
func assertBaselineIndexCodedError(t *testing.T, err error, wantBaselineIndex, wantSeriesLength int) {
	t.Helper()
	coded, ok := err.(*pulseerrors.CodedError)
	if !ok {
		t.Fatalf("err = %T; want *errors.CodedError. err = %v", err, err)
	}
	if coded.Details == nil {
		t.Fatalf("coded.Details is nil; want map carrying code + baseline_index + series_length")
	}
	codeRaw, ok := coded.Details["code"].(string)
	if !ok {
		t.Fatalf("coded.Details[code] = %v (%T); want string", coded.Details["code"], coded.Details["code"])
	}
	if codeRaw != string(pulseerrors.PULSE_OVERLAY_REF_UNKNOWN) {
		t.Fatalf("coded.Details[code] = %q; want %q",
			codeRaw, pulseerrors.PULSE_OVERLAY_REF_UNKNOWN)
	}
	gotBI, ok := coded.Details["baseline_index"].(int)
	if !ok {
		t.Fatalf("coded.Details[baseline_index] = %v (%T); want int",
			coded.Details["baseline_index"], coded.Details["baseline_index"])
	}
	if gotBI != wantBaselineIndex {
		t.Errorf("coded.Details[baseline_index] = %d; want %d", gotBI, wantBaselineIndex)
	}
	gotSL, ok := coded.Details["series_length"].(int)
	if !ok {
		t.Fatalf("coded.Details[series_length] = %v (%T); want int",
			coded.Details["series_length"], coded.Details["series_length"])
	}
	if gotSL != wantSeriesLength {
		t.Errorf("coded.Details[series_length] = %d; want %d", gotSL, wantSeriesLength)
	}
	// Sanity: message references the host series so a callsite reader
	// recognises the failure mode without parsing the details map.
	if !strings.Contains(coded.Message, "baseline-index") {
		t.Errorf("coded.Message = %q; want substring %q",
			coded.Message, "baseline-index")
	}
}
