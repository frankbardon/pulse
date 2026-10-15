package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The host rule of every Process kind on a SCALAR Request.Overlays host —
// a request with no crosstab, no matrices and no grouped aggregations
// (an ungrouped request, or one with groups but no aggregations) — the
// twin of the crosstab (spec_refusal.go) and SERIES
// (series_spec_refusal.go) tables. Nothing runs on that host: before this
// rule predict's per-kind validators refused the spec ("requires a
// MATRIX host", "requires a SERIES host") while the runtime ran no
// overlay and raised nothing (U46 E4-S15 called the host "not judged";
// the owner superseded that in U30 E7-S5). Predict
// (descriptor.ValidateOverlays), the runtime Process hooks
// (processing.refuseWrongHostOverlays — buffered, streaming routed
// buffered by canStreamOverlays, the shard-parallel tail, a Compose
// slot) and the chain pre-pass (descriptor.ChainStageOverlayRefusal via
// ProcessSpecRefusal) all raise ScalarSpecRefusal, so code, message and
// details match on every arm.

// scalarRule is one Process kind's host on a SCALAR host: the host it
// needs and the Ref / Scope table predict judges first.
type scalarRule struct {
	// host is the Process host shape the kind needs. OVERLAY_SHARE_OF_TOTAL
	// needs either; its dispatch follows the request (grouped: SERIES).
	host types.OverlayShape
	// dual marks OVERLAY_SHARE_OF_TOTAL (MATRIX and SERIES dispatch).
	dual bool
}

var (
	needsMatrix = scalarRule{host: types.OverlayShapeMatrix}
	needsSeries = scalarRule{host: types.OverlayShapeSeries}
)

// scalarRules has a row for every kind that runs on a MATRIX or SERIES
// Process host (TestScalarSpecRefusal_CoversProcessKinds); the
// MATRIX_RESULT kind is refused earlier by ShapeRefusal.
var scalarRules = map[types.OverlayKind]scalarRule{
	types.OverlayKindChiSqCol:                  needsMatrix,
	types.OverlayKindChiSqMatrix:               needsMatrix,
	types.OverlayKindChiSqRow:                  needsMatrix,
	types.OverlayKindCorrespondence:            needsMatrix,
	types.OverlayKindMarkov:                    needsMatrix,
	types.OverlayKindRake:                      needsMatrix,
	types.OverlayKindDeltaVsMargin:             needsMatrix,
	types.OverlayKindFisherExactCell:           needsMatrix,
	types.OverlayKindFormula:                   {host: types.OverlayShapeMatrix, dual: true},
	types.OverlayKindIndexVsMargin:             needsMatrix,
	types.OverlayKindPairwiseProbitT:           needsMatrix,
	types.OverlayKindPairwisePropZ:             needsMatrix,
	types.OverlayKindPairwiseTwoMeansZ:         needsMatrix,
	types.OverlayKindPairwiseWeightedTwoMeansZ: needsMatrix,
	types.OverlayKindPairwiseWelchT:            needsMatrix,
	types.OverlayKindShareOfCol:                needsMatrix,
	types.OverlayKindShareOfRow:                needsMatrix,
	types.OverlayKindStdResidual:               needsMatrix,
	types.OverlayKindSymmetry:                  needsMatrix,
	types.OverlayKindRetention:                 needsMatrix,
	types.OverlayKindZScoreVsMargin:            needsMatrix,
	types.OverlayKindDeltaVsBaseline:           needsSeries,
	types.OverlayKindDeltaVsPrior:              needsSeries,
	types.OverlayKindDeltaVsSibling:            needsSeries,
	types.OverlayKindIndexVsBaseline:           needsSeries,
	types.OverlayKindIndexVsPrior:              needsSeries,
	types.OverlayKindIndexVsRollingMean:        needsSeries,
	types.OverlayKindIndexVsSibling:            needsSeries,
	types.OverlayKindIndexVsTotal:              needsSeries,
	types.OverlayKindYoY:                       needsSeries,
	types.OverlayKindZScoreVsRolling:           needsSeries,
	types.OverlayKindZScoreVsTotal:             needsSeries,
	types.OverlayKindShareOfTotal:              {host: types.OverlayShapeMatrix, dual: true},
}

// ScalarSpecKinds returns every kind ScalarSpecRefusal judges, in no
// particular order (tests pin it against the Process shape table).
func ScalarSpecKinds() []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(scalarRules))
	for k := range scalarRules {
		out = append(out, k)
	}
	return out
}

// scalarHost is the host route's spec needs on a SCALAR host: SERIES for
// OVERLAY_SHARE_OF_TOTAL on a request with groups (its SERIES dispatch),
// else the rule's host. ok is false for a kind without a rule.
func scalarHost(route types.OverlayKind, grouped bool) (shape types.OverlayShape, dual, ok bool) {
	rule, ok := scalarRules[route]
	if !ok {
		return "", false, false
	}
	if rule.dual && grouped {
		return types.OverlayShapeSeries, true, true
	}
	return rule.host, rule.dual, true
}

// HostRequiredRefusal is the refusal of the Request.Overlays spec at
// index whose kind's Process host is absent: PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
//
//   - a MATRIX kind: "overlay K requires a MATRIX host (Request.Crosstab);
//     none present", details {index, kind}, plus axis (Ref.Margin.Axis)
//     for an axis-margin kind;
//   - a SERIES kind: "overlay K requires a SERIES host (grouped Process
//     result: Request.Groups and Request.Aggregations non-empty,
//     Request.Crosstab nil)", details {index, kind};
//   - OVERLAY_SHARE_OF_TOTAL on a request with groups (its SERIES
//     dispatch): "overlay K SERIES dispatch requires a SERIES host (…)",
//     details {index, kind, host: "series"}.
//
// route keys the decision (nil for a kind without a rule — the empty
// route of a hidden kind included); spec.Kind is what message and
// details name. grouped is len(Request.Groups) > 0. Predict's per-kind
// validators raise it as their host guard; ScalarSpecRefusal raises it
// after the kind's Ref / Scope table.
func HostRequiredRefusal(spec *types.OverlaySpec, route types.OverlayKind, grouped bool, index int) *errors.CodedError {
	shape, dual, ok := scalarHost(route, grouped)
	if !ok || spec == nil {
		return nil
	}
	kind := string(spec.Kind)
	details := map[string]any{"index": index, "kind": kind}
	if shape == types.OverlayShapeSeries {
		msg := "overlay " + kind + " requires a SERIES host (grouped Process result: Request.Groups and Request.Aggregations non-empty, Request.Crosstab nil)"
		if dual {
			msg = "overlay " + kind + " SERIES dispatch requires a SERIES host (grouped Process result: Request.Groups and Request.Aggregations non-empty, Request.Crosstab nil)"
			details["host"] = "series"
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, msg, details)
	}
	if specRules[route].ref == refMargin && spec.Ref.Margin != nil {
		details["axis"] = string(spec.Ref.Margin.Axis)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+kind+" requires a MATRIX host (Request.Crosstab); none present", details)
}

// ScalarSpecRefusal is the refusal of the Request.Overlays spec at index
// on a SCALAR Process host — every Process kind is refused there. In
// order: the Ref / Scope table of the host the kind needs
// (CrosstabSpecRefusal for a MATRIX kind — none for OVERLAY_FORMULA —
// SeriesSpecRefusal for a SERIES kind; OVERLAY_SHARE_OF_TOTAL by its
// dispatch, SERIES when grouped), then HostRequiredRefusal. route,
// spec.Kind and grouped follow HostRequiredRefusal; nil for a kind
// without a rule.
func ScalarSpecRefusal(spec *types.OverlaySpec, route types.OverlayKind, grouped bool, index int) *errors.CodedError {
	shape, _, ok := scalarHost(route, grouped)
	if !ok || spec == nil {
		return nil
	}
	var cerr *errors.CodedError
	if shape == types.OverlayShapeSeries {
		cerr = SeriesSpecRefusal(spec, route, index)
	} else {
		cerr = CrosstabSpecRefusal(spec, route, index)
	}
	if cerr != nil {
		return cerr
	}
	return HostRequiredRefusal(spec, route, grouped, index)
}
