package errors_test

import (
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestErrorsLookup_OverlayReferenceUnknown_FixupCount asserts the
// PULSE_OVERLAY_REFERENCE_UNKNOWN entry surfaces at least two fixup
// templates. The E7-S13 polish bar required upgrading the S6 minimal
// form to a multi-template form that covers both the Compose slot-label
// arm and the chain StageRef arm so a single envelope hit gives the
// caller a complete repair surface regardless of the host family.
func TestErrorsLookup_OverlayReferenceUnknown_FixupCount(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_REFERENCE_UNKNOWN))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_REFERENCE_UNKNOWN")
	}
	if r.FixupNotApplicable {
		t.Errorf("FixupNotApplicable=true, want false for a polish-bar entry")
	}
	if got := len(r.Fixups); got < 2 {
		t.Errorf("len(Fixups)=%d, want >= 2 (E7-S13 polish bar)", got)
	}
}

// TestErrorsLookup_OverlayReferenceUnknown_MessageMentionsComposeAndChain
// asserts the Message body covers both host families — Compose
// slot-label resolution AND chain StageRef resolution — so a reader
// sees the full surface without having to chase the call site.
func TestErrorsLookup_OverlayReferenceUnknown_MessageMentionsComposeAndChain(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_REFERENCE_UNKNOWN))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_REFERENCE_UNKNOWN")
	}
	if !strings.Contains(r.Message, "ComposedRequest") {
		t.Errorf("Message does not mention ComposedRequest: %q", r.Message)
	}
	if !strings.Contains(r.Message, "ChainOverlaySpec") {
		t.Errorf("Message does not mention ChainOverlaySpec: %q", r.Message)
	}
}

// TestErrorsLookup_OverlayReferenceUnknown_FixupsCoverComposeAndChainSlots
// asserts the fixup catalogue covers BOTH the Compose Reference slot
// (Overlays[*].Reference) AND the chain Ref StageRef slot
// (Overlays[*].Ref). Together they give the caller a complete repair
// surface regardless of which host family fired the code.
func TestErrorsLookup_OverlayReferenceUnknown_FixupsCoverComposeAndChainSlots(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_REFERENCE_UNKNOWN))
	if len(r.Fixups) < 2 {
		t.Skip("not enough fixups to test slot coverage")
	}
	sawCompose, sawChain := false, false
	for _, f := range r.Fixups {
		path := strings.Join(f.Path, "/")
		// Compose arm: ["Overlays", "*", "Reference"] — exact match.
		if path == "Overlays/*/Reference" {
			sawCompose = true
		}
		// Chain arm: ["Overlays", "*", "Ref", "Index"] or
		// ["Overlays", "*", "Ref", "Name"] — both qualify.
		if strings.HasPrefix(path, "Overlays/*/Ref/") {
			sawChain = true
		}
	}
	if !sawCompose {
		t.Errorf("no fixup targets the Compose Overlays[*].Reference slot")
	}
	if !sawChain {
		t.Errorf("no fixup targets the chain Overlays[*].Ref/{Index,Name} slot")
	}
}

// TestErrorsLookup_OverlayReferenceUnknown_FixupHintMentionsRequestsLabel
// asserts the Compose-arm fixup hint mentions the canonical authoring
// slot the story brief calls out — `ComposedRequest.Requests[].Label`
// — so a caller hitting this code sees the exact location to edit.
func TestErrorsLookup_OverlayReferenceUnknown_FixupHintMentionsRequestsLabel(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_REFERENCE_UNKNOWN))
	for _, f := range r.Fixups {
		if strings.Join(f.Path, "/") != "Overlays/*/Reference" {
			continue
		}
		if strings.Contains(f.Hint, "ComposedRequest.Requests[].Label") {
			return
		}
	}
	t.Errorf("Compose-arm fixup hint does not mention ComposedRequest.Requests[].Label")
}

// TestErrorsLookup_OverlayTargetUnknown_FixupCount asserts the
// PULSE_OVERLAY_TARGET_UNKNOWN entry surfaces at least two fixup
// templates. Same polish bar as the Reference sibling — cover both
// host families in one canonical entry.
func TestErrorsLookup_OverlayTargetUnknown_FixupCount(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_TARGET_UNKNOWN))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_TARGET_UNKNOWN")
	}
	if r.FixupNotApplicable {
		t.Errorf("FixupNotApplicable=true, want false for a polish-bar entry")
	}
	if got := len(r.Fixups); got < 2 {
		t.Errorf("len(Fixups)=%d, want >= 2 (E7-S13 polish bar)", got)
	}
}

// TestErrorsLookup_OverlayTargetUnknown_MessageMentionsComposeAndChain
// asserts the Message body covers both host families.
func TestErrorsLookup_OverlayTargetUnknown_MessageMentionsComposeAndChain(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_TARGET_UNKNOWN))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_TARGET_UNKNOWN")
	}
	if !strings.Contains(r.Message, "ComposedRequest") {
		t.Errorf("Message does not mention ComposedRequest: %q", r.Message)
	}
	if !strings.Contains(r.Message, "ChainOverlaySpec") {
		t.Errorf("Message does not mention ChainOverlaySpec: %q", r.Message)
	}
}

// TestErrorsLookup_OverlayTargetUnknown_FixupsCoverComposeAndChainSlots
// asserts the fixup catalogue covers BOTH the Compose Targets slot
// (Overlays[*].Targets) AND the chain Target StageRef slot
// (Overlays[*].Target).
func TestErrorsLookup_OverlayTargetUnknown_FixupsCoverComposeAndChainSlots(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_TARGET_UNKNOWN))
	if len(r.Fixups) < 2 {
		t.Skip("not enough fixups to test slot coverage")
	}
	sawCompose, sawChain := false, false
	for _, f := range r.Fixups {
		path := strings.Join(f.Path, "/")
		if path == "Overlays/*/Targets" {
			sawCompose = true
		}
		if strings.HasPrefix(path, "Overlays/*/Target/") {
			sawChain = true
		}
	}
	if !sawCompose {
		t.Errorf("no fixup targets the Compose Overlays[*].Targets slot")
	}
	if !sawChain {
		t.Errorf("no fixup targets the chain Overlays[*].Target/{Index,Name} slot")
	}
}

// TestErrorsLookup_OverlayTargetUnknown_FixupHintMentionsRequestsLabel
// asserts the Compose-arm fixup hint mentions the canonical authoring
// slot the story brief calls out.
func TestErrorsLookup_OverlayTargetUnknown_FixupHintMentionsRequestsLabel(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_TARGET_UNKNOWN))
	for _, f := range r.Fixups {
		if strings.Join(f.Path, "/") != "Overlays/*/Targets" {
			continue
		}
		if strings.Contains(f.Hint, "ComposedRequest.Requests[].Label") {
			return
		}
	}
	t.Errorf("Compose-arm fixup hint does not mention ComposedRequest.Requests[].Label")
}

// TestErrorsLookup_OverlayPanelTargetsOverCap_FixupCount asserts the
// PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP entry surfaces at least two
// fixup templates — one that drops targets to fit the active cap, one
// that raises the cap via Options. The E7-S13 polish bar lifts this
// from a minimal entry to the canonical multi-template form.
func TestErrorsLookup_OverlayPanelTargetsOverCap_FixupCount(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP")
	}
	if r.FixupNotApplicable {
		t.Errorf("FixupNotApplicable=true, want false for a polish-bar entry")
	}
	if got := len(r.Fixups); got < 2 {
		t.Errorf("len(Fixups)=%d, want >= 2 (E7-S13 polish bar)", got)
	}
}

// TestErrorsLookup_OverlayPanelTargetsOverCap_MessageMentionsMaxPanelTargets
// asserts the Message body names the canonical knob
// (`OverlayOptions.MaxPanelTargets`) and the combinatorial-explosion
// motivation so a reader sees both the surface and the why.
func TestErrorsLookup_OverlayPanelTargetsOverCap_MessageMentionsMaxPanelTargets(t *testing.T) {
	r, ok := perr.Lookup(string(perr.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP))
	if !ok {
		t.Fatal("Lookup returned ok=false for PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP")
	}
	if !strings.Contains(r.Message, "MaxPanelTargets") {
		t.Errorf("Message does not mention MaxPanelTargets: %q", r.Message)
	}
	if !strings.Contains(strings.ToLower(r.Message), "combinatorial") {
		t.Errorf("Message does not mention combinatorial explosion motivation: %q", r.Message)
	}
}

// TestErrorsLookup_OverlayPanelTargetsOverCap_FixupsCoverBothSurfaces
// asserts the catalogue offers BOTH a Targets-side fixup (drop targets
// to fit the cap) AND an Options-side fixup (raise MaxPanelTargets).
// The story brief required both authoring paths.
func TestErrorsLookup_OverlayPanelTargetsOverCap_FixupsCoverBothSurfaces(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP))
	if len(r.Fixups) < 2 {
		t.Skip("not enough fixups to test slot coverage")
	}
	sawTargets, sawOptions := false, false
	for _, f := range r.Fixups {
		path := strings.Join(f.Path, "/")
		if path == "Overlays/*/Targets" {
			sawTargets = true
		}
		if strings.HasPrefix(path, "Overlays/*/Options/") {
			sawOptions = true
		}
	}
	if !sawTargets {
		t.Errorf("no fixup targets Overlays[*].Targets (drop-to-fit path)")
	}
	if !sawOptions {
		t.Errorf("no fixup targets Overlays[*].Options/MaxPanelTargets (raise-cap path)")
	}
}

// TestErrorsLookup_OverlayPanelTargetsOverCap_TargetsFixupMentionsCap
// asserts the Targets-side fixup hint references the cap value (literal
// number or the OverlayOptions.MaxPanelTargets knob) so the caller sees
// the bound they're up against.
func TestErrorsLookup_OverlayPanelTargetsOverCap_TargetsFixupMentionsCap(t *testing.T) {
	r, _ := perr.Lookup(string(perr.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP))
	for _, f := range r.Fixups {
		if strings.Join(f.Path, "/") != "Overlays/*/Targets" {
			continue
		}
		if strings.Contains(f.Hint, "MaxPanelTargets") {
			return
		}
	}
	t.Errorf("Targets-side fixup hint does not name OverlayOptions.MaxPanelTargets")
}
