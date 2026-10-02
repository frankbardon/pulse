package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

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

func TestResolveSibling_EmptySeriesReturnsAbsent(t *testing.T) {
	host := newStubSeriesHostWithField(nil, nil, "region")
	got, present := resolveSibling(host, "region", "US")
	if present {
		t.Errorf("resolveSibling on empty series returned present=true; want false")
	}
	if got != 0 {
		t.Errorf("resolveSibling on empty series value = %v, want 0", got)
	}
}

func TestResolveSibling_SingleEntrySeriesHits(t *testing.T) {
	keys := []types.AxisKey{{"US"}}
	host := newStubSeriesHostWithField(keys, []float64{42.0}, "region")

	// Matching path.
	got, present := resolveSibling(host, "region", "US")
	if !present {
		t.Fatalf("single-entry hit returned present=false; want true")
	}
	if got != 42.0 {
		t.Errorf("single-entry hit value = %v, want 42.0", got)
	}

	// Non-matching value on the same one-group host.
	got, present = resolveSibling(host, "region", "CA")
	if present {
		t.Errorf("single-entry non-match returned present=true; want false")
	}
	if got != 0 {
		t.Errorf("single-entry non-match value = %v, want 0", got)
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
