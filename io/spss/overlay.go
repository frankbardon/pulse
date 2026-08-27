package spss

// SPSS overlay warn-and-skip, per research/export-embedding-shape.md § 7.
//
// The `.sav` format DOES have an extension surface — record type 7
// subtypes — and it is tempting to think an overlay layer could ride one.
// It cannot, usefully. Every subtype in the format is specified, a reader
// meeting an unknown one is entitled to ignore it, and every reader does:
// haven, foreign, PSPP and SPSS itself would all open the file and show
// none of it. That is warn-and-skip with extra steps and a private
// subtype number nobody agreed to.
//
// So this adapter joins the CSV / TSV family: it RECORDS the layers, never
// writes them, and surfaces one PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED per
// export naming what was dropped. The warning code keeps its CSV spelling
// because it is the canonical name of the warn-and-skip behaviour rather
// than a claim about the CSV format; the message names SPSS.
//
// There is a second reason overlays cannot reach a `.sav` body, and it is
// structural rather than a format limitation: the writer takes the
// pio.CohortWriter path, encoding from the cohort's raw storage instead of
// from the rendered row stream. Overlay embedding in the other adapters
// happens as part of writing that stream.

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// SetOverlays records the layers the export pipeline wanted embedded.
// They are never written into the `.sav`; the emitted file is
// byte-identical to an overlay-free export. pio.OverlayAwareWriter.
//
// Calling it twice overwrites the recorded set, so the warning describes
// the last slate handed over.
func (w *Writer) SetOverlays(layers []*types.OverlayLayer) { w.overlays = layers }

// OverlayWarnings returns the one warn-and-skip diagnostic for the
// recorded layers, or nil when none were recorded.
//
// nil / empty layers produce no warning: the diagnostic is "you asked for
// overlays and a .sav cannot carry them", not "a .sav cannot carry
// overlays in general". ExportJob.Run skips SetOverlays entirely when
// IncludeOverlays explicitly opts out, so the opt-out is silent by
// construction.
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
	return []*errors.CodedError{errors.NewCodedErrorWithDetails(
		errors.PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED,
		"SPSS .sav export does not support overlay embedding; "+
			strconv.Itoa(len(w.overlays))+" overlay layer(s) dropped",
		map[string]any{
			"layer_count": len(w.overlays),
			"layer_names": names,
			"layer_kinds": kinds,
		})}
}

var (
	_ pio.OverlayAwareWriter    = (*Writer)(nil)
	_ pio.OverlayWarningEmitter = (*Writer)(nil)
)
