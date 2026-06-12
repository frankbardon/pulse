package csv

import (
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// CSV overlay warn-and-skip implementation per
// research/export-embedding-shape.md § 7.
//
// CSV is flat — there is no clean way to encode nested overlay
// payloads (the only sensible matrix-overlay-in-CSV shape would be a
// second pseudo-table appended after a blank-row separator, which
// breaks every standard CSV reader). The adapter therefore implements
// warn-and-skip:
//
//  1. SetOverlays RECORDS the layers on the writer but NEVER writes
//     them into the CSV body. The host CSV is byte-identical to a
//     pre-overlay export.
//  2. When SetOverlays handed the writer a non-empty slice the writer
//     surfaces ONE PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning via
//     OverlayWarnings(). The warning carries layer_count, layer_names,
//     and layer_kinds in its Details so callers can audit the dropped
//     layer slate without re-reading the source Response. The
//     dispatcher wiring (E9-S11) lifts this slice onto the
//     ExportReport / envelope warnings surface.
//  3. nil OR empty layers leave OverlayWarnings() empty — the warning
//     is specifically "you asked for overlays but CSV cannot carry
//     them," not "CSV cannot carry overlays in general."
//
// The TSV adapter (io/tsv/) shares this contract by mirroring the same
// SetOverlays + OverlayWarnings surface; the warning code stays
// PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED because CSV is the canonical
// name in the warning text and the fixups call out TSV alongside CSV.

// SetOverlays records the Response.Overlays layers the export pipeline
// wanted the writer to embed. CSV cannot encode overlay payloads so
// the layers are RECORDED for the warn-and-skip emission surface but
// NEVER written into the CSV body. The host CSV output is byte-
// identical to a pre-overlay export regardless of whether SetOverlays
// was called or what slice it received. Implements
// pio.OverlayAwareWriter — the dispatcher (E9-S11) lifts the
// OverlayWarnings() slice onto the ExportReport / envelope warnings
// slot.
//
// Calling SetOverlays multiple times overwrites the previously
// recorded layers; the warning slice surfaces the LAST recorded set.
func (w *Writer) SetOverlays(layers []*types.OverlayLayer) {
	w.overlays = layers
}

// OverlayWarnings returns the PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED
// warning slice for the recorded overlay layers, or nil when no
// layers were recorded. The dispatcher wiring (E9-S11) consumes this
// slice and lifts it onto the ExportReport / envelope warnings slot.
//
// Per research/export-embedding-shape.md § 7.1 the warning fires
// REGARDLESS of ExportJob.IncludeOverlays because the writer never
// sees the IncludeOverlays toggle directly — the dispatcher chooses
// whether to call SetOverlays at all based on IncludeOverlays. When
// IncludeOverlays resolves to false (explicit opt-out) the
// dispatcher skips SetOverlays entirely and OverlayWarnings() stays
// empty; when IncludeOverlays resolves to true (default or explicit)
// and the Response carries layers, SetOverlays gets the slice and
// OverlayWarnings() emits the warning.
//
// nil or empty recorded layers produce an empty warning slice — the
// warning is specifically "you asked for overlays but CSV cannot
// carry them," not "CSV cannot carry overlays in general."
func (w *Writer) OverlayWarnings() []*errors.CodedError {
	if len(w.overlays) == 0 {
		return nil
	}
	names := make([]string, 0, len(w.overlays))
	kinds := make([]string, 0, len(w.overlays))
	for _, layer := range w.overlays {
		if layer == nil {
			continue
		}
		names = append(names, layer.Name)
		kinds = append(kinds, string(layer.Kind))
	}
	details := map[string]any{
		"layer_count": len(w.overlays),
		"layer_names": names,
		"layer_kinds": kinds,
	}
	msg := formatOverlayDroppedMessage(len(w.overlays))
	return []*errors.CodedError{
		errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED,
			msg,
			details,
		),
	}
}

// formatOverlayDroppedMessage renders the canonical warning message
// without using fmt.Sprintf-built JSON (per CLAUDE.md "Structural
// defense bans"). The message text matches the metadata Message in
// errors/fixup_metadata.go (with the %d placeholder filled in).
func formatOverlayDroppedMessage(n int) string {
	// strconv on hot paths; the canonical text reads:
	//   "CSV export does not support overlay embedding; N overlay
	//    layer(s) dropped"
	var b []byte
	b = append(b, "CSV export does not support overlay embedding; "...)
	b = appendInt(b, n)
	b = append(b, " overlay layer(s) dropped"...)
	return string(b)
}

// appendInt is a minimal int-to-decimal helper. Inlined instead of
// pulling fmt or strconv into the hot path. The warning fires at most
// once per export, so allocation is not a concern; the function exists
// to keep the message-building intent explicit and avoid any
// stringification surprise.
func appendInt(dst []byte, n int) []byte {
	if n == 0 {
		return append(dst, '0')
	}
	if n < 0 {
		dst = append(dst, '-')
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return append(dst, buf[i:]...)
}

// Compile-time interface assertion — the CSV Writer satisfies the
// pio.OverlayAwareWriter contract by recording layers (and emitting
// warnings via OverlayWarnings) rather than embedding them, which is
// the SetOverlays-different-shape pattern the contract allows.
var _ pio.OverlayAwareWriter = (*Writer)(nil)
