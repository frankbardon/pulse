package excel

import (
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/iocore"
)

// OverlayWarnings reports the named parts (OverlayPayload.Parts) the
// workbook could not carry. Each overlay layer rides one sheet holding
// its PRIMARY payload; a multi-output kind's parts have no slot in that
// layout, so they are dropped and named here as one
// PULSE_OVERLAY_EXPORT_PARTS_DROPPED warning. nil when no recorded
// layer carries parts — a parts-free export reports nothing, exactly
// as before parts existed. Implements iocore.OverlayWarningEmitter; a
// pure accessor over the recorded slate.
func (w *Writer) OverlayWarnings() []*errors.CodedError {
	var names []string
	parts := map[string]any{}
	for _, layer := range w.overlays {
		if layer == nil || len(layer.Payload.Parts) == 0 {
			continue
		}
		keys := make([]string, 0, len(layer.Payload.Parts))
		for k := range layer.Payload.Parts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		names = append(names, layer.Name)
		parts[layer.Name] = keys
	}
	if len(names) == 0 {
		return nil
	}
	return []*errors.CodedError{
		errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_EXPORT_PARTS_DROPPED,
			"Excel export embeds the primary payload only; "+strconv.Itoa(len(names))+" overlay layer(s) lost their named parts",
			map[string]any{
				"layer_count": len(names),
				"layer_names": names,
				"parts":       parts,
			}),
	}
}

var _ iocore.OverlayWarningEmitter = (*Writer)(nil)
