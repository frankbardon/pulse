package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// TestComposeOverlayCost_FanOutScalesPerTarget: a fan-out spec emits
// one layer per target, so its cost scales by min(targets, cap) like
// the panel kinds'; one target keeps the kind's own cost.
func TestComposeOverlayCost_FanOutScalesPerTarget(t *testing.T) {
	t.Parallel()
	for _, kind := range overlayhost.ComposeFanOutKinds() {
		base := overlayCostForKind(kind)
		one := &types.ComposeOverlaySpec{Kind: kind, Targets: []string{"b"}}
		if got := composeOverlayCostForSpec(one); got != base {
			t.Errorf("%s one target cost %v, want %v", kind, got, base)
		}
		three := &types.ComposeOverlaySpec{Kind: kind, Targets: []string{"b", "c", "d"}}
		if got := composeOverlayCostForSpec(three); got != 3*base {
			t.Errorf("%s three targets cost %v, want %v", kind, got, 3*base)
		}
		capped := &types.ComposeOverlaySpec{Kind: kind, Targets: []string{"b", "c", "d"}, Options: &types.OverlayOptions{MaxPanelTargets: 2}}
		if got := composeOverlayCostForSpec(capped); got != 2*base {
			t.Errorf("%s capped cost %v, want %v", kind, got, 2*base)
		}
	}
	if composeDefaultPanelCap != overlayhost.DefaultComposeTargetCap {
		t.Errorf("composeDefaultPanelCap %d != overlayhost.DefaultComposeTargetCap %d", composeDefaultPanelCap, overlayhost.DefaultComposeTargetCap)
	}
}

// TestComposeFanOutKinds_CapabilityNotesFanOut: every fan-out kind's
// manifest description says it fans out, and no other kind's does.
func TestComposeFanOutKinds_CapabilityNotesFanOut(t *testing.T) {
	t.Parallel()
	for _, kind := range types.AllOverlayKinds() {
		has := strings.Contains(overlayCapabilityFor(kind).Description, composeFanOutNote)
		if has != overlayhost.ComposeFanOutKind(kind) {
			t.Errorf("%s: description carries the fan-out note %v, fan-out kind %v", kind, has, overlayhost.ComposeFanOutKind(kind))
		}
	}
}
