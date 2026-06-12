package errors_test

import (
	"encoding/json"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestErrorsLookup_YoY_FrequencyMissing_FixupCount asserts the
// PULSE_OVERLAY_YOY_FREQUENCY_MISSING entry surfaces at least two
// fixup templates. The E4-S8 polish bar required upgrading the S7
// single-template form to a multi-template form that gives the caller
// the choice between authoring the frequency on the host GROUP_DATE
// grouper or on the OverlaySpec itself.
func TestErrorsLookup_YoY_FrequencyMissing_FixupCount(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_YOY_FREQUENCY_MISSING))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_YOY_FREQUENCY_MISSING")
	}
	if r.FixupNotApplicable {
		t.Errorf("FixupNotApplicable=true, want false for a polish-bar entry")
	}
	if got := len(r.Fixups); got < 2 {
		t.Errorf("len(Fixups)=%d, want >= 2 (E4-S8 polish bar)", got)
	}
}

// TestErrorsLookup_YoY_FrequencyMissing_MessageMentionsFrequency
// asserts the Message body names both the offending `frequency` Param
// and the host `GROUP_DATE` grouper so a reader can locate the slot
// without cross-referencing the predict path.
func TestErrorsLookup_YoY_FrequencyMissing_MessageMentionsFrequency(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_YOY_FREQUENCY_MISSING))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_YOY_FREQUENCY_MISSING")
	}
	lower := strings.ToLower(r.Message)
	if !strings.Contains(lower, "frequency") {
		t.Errorf("Message does not mention `frequency`: %q", r.Message)
	}
	if !strings.Contains(r.Message, "GROUP_DATE") {
		t.Errorf("Message does not mention `GROUP_DATE`: %q", r.Message)
	}
}

// TestErrorsLookup_YoY_FrequencyMissing_FixupsCoverBothAuthoringSlots
// asserts the two fixups target distinct authoring paths — one
// pointing at Groups[0].Params (the canonical grouper slot) and one
// pointing at Overlays[*].Params (the per-overlay override slot).
// Together they give the caller a complete repair surface regardless
// of who owns the host GROUP_DATE config.
func TestErrorsLookup_YoY_FrequencyMissing_FixupsCoverBothAuthoringSlots(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_YOY_FREQUENCY_MISSING))
	if len(r.Fixups) < 2 {
		t.Skip("not enough fixups to test slot coverage")
	}
	sawGroups, sawOverlays := false, false
	for _, f := range r.Fixups {
		path := strings.Join(f.Path, "/")
		if strings.HasPrefix(path, "Groups/") {
			sawGroups = true
		}
		if strings.HasPrefix(path, "Overlays/") {
			sawOverlays = true
		}
	}
	if !sawGroups {
		t.Errorf("no fixup targets the Groups[0].Params authoring slot")
	}
	if !sawOverlays {
		t.Errorf("no fixup targets the Overlays[*].Params authoring slot")
	}
}

// TestErrorsLookup_YoY_IncompatibleFrequency_FixupCount asserts the
// PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY entry carries at least two
// fixup templates. The E4-S8 polish bar covers both substitution
// (swap the frequency string in place) and granularity-shift (switch
// the host grouper to a coarser component when the raw data is
// finer-grained than the supported catalog).
func TestErrorsLookup_YoY_IncompatibleFrequency_FixupCount(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY")
	}
	if r.FixupNotApplicable {
		t.Errorf("FixupNotApplicable=true, want false for a polish-bar entry")
	}
	if got := len(r.Fixups); got < 2 {
		t.Errorf("len(Fixups)=%d, want >= 2 (E4-S8 polish bar)", got)
	}
}

// TestErrorsLookup_YoY_IncompatibleFrequency_FixupEnumeratesSupported
// asserts at least one fixup template body lists every supported
// frequency the YoY catalog accepts — annual / quarterly / monthly /
// weekly / daily / hourly. A reader hitting this code should see the
// complete supported set without having to consult the validator.
func TestErrorsLookup_YoY_IncompatibleFrequency_FixupEnumeratesSupported(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY")
	}
	supported := []string{"annual", "quarterly", "monthly", "weekly", "daily", "hourly"}
	for _, f := range r.Fixups {
		hint := strings.ToLower(f.Hint)
		hasAll := true
		for _, freq := range supported {
			if !strings.Contains(hint, freq) {
				hasAll = false
				break
			}
		}
		if hasAll {
			return
		}
	}
	// No Hint covered every frequency — try the marshalled-examples body
	// as a fallback so an Examples-driven enumeration also passes the gate.
	for _, f := range r.Fixups {
		blob, err := json.Marshal(f.Examples)
		if err != nil {
			continue
		}
		body := strings.ToLower(string(blob))
		hasAll := true
		for _, freq := range supported {
			if !strings.Contains(body, freq) {
				hasAll = false
				break
			}
		}
		if hasAll {
			return
		}
	}
	t.Errorf("no fixup template (Hint or Examples) enumerates the full supported set %v", supported)
}

// TestErrorsLookup_YoY_IncompatibleFrequency_MentionsGrouperFallback
// asserts at least one fixup walks the reader through the
// granularity-shift alternative — switching the host GROUP_DATE
// grouper to a coarser component when raw data is finer-grained
// than the supported catalog. The literal worked example from the
// story brief is the "hourly raw data ⇒ daily" lift.
func TestErrorsLookup_YoY_IncompatibleFrequency_MentionsGrouperFallback(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY))
	sawGrouperHint := false
	for _, f := range r.Fixups {
		path := strings.Join(f.Path, "/")
		if !strings.HasPrefix(path, "Groups/") {
			continue
		}
		hint := strings.ToLower(f.Hint)
		// The granularity-shift fixup walks coarser components; "GROUP_DATE"
		// must appear because we're naming the host grouper, and either
		// "coarser" or "component" must appear because we're describing
		// the shift.
		if strings.Contains(hint, "group_date") && (strings.Contains(hint, "coarser") || strings.Contains(hint, "component")) {
			sawGrouperHint = true
			break
		}
	}
	if !sawGrouperHint {
		t.Errorf("no Groups-targeted fixup explains the coarser-component fallback path")
	}
}
