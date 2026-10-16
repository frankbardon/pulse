package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The inferential prior-period kinds (U47 #328): OVERLAY_T_VS_PRIOR,
// OVERLAY_Z_VS_PRIOR and OVERLAY_PROP_Z_VS_PRIOR test each period
// against the one before it. They run on two Request.Overlays hosts —
// a grouped Process (SERIES) and a crosstab with NO column grouper,
// whose rows are the periods of the one implicit Total column — and
// read their legs off the scored measure's components. The rules below
// are the one reading predict (internal/descriptor) and the runtime
// (internal/processing) share, so both raise the same code, message
// and details.

// PriorTestKind reports whether route is one of the three inferential
// prior-period kinds.
func PriorTestKind(route types.OverlayKind) bool {
	switch route {
	case types.OverlayKindTVsPrior, types.OverlayKindZVsPrior, types.OverlayKindPropZVsPrior:
		return true
	}
	return false
}

// PriorTestUsesWelford reports whether route reads the Welford triple
// {mean, variance, n} (the T and Z kinds); the proportion kind reads
// the measure's value and its floor's n.
func PriorTestUsesWelford(route types.OverlayKind) bool {
	return route == types.OverlayKindTVsPrior || route == types.OverlayKindZVsPrior
}

// PriorWelfordMeasure reports whether a BUILT-IN aggregation type's
// components carry the Welford triple a T / Z prior-period leg reads:
// AGG_WELFORD (the sample variance), AGG_VARIANCE and AGG_STDDEV (the
// population variance, converted with SampleVariance). It is the set of
// built-ins whose declared ComponentSchema carries both `mean` and
// `variance` (pinned by a descriptor test); an extension type is judged
// by its declared schema (predict) and its emitted keys (runtime).
func PriorWelfordMeasure(t types.AggregationType) bool {
	switch t {
	case types.AGG_WELFORD, types.AGG_VARIANCE, types.AGG_STDDEV:
		return true
	}
	return false
}

// PriorProportionMeasure reports whether a BUILT-IN aggregation type's
// value is a proportion a prior-period proportion leg reads: AGG_AVERAGE
// (of a 0/1 indicator — p̂ is the mean, the weighted mean when
// weighted). An extension type is admitted and judged per period at
// runtime (a value outside [0, 1] skips as
// PULSE_STATS_SKIP_PROPORTION_INVALID).
func PriorProportionMeasure(t types.AggregationType) bool {
	return t == types.AGG_AVERAGE
}

// PriorColumnsRefusal refuses a prior-period kind over a crosstab that
// HAS a column grouper: the kind's periods are the row keys of the one
// implicit Total column (crosstab.columns omitted), so a real column
// axis has no single series to walk. PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
// details {index, kind, reason: "column_grouper"}. nil for every other
// kind. route keys the decision; kind is what message and details name.
func PriorColumnsRefusal(kind, route types.OverlayKind, index int) *errors.CodedError {
	if !PriorTestKind(route) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" runs on a crosstab with no column grouper (its periods are the rows of the implicit \"Total\" column); omit crosstab.columns or use a grouped Process",
		priorDetails(kind, index, "reason", "column_grouper"))
}

// PriorWelfordRefusal refuses a T / Z prior-period kind whose scored
// measure carries no Welford triple: PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
// details {index, kind, measure_type}.
func PriorWelfordRefusal(kind types.OverlayKind, measure types.AggregationType, index int) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" requires a measure whose components carry the Welford triple {mean, variance, n} (AGG_WELFORD, AGG_VARIANCE or AGG_STDDEV); the scored measure is "+measureName(measure),
		priorDetails(kind, index, "measure_type", string(measure)))
}

// PriorProportionRefusal refuses the proportion prior-period kind whose
// scored measure is a built-in other than AGG_AVERAGE:
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, details
// {index, kind, measure_type}.
func PriorProportionRefusal(kind types.OverlayKind, measure types.AggregationType, index int) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" requires an AGG_AVERAGE measure of a 0/1 indicator (its value is the proportion, its components the n); the scored measure is "+measureName(measure),
		priorDetails(kind, index, "measure_type", string(measure)))
}

// PriorNormalizedRefusal refuses the proportion prior-period kind over a
// NORMALISED crosstab: every cell is then a share of a base the periods
// share, so the two legs are not independent samples.
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, details
// {index, kind, normalize}. nil for any other kind or an unnormalised
// host (mode "" or none).
func PriorNormalizedRefusal(kind, route types.OverlayKind, mode types.CrosstabNormalize, index int) *errors.CodedError {
	if route != types.OverlayKindPropZVsPrior || !normalised(mode) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" reads each period's cell as a proportion of its own respondents; the host crosstab is normalized (normalize "+string(mode)+"), so every period shares one base — set normalize \"none\" with an AGG_AVERAGE cell",
		priorDetails(kind, index, "normalize", string(mode)))
}

// PriorComponentsRequiredMessage is the PULSE_OVERLAY_COMPONENTS_REQUIRED
// message of a prior-period kind over a host built with components
// disabled: every leg's n (and the T / Z moments) is read off them.
func PriorComponentsRequiredMessage(kind types.OverlayKind) string {
	return "overlay " + string(kind) + " requires Response.Components (each period's n and moments are read off them); the host was built with components disabled"
}

// PriorLevelWithinMessage is the PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
// message of a prior-period kind whose Level or Within is non-zero.
func PriorLevelWithinMessage(kind types.OverlayKind) string {
	return "overlay " + string(kind) + " does not support Level / Within (the prior-period test walks the ordered period axis without a prefix-bucket denominator)"
}

func measureName(t types.AggregationType) string {
	if t == "" {
		return "untyped"
	}
	return string(t)
}

func priorDetails(kind types.OverlayKind, index int, k, v string) map[string]any {
	d := map[string]any{"kind": string(kind), k: v}
	if index >= 0 {
		d["index"] = index
	}
	return d
}

// PriorMeasureBuiltin reports whether t is a built-in aggregation type
// (types.AllAggregationTypes): the prior-period measure rule judges a
// built-in by its type, an extension by its declared schema (predict)
// or its emitted keys (runtime).
func PriorMeasureBuiltin(t types.AggregationType) bool {
	for _, b := range types.AllAggregationTypes() {
		if b == t {
			return true
		}
	}
	return false
}

// PriorMeasureRefusal is the type half of the prior-period measure rule,
// raised identically by predict and the runtime: a T / Z kind whose
// scored measure is a built-in outside PriorWelfordMeasure, or the
// proportion kind whose measure is a built-in other than AGG_AVERAGE,
// is refused (PriorWelfordRefusal / PriorProportionRefusal). An empty
// or extension type is not judged here. nil for every other kind.
func PriorMeasureRefusal(kind, route types.OverlayKind, measure types.AggregationType, index int) *errors.CodedError {
	if !PriorTestKind(route) || measure == "" || !PriorMeasureBuiltin(measure) {
		return nil
	}
	if PriorTestUsesWelford(route) {
		if !PriorWelfordMeasure(measure) {
			return PriorWelfordRefusal(kind, measure, index)
		}
		return nil
	}
	if !PriorProportionMeasure(measure) {
		return PriorProportionRefusal(kind, measure, index)
	}
	return nil
}
