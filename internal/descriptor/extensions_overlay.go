package descriptor

import (
	"github.com/frankbardon/pulse/errors"
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

// extensionSlotRefusal is the wrong-slot refusal an extension overlay
// kind authored on host gets (overlayhost.ExtensionSlotRefusal), nil
// for a built-in, unknown or hidden kind or a slot the kind runs on —
// the descriptor twin of processing.ExtensionRegistry.extensionSlotRefusal,
// raised at the point each validator raises overlayhost.Refusal.
func (o *PredictOptions) extensionSlotRefusal(host overlayhost.Host, kind types.OverlayKind, index int) *errors.CodedError {
	if d := o.extensionOverlay(kind); d != nil {
		return overlayhost.ExtensionSlotRefusal(d, host, index)
	}
	return nil
}
