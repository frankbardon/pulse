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

// TestErrorsLookup_OverlayParamMissing_SymmetryExactFixups: the
// PULSE_OVERLAY_PARAM_MISSING answer offers the two k x k exact
// OVERLAY_SYMMETRY ways out (U66 E10-S1) — REMOVE_PARAM on
// Overlays[*].Params.exact for exact_too_large and on
// Overlays[*].Params.conf_level for needs_2x2 — and its message names
// both reasons.
func TestErrorsLookup_OverlayParamMissing_SymmetryExactFixups(t *testing.T) {
	t.Parallel()

	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_PARAM_MISSING))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_PARAM_MISSING")
	}
	for _, reason := range []string{"exact_too_large", "needs_2x2"} {
		if !strings.Contains(r.Message, reason) {
			t.Errorf("message lacks reason %s", reason)
		}
	}
	for param, reason := range map[string]string{"exact": "exact_too_large", "conf_level": "needs_2x2"} {
		found := false
		for _, f := range r.Fixups {
			if slices.Equal(f.Path, []string{"Overlays", "*", "Params", param}) && f.Action == perr.FixupRemoveParam &&
				strings.Contains(f.Hint, reason) && strings.Contains(f.Hint, "OVERLAY_SYMMETRY") {
				found = true
			}
		}
		if !found {
			t.Errorf("no REMOVE_PARAM fixup on Overlays.*.Params.%s for %s among %+v", param, reason, r.Fixups)
		}
	}
}
