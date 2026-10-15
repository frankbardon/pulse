package overlayhost

import (
	"math"

	"github.com/frankbardon/pulse/types"
)

// WelfordCellMessage is the PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// message of a moment pairwise kind (types.PairwiseKindUsesWelford)
// over a crosstab whose cells carry no Welford triple {mean, variance,
// n}. The runtime (processing.runPairwiseOverlay, off the materialised
// host) and predict (descriptor.validateOverlayPairwise, off the cell
// type's declared ComponentSchema) raise it with the same text.
func WelfordCellMessage(kind types.OverlayKind) string {
	return "overlay " + string(kind) + " requires AGG_WELFORD cells (Welford triple on CellComponents); host matrix has none"
}

// ComponentsRequiredMessage is the PULSE_OVERLAY_COMPONENTS_REQUIRED
// message of an OVERLAY_PAIRWISE_* kind over a crosstab built with
// components disabled (Options.DisableComponents, or the request's own
// disable_components: true — a request's false re-opens an
// Options-disabled instance). Every pairwise kind reads its sample-size
// legs off the cell components, so the runtime
// (processing.runPairwiseOverlay) refuses it and predict
// (descriptor.validateOverlayPairwise) reports it with the same text.
func ComponentsRequiredMessage(kind types.OverlayKind) string {
	return "overlay " + string(kind) + " requires Response.Components.Crosstab; the host was built with components disabled"
}

// PopulationVarianceCell reports whether a built-in crosstab cell type
// emits the POPULATION variance m2 / n under its `variance` component
// key: AGG_VARIANCE and AGG_STDDEV. AGG_WELFORD emits the sample
// variance m2 / (n - 1) the moment pairwise kinds
// (OVERLAY_PAIRWISE_WELCH_T / _TWO_MEANS_Z) are defined on, so their
// leg reader (processing.CrosstabHostView.MeanLeg) converts an
// unweighted population cell with SampleVariance; a weighted cell's
// variance is recomputed from m2 and N* either way. A component schema
// cannot declare which variance it carries, so an extension cell is
// read as is: it must expose the sample variance.
func PopulationVarianceCell(t types.AggregationType) bool {
	return t == types.AGG_VARIANCE || t == types.AGG_STDDEV
}

// SampleVariance is the sample variance of an unweighted cell of n
// non-null values whose components carry the population variance
// popVariance and, when hasM2, the second central moment m2: m2 /
// (n - 1) — bit-identical to AGG_WELFORD's own — or popVariance · n /
// (n - 1) without m2. Undefined (NaN, JSON null) when n < 2, never 0.
func SampleVariance(popVariance, m2 float64, hasM2 bool, n float64) float64 {
	if !(n >= 2) {
		return math.NaN()
	}
	if hasM2 {
		return m2 / (n - 1)
	}
	return popVariance * n / (n - 1)
}
