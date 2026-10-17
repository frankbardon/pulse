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

// normalised reports whether a crosstab normalize mode divides every
// cell by a denominator, so the cell value is a 0..1 share.
func normalised(mode types.CrosstabNormalize) bool {
	return mode != "" && mode != types.CrosstabNormalizeNone
}

// EffectivePairwisePSource is the proportion-leg reading a pairwise
// proportion kind applies: the explicit p_source when set, else — the
// omitted default — cell_value on a normalised host (every cell is
// already a 0..1 share; dividing it by 100 would score the pair
// silently wrong, survey-tables E1-S3) and cell_value_pct otherwise.
// mode is the host crosstab's NormalizeOrDefault. The runtime
// (processing.runPairwiseOverlay) reads through it; predict's refusal
// below is its other half.
func EffectivePairwisePSource(pSource string, mode types.CrosstabNormalize) string {
	switch {
	case pSource != "":
		return pSource
	case normalised(mode):
		return types.PairwisePSourceCellValue
	}
	return types.PairwisePSourceCellValuePct
}

// PairwisePSourceRefusal is the PULSE_OVERLAY_PARAM_MISSING message of
// an EXPLICIT p_source cell_value_pct on a normalised host ("" when the
// pair is fine): the selector divides a 0..1 share by 100. The runtime
// (processing.runPairwiseOverlay) and predict
// (descriptor.validateOverlayPairwise) raise it with this text and
// details {kind, param, p_source, normalize}.
func PairwisePSourceRefusal(kind types.OverlayKind, pSource string, mode types.CrosstabNormalize) string {
	if pSource != types.PairwisePSourceCellValuePct || !normalised(mode) {
		return ""
	}
	return "overlay " + string(kind) + " p_source " + pSource + " reads each cell as a 0..100 percentage, but the host crosstab is normalized (normalize " +
		string(mode) + ") so every cell is already a 0..1 share; omit p_source or set \"" + types.PairwisePSourceCellValue + "\""
}

// PairwiseCountCell reports whether a BUILT-IN crosstab cell type's
// value is a count — of records, of a label, of distinct values, or the
// sum of a field (a sum of a 0/1 indicator is its count) — which the
// omitted p_source of a proportion pairwise kind keeps reading as
// cell_value_pct on an unnormalised host. Every other built-in is a
// statistic of its field (a mean, a quantile, a spread, a ratio), not a
// count or a percentage.
func PairwiseCountCell(t types.AggregationType) bool {
	switch t {
	case types.AGG_COUNT, types.AGG_SUM, types.AGG_NULL_COUNT, types.AGG_DISTINCT_COUNT, types.AGG_DISTINCT_SUM,
		types.AGG_MODE_COUNT, types.AGG_FREQUENCY, types.AGG_SET_CARDINALITY_SUM:
		return true
	}
	return false
}

// PairwiseMeanCellRefusal is the PULSE_OVERLAY_PARAM_MISSING message of
// a proportion pairwise kind whose p_source is OMITTED on an
// UNNORMALISED host whose cell is a built-in statistic of its field
// (not PairwiseCountCell) — "" when the pair is fine (survey-tables
// E4-S6). The omitted default there reads each cell as a 0..100
// percentage (÷100), so a mean cell — AGG_AVERAGE of a 0/1 indicator,
// 0.86 read as 0.0086 — scored every pair silently wrong. An explicit
// p_source states the reading and is never refused here; an empty cell
// type (a caller-built host) or an extension type is not judged. The
// runtime (processing.pairwiseGate, per measure on a multi-measure host)
// and predict (descriptor.validateOverlayPairwise) raise it with this
// text and details {kind, param, cell_type}.
func PairwiseMeanCellRefusal(kind types.OverlayKind, pSource string, mode types.CrosstabNormalize, cellType types.AggregationType) string {
	if pSource != "" || normalised(mode) || cellType == "" || !PriorMeasureBuiltin(cellType) || PairwiseCountCell(cellType) {
		return ""
	}
	return "overlay " + string(kind) + " with p_source omitted reads each cell as a 0..100 percentage (" + types.PairwisePSourceCellValuePct +
		"), but the host crosstab is not normalized and its cell is " + string(cellType) +
		", a statistic of its field rather than a count or a percentage; set p_source \"" + types.PairwisePSourceCellValue +
		"\" when the cell is a 0..1 proportion (an AGG_AVERAGE of a 0/1 indicator) or \"" + types.PairwisePSourceCellValuePct +
		"\" when it is a 0..100 percentage"
}
