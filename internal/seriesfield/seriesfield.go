// Package seriesfield is the one reading of a series overlay's
// `params.field`: the OUTPUT LABEL of the request aggregation the
// overlay scores. Absent (or empty) means the request's first
// aggregation, the rule every series kind followed before the param
// existed. The runtime host builder (internal/processing) and predict
// (internal/descriptor) both call Resolve, so the refusal — code,
// message and Details — cannot drift between them.
//
// Leaf: stdlib + types + errors. No-execute.
package seriesfield

import (
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Param is the OverlaySpec.Params key naming the scored aggregation.
const Param = "field"

// kinds is the set of process-host (grouped Process) series overlay
// kinds that score one aggregation of the host and so honour Param.
var kinds = map[types.OverlayKind]bool{
	types.OverlayKindDeltaVsPrior:       true,
	types.OverlayKindIndexVsPrior:       true,
	types.OverlayKindIndexVsRollingMean: true,
	types.OverlayKindZScoreVsRolling:    true,
	types.OverlayKindDeltaVsBaseline:    true,
	types.OverlayKindIndexVsBaseline:    true,
	types.OverlayKindDeltaVsSibling:     true,
	types.OverlayKindIndexVsSibling:     true,
	types.OverlayKindYoY:                true,
	types.OverlayKindIndexVsTotal:       true,
	types.OverlayKindShareOfTotal:       true,
	types.OverlayKindZScoreVsTotal:      true,
}

// Applies reports whether kind is a series overlay kind that scores the
// aggregation Param names.
func Applies(kind types.OverlayKind) bool { return kinds[kind] }

// Kinds returns every kind Applies accepts, in types.AllOverlayKinds
// order.
func Kinds() []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(kinds))
	for _, k := range types.AllOverlayKinds() {
		if kinds[k] {
			out = append(out, k)
		}
	}
	return out
}

// Labels returns the output label of every aggregation, in request
// order: the explicit Label, else TYPE_field (types.AggregationLabelOf).
func Labels(aggs []*types.Aggregation) []string {
	out := make([]string, len(aggs))
	for i, a := range aggs {
		out[i] = types.AggregationLabelOf(a)
	}
	return out
}

// IndexOf returns the index of the first aggregation whose output label
// equals label, or -1.
func IndexOf(aggs []*types.Aggregation, label string) int {
	for i, a := range aggs {
		if a != nil && types.AggregationLabelOf(a) == label {
			return i
		}
	}
	return -1
}

// Resolve returns the index into aggs of the aggregation spec scores:
// 0 when Params carries no (or an empty) `field`, else the aggregation
// whose output label equals it. A `field` that is not a string, or names
// no aggregation, is refused PULSE_OVERLAY_FIELD_UNKNOWN with Details
// {index, kind, field, available} — index is the spec's position in
// Request.Overlays, available the request's labels in order.
//
// Params that do not decode as a JSON object carry no `field`; the
// kind's own params gate owns that refusal.
func Resolve(aggs []*types.Aggregation, spec *types.OverlaySpec, index int) (int, error) {
	if spec == nil || len(spec.Params) == 0 {
		return 0, nil
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return 0, nil
	}
	return resolve(aggs, spec.Kind, m, index)
}

// resolve is Resolve over decoded params, shared with the Compose host.
func resolve(aggs []*types.Aggregation, kind types.OverlayKind, params map[string]any, index int) (int, error) {
	raw, present := params[Param]
	if !present || raw == nil {
		return 0, nil
	}
	name, isString := raw.(string)
	if isString && name == "" {
		return 0, nil
	}
	if isString {
		if i := IndexOf(aggs, name); i >= 0 {
			return i, nil
		}
	}
	msg := "overlay " + string(kind) + " params.field must name the output label of a request aggregation"
	if isString {
		msg = "overlay " + string(kind) + " params.field \"" + name + "\" names no request aggregation"
	}
	return 0, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FIELD_UNKNOWN, msg, map[string]any{
		"index":     index,
		"kind":      string(kind),
		"field":     raw,
		"available": Labels(aggs),
	})
}

// composeKinds is the set of Compose-host kinds whose SERIES arm scores
// one aggregation of each slot: the value of a slot's Data row is the
// aggregation Param names, its key the slot's group columns only.
var composeKinds = map[types.OverlayKind]bool{
	types.OverlayKindIndexVsRef:      true,
	types.OverlayKindDeltaVsRef:      true,
	types.OverlayKindTVsRef:          true,
	types.OverlayKindZVsRef:          true,
	types.OverlayKindPanelIndexVsRef: true,
}

// AppliesCompose reports whether kind is a Compose-host kind whose
// SERIES arm scores the slot aggregation Param names.
func AppliesCompose(kind types.OverlayKind) bool { return composeKinds[kind] }

// ComposeKinds returns every kind AppliesCompose accepts, in
// types.AllOverlayKinds order.
func ComposeKinds() []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(composeKinds))
	for _, k := range types.AllOverlayKinds() {
		if composeKinds[k] {
			out = append(out, k)
		}
	}
	return out
}

// ResolveComposeSlot returns the index into aggs (one Compose slot's
// Request.Aggregations) of the aggregation a Compose overlay spec
// scores on that slot: the Resolve rule over the spec's decoded Params.
// The refusal is PULSE_OVERLAY_FIELD_UNKNOWN with Details {index, kind,
// field, available, slot} — index the spec's position in
// ComposedRequest.Overlays, slot the slot's label. The runtime
// dispatcher and predict-compose both call it, so the refusal cannot
// drift between them.
func ResolveComposeSlot(aggs []*types.Aggregation, spec *types.ComposeOverlaySpec, index int, slot string) (int, error) {
	if spec == nil {
		return 0, nil
	}
	i, err := resolve(aggs, spec.Kind, spec.Params, index)
	if ce, ok := err.(*errors.CodedError); ok {
		ce.Details["slot"] = slot
		ce.Message += " on slot \"" + slot + "\""
	}
	return i, err
}
