package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// BaselineIndexOutOfRange is the refusal of a SERIES baseline-index kind
// (OVERLAY_INDEX_VS_BASELINE, OVERLAY_DELTA_VS_BASELINE) whose
// Ref.BaselineIndex.Position is outside a host series of seriesLength
// rows: PULSE_OVERLAY_REF_UNKNOWN {index, kind, baseline_index,
// series_length}, shared by predict (internal/descriptor, when every
// grouper bounds the series length by its dictionary; series_length then
// carries the bound) and the runtime resolver
// (processing.ResolveBaselineIndexFor, off the materialised host) so
// both raise the same code, message and details. An empty kind (the
// kind-free resolver) omits the kind from the message and details; a
// negative index omits Details.index (the runtime handler, whose
// dispatcher stamps it).
func BaselineIndexOutOfRange(kind types.OverlayKind, index, position, seriesLength int) *errors.CodedError {
	details := map[string]any{"baseline_index": position, "series_length": seriesLength}
	msg := "overlay baseline-index position is out of range for the host series"
	if kind != "" {
		details["kind"] = string(kind)
		msg = "overlay " + string(kind) + " baseline-index position is out of range for the host series"
	}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_UNKNOWN, msg, details)
}
