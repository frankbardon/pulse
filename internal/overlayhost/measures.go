package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// MeasureHostRefusal refuses, on a multi-measure crosstab
// (types.CrosstabSpec.Cells, U47 #326), every overlay kind outside the
// pairwise family. The pairwise kinds run per measure against each
// measure's own single-measure host and fold into one layer, so a pair
// never crosses measures; every other kind reads the host matrix as one
// table — its margins, its grand total, its rows as one population —
// and on a MEASURE-folded payload that table mixes measures, so it is
// refused up front rather than computed over rows that are not one
// population. Predict (descriptor.ValidateOverlays) and runtime
// (processing.refuseWrongHostOverlays) both raise it ahead of any
// handler. route is the kind the rule keys on (an extension routed onto
// a pairwise kind runs; a hidden kind, route "", is left to its slot's
// unknown-kind branch); messages name the authored kind. nil when the
// kind runs.
func MeasureHostRefusal(kind, route types.OverlayKind, index int) *errors.CodedError {
	if route == "" || types.IsPairwiseOverlayKind(route) {
		return nil
	}
	details := map[string]any{"kind": string(kind), "reason": "multi_measure_host"}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" does not run on a multi-measure crosstab (crosstab.cells): its host table would mix measures. "+
			"Only the OVERLAY_PAIRWISE_* kinds run there, once per measure; run "+string(kind)+" on a single-measure crosstab (crosstab.cell)",
		details)
}
