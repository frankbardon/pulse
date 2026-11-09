package errors_test

import (
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestErrorsLookup_OverlayExpectedLow_FixupPointsAtExact: the
// PULSE_OVERLAY_EXPECTED_LOW answer offers OVERLAY_SYMMETRY's exact
// route — a REPLACE_FIELD fixup on Overlays[*].Params.exact with the
// value true — beside the coarsen-the-table fixes (U66 E6-S2).
func TestErrorsLookup_OverlayExpectedLow_FixupPointsAtExact(t *testing.T) {
	t.Parallel()

	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_EXPECTED_LOW))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_EXPECTED_LOW")
	}
	for _, f := range r.Fixups {
		if !slices.Equal(f.Path, []string{"Overlays", "*", "Params", "exact"}) {
			continue
		}
		if f.Action != perr.FixupReplaceField || !strings.Contains(f.Hint, "OVERLAY_SYMMETRY") ||
			!slices.Contains(f.Examples, any(true)) {
			t.Errorf("exact fixup %+v, want REPLACE_FIELD naming OVERLAY_SYMMETRY with example true", f)
		}
		return
	}
	t.Errorf("no fixup on Overlays.*.Params.exact among %+v", r.Fixups)
}
