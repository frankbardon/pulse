package descriptor

import (
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// extensionOverlay returns the declared contract of the extension
// overlay kind the spec authors, nil for a built-in, unknown or hidden
// kind (a hidden kind's route is "", which no registration carries) —
// the descriptor twin of processing.ExtensionRegistry.extensionOverlay.
// Nil-receiver-safe.
func (o *PredictOptions) extensionOverlay(kind types.OverlayKind) *overlayhost.ExtensionDecl {
	route := o.overlayRoute(kind)
	snap := o.instance().Extensions()
	if route == "" || snap == nil {
		return nil
	}
	d, ok := snap.OverlayDetails[string(route)]
	if !ok {
		return nil
	}
	return &d
}
