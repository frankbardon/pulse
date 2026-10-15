package overlayhost

import "github.com/frankbardon/pulse/types"

// WelfordCellMessage is the PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// message of a moment pairwise kind (types.PairwiseKindUsesWelford)
// over a crosstab whose cells carry no Welford triple {mean, variance,
// n}. The runtime (processing.runPairwiseOverlay, off the materialised
// host) and predict (descriptor.validateOverlayPairwise, off the cell
// type's declared ComponentSchema) raise it with the same text.
func WelfordCellMessage(kind types.OverlayKind) string {
	return "overlay " + string(kind) + " requires AGG_WELFORD cells (Welford triple on CellComponents); host matrix has none"
}
