package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The Ref / Scope rule of every SERIES-host kind on Request.Overlays —
// the twin of the crosstab table in spec_refusal.go — shared by predict
// (internal/descriptor: each SERIES validator calls it first) and every
// runtime arm that folds a SERIES spec: the grouped Process hook
// (processing.refuseWrongHostOverlays, ahead of the buffered, streaming
// and parallel tails and of every payload guard, so an empty result
// refuses too — a Compose slot's own Request.Overlays and a ProcessChain
// stage 0 run that hook), the chain pre-pass
// (descriptor.ChainStageOverlayRefusal, every stage, before any runs)
// and Pulse.ApplySeriesOverlays. Before it the runtime re-checked
// neither: a spec predict refused (an unsupported scope, a foreign Ref
// arm, a missing Ref.RollingMean / YoY / BaselineIndex / Sibling, a
// non-zero Ref.Prior.Lag) ran silently.

// seriesArm is the Ref arm a SERIES kind reads.
type seriesArm int

const (
	// seriesImplicit: every Ref arm must be unset (the host series'
	// own grand total is the reference).
	seriesImplicit seriesArm = iota
	// seriesPrior: Ref.Prior or an empty Ref; Ref.Prior.Lag must be 0.
	seriesPrior
	// seriesRollingMean / seriesYoY / seriesBaseline / seriesSibling:
	// that arm alone, and it must be set.
	seriesRollingMean
	seriesYoY
	seriesBaseline
	seriesSibling
)

// seriesRule is one SERIES kind's Ref / Scope rule.
type seriesRule struct {
	arm seriesArm
	// why completes the implicit message's parenthesis, or the
	// missing-arm message's.
	why string
	// dispatch marks a kind that also runs on a crosstab
	// (OVERLAY_SHARE_OF_TOTAL): its messages name the SERIES dispatch
	// and its details carry host "series".
	dispatch bool
}

const (
	totalWhy   = "implicit-grand-total: the host series' own grand total is the denominator"
	rollingWhy = "windowed rolling-mean reference; window width lives on Params[\"window\"]"
)

// seriesRules is the per-kind rule table: every kind that runs on a
// SERIES Process host has a row (TestSeriesSpecRefusal_CoversSeriesKinds).
var seriesRules = map[types.OverlayKind]seriesRule{
	types.OverlayKindIndexVsTotal:  {seriesImplicit, totalWhy, false},
	types.OverlayKindShareOfTotal:  {seriesImplicit, totalWhy, true},
	types.OverlayKindZScoreVsTotal: {seriesImplicit, "implicit-grand-total: the host series' own grand-total mean + SD are the centerpoint", false},

	types.OverlayKindIndexVsPrior: {seriesPrior, "", false},
	types.OverlayKindDeltaVsPrior: {seriesPrior, "", false},

	types.OverlayKindIndexVsRollingMean: {seriesRollingMean, rollingWhy, false},
	types.OverlayKindZScoreVsRolling:    {seriesRollingMean, "windowed rolling-window reference; window width lives on Params[\"window\"]", false},

	types.OverlayKindYoY: {seriesYoY, "windowed year-over-year reference; frequency lives on Params[\"frequency\"] or the GROUP_DATE grouper's Params[\"frequency\"]", false},

	types.OverlayKindIndexVsBaseline: {seriesBaseline, "windowed positional baseline reference", false},
	types.OverlayKindDeltaVsBaseline: {seriesBaseline, "windowed positional baseline reference", false},

	types.OverlayKindIndexVsSibling: {seriesSibling, "sibling-group reference", false},
	types.OverlayKindDeltaVsSibling: {seriesSibling, "sibling-group reference", false},
}

// SeriesSpecKinds returns every kind SeriesSpecRefusal judges, in no
// particular order (tests pin it against the SERIES shape table).
func SeriesSpecKinds() []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(seriesRules))
	for k := range seriesRules {
		out = append(out, k)
	}
	return out
}

// SeriesSpecRefusal is the Ref / Scope refusal of the SERIES-host spec at
// index of Request.Overlays (index < 0 leaves it out of details). route
// is the kind every decision keys on (the empty route of a hidden kind
// has no rule); spec.Kind is what message and details name. nil when the
// spec passes or route has no rule. Judged in order, every refusal
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {index, kind} unless noted:
//
//   - a Ref arm the kind does not read is set (every arm is judged);
//   - the kind's own arm is required and unset (rolling-mean, YoY,
//     baseline, sibling kinds);
//   - Ref.Prior.Lag != 0 ({…, lag}); an empty Ref.Sibling.Field, then
//     Ref.Sibling.Value ({…, field});
//   - a scope other than group → PULSE_OVERLAY_SCOPE_UNSUPPORTED
//     {index, kind, scope}.
//
// OVERLAY_SHARE_OF_TOTAL's row is its SERIES dispatch only (its
// details add host "series"); call this on a SERIES host only.
func SeriesSpecRefusal(spec *types.OverlaySpec, route types.OverlayKind, index int) *errors.CodedError {
	rule, ok := seriesRules[route]
	if !ok || spec == nil {
		return nil
	}
	kind := string(spec.Kind)
	details := func(extra ...any) map[string]any {
		d := map[string]any{"kind": kind}
		if index >= 0 {
			d["index"] = index
		}
		if rule.dispatch {
			d["host"] = "series"
		}
		for i := 0; i+1 < len(extra); i += 2 {
			d[extra[i].(string)] = extra[i+1]
		}
		return d
	}
	ref := func(msg string, extra ...any) *errors.CodedError {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, "overlay "+kind+" "+msg, details(extra...))
	}
	// other is the Ref with the kind's own arm cleared: anything left
	// set is a foreign arm.
	other := spec.Ref
	var arm string
	var set bool
	switch rule.arm {
	case seriesPrior:
		arm, set = "Prior", other.Prior != nil
		other.Prior = nil
	case seriesRollingMean:
		arm, set = "RollingMean", other.RollingMean != nil
		other.RollingMean = nil
	case seriesYoY:
		arm, set = "YoY", other.YoY != nil
		other.YoY = nil
	case seriesBaseline:
		arm, set = "BaselineIndex", other.BaselineIndex != nil
		other.BaselineIndex = nil
	case seriesSibling:
		arm, set = "Sibling", other.Sibling != nil
		other.Sibling = nil
	}
	if other != (types.OverlayRef{}) {
		switch rule.arm {
		case seriesImplicit:
			if rule.dispatch {
				return ref("against a SERIES host must leave Ref empty (" + rule.why + ")")
			}
			return ref("must leave Ref empty (" + rule.why + ")")
		case seriesPrior:
			return ref("requires Ref.Prior or an empty Ref (windowed lag-1 prior; no other Ref arm)")
		}
		return ref("requires Ref." + arm + " only (no other Ref arm)")
	}
	switch rule.arm {
	case seriesPrior:
		if set && spec.Ref.Prior.Lag != 0 {
			return ref("Ref.Prior.Lag must be zero (v1 ships lag-1 only; the slot is reserved for future window-N priors)", "lag", spec.Ref.Prior.Lag)
		}
	case seriesRollingMean, seriesYoY, seriesBaseline, seriesSibling:
		if !set {
			return ref("requires Ref." + arm + " (" + rule.why + ")")
		}
	}
	if rule.arm == seriesSibling {
		if spec.Ref.Sibling.Field == "" {
			return ref("Ref.Sibling.Field is empty (sibling reference requires a grouper Field name)")
		}
		if spec.Ref.Sibling.Value == "" {
			return ref("Ref.Sibling.Value is empty (sibling reference requires a specific axis-key value)", "field", spec.Ref.Sibling.Field)
		}
	}
	if spec.Scope == types.OverlayScopeGroup {
		return nil
	}
	prefix := "overlay " + kind
	if rule.dispatch {
		prefix += " SERIES dispatch"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		prefix+" does not support scope "+string(spec.Scope)+" (supports: group)",
		details("scope", string(spec.Scope)))
}

// ProcessSpecRefusal is the Ref / Scope refusal of the Request.Overlays
// spec at index judged on a Process host of shape: the crosstab table on
// a MATRIX host, the SERIES table on a SERIES host, the SCALAR host rule
// (ScalarSpecRefusal: nothing runs there; grouped is
// len(Request.Groups) > 0) on a SCALAR host, nil on a MATRIX_RESULT
// host (it owns its own rule, MatrixResultRefusal). The chain pre-pass
// calls it per stage.
func ProcessSpecRefusal(shape types.OverlayShape, spec *types.OverlaySpec, route types.OverlayKind, grouped bool, index int) *errors.CodedError {
	switch shape {
	case types.OverlayShapeMatrix:
		return CrosstabSpecRefusal(spec, route, index)
	case types.OverlayShapeSeries:
		return SeriesSpecRefusal(spec, route, index)
	case types.OverlayShapeScalar:
		return ScalarSpecRefusal(spec, route, grouped, index)
	}
	return nil
}
