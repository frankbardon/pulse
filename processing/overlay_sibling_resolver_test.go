package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Tests for the sibling resolver helper
// (processing/overlay_sibling_resolver.go).
//
// E3-S5 scope:
//
//   - Single-field resolver shape: when the host has a GroupFields slot
//     the resolver maps `Sibling.Field` to the matching axis-key
//     element via that slot.
//   - Fallback scan: when the host has NO GroupFields slot the resolver
//     scans every axis-key element for a match against `Sibling.Value`
//     — byte-equivalent on a single-axis host.
//   - Defensive returns: nil host, empty field, empty value, unknown
//     field, unknown value, present=false from the host's own resolver
//     all return `(0, false)`.

// TestResolveSibling_KnownFieldAndValue exercises the happy path with
// the GroupFields slot populated.
func TestResolveSibling_KnownFieldAndValue(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}}
	values := []float64{100.0, 200.0, 300.0}
	host := newStubSeriesHostWithField(keys, values, "region")

	got, present := resolveSibling(host, "region", "CA")
	if !present {
		t.Fatalf("resolveSibling returned present=false; want present=true")
	}
	if got != 200.0 {
		t.Errorf("resolveSibling = %v, want 200.0", got)
	}
}

// TestResolveSibling_UnknownField pins the unknown-field path: the
// field list is populated but does not contain the requested field, so
// the resolver returns (0, false).
func TestResolveSibling_UnknownField(t *testing.T) {
	keys := []types.AxisKey{{"US"}}
	host := newStubSeriesHostWithField(keys, []float64{42.0}, "region")
	got, present := resolveSibling(host, "bogus", "US")
	if present {
		t.Errorf("resolveSibling returned present=true; want false (unknown field)")
	}
	if got != 0 {
		t.Errorf("resolveSibling value = %v, want 0", got)
	}
}

// TestResolveSibling_UnknownValue pins the unknown-value path: the
// field exists but the requested value is not in any observed axis
// key.
func TestResolveSibling_UnknownValue(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}}
	host := newStubSeriesHostWithField(keys, []float64{1.0, 2.0}, "region")
	got, present := resolveSibling(host, "region", "MX")
	if present {
		t.Errorf("resolveSibling returned present=true; want false (unknown value)")
	}
	if got != 0 {
		t.Errorf("resolveSibling value = %v, want 0", got)
	}
}

// TestResolveSibling_FallbackScanFindsMatch pins the fallback scan
// path: when the host has no GroupFields slot the resolver matches
// against every element of every axis key.
func TestResolveSibling_FallbackScanFindsMatch(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}}
	// newStubSeriesHost — no GroupFields slot.
	host := newStubSeriesHost(keys, []float64{10.0, 20.0, 30.0})
	got, present := resolveSibling(host, "anyfield", "CA")
	if !present {
		t.Fatalf("resolveSibling (fallback scan) present=false; want true")
	}
	if got != 20.0 {
		t.Errorf("resolveSibling (fallback scan) = %v, want 20.0", got)
	}
}

// TestResolveSibling_HostAbsentValueReturnsPresentFalse pins the
// "matched but host did not produce a value" branch: the resolver
// finds the group ordinal but the host's resolver itself reports
// absent.
func TestResolveSibling_HostAbsentValueReturnsPresentFalse(t *testing.T) {
	keys := []types.AxisKey{{"US"}, {"CA"}}
	// NaN signals absent (newStubSeriesHostWithField stub maps NaN to
	// (0, false)).
	host := newStubSeriesHostWithField(keys, []float64{math.NaN(), 50.0}, "region")
	got, present := resolveSibling(host, "region", "US")
	if present {
		t.Errorf("resolveSibling returned present=true; want false (host group absent)")
	}
	if got != 0 {
		t.Errorf("resolveSibling value = %v, want 0", got)
	}
}

// TestResolveSibling_NilOrEmptyArgsReturnAbsent pins the defensive
// returns: nil host, empty field, empty value all return (0, false)
// without panicking.
func TestResolveSibling_NilOrEmptyArgsReturnAbsent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		host  *SeriesHostView
		field string
		value string
	}{
		{"nil host", nil, "region", "US"},
		{"empty field", newStubSeriesHostWithField([]types.AxisKey{{"US"}}, []float64{1.0}, "region"), "", "US"},
		{"empty value", newStubSeriesHostWithField([]types.AxisKey{{"US"}}, []float64{1.0}, "region"), "region", ""},
		{"zero-group host", newStubSeriesHostWithField(nil, nil, "region"), "region", "US"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, present := resolveSibling(tc.host, tc.field, tc.value)
			if present {
				t.Errorf("present=true; want false (%s)", tc.name)
			}
			if got != 0 {
				t.Errorf("value = %v, want 0 (%s)", got, tc.name)
			}
		})
	}
}
