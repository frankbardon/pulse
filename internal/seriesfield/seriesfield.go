// Package seriesfield is the one reading of a series overlay's
// `params.field`: the OUTPUT LABEL of the request aggregation the
// overlay scores, or one expanded per-element column
// "<label>[<element label>]" of an array-valued aggregation under
// params.expand. Absent (or empty) means the request's first
// aggregation, the rule every series kind followed before the param
// existed. The runtime host builder (internal/processing) and predict
// (internal/descriptor) both call Resolve, so the refusal — code,
// message and Details — cannot drift between them.
//
// Leaf: stdlib + types + errors. No-execute.
package seriesfield

import (
	"encoding/json"
	"slices"
	"strings"

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
	types.OverlayKindFormula:            true,
}

// Applies reports whether kind is a series overlay kind that scores the
// aggregation Param names.
func Applies(kind types.OverlayKind) bool { return kinds[kind] }

// Kinds returns every kind Applies accepts, in types.AllOverlayKinds
// order.
func Kinds() []types.OverlayKind { return kindsIn(kinds) }

// Columns reports the Response.Data columns aggregation i of the
// resolved slice writes: its label, or — on an expanding array-valued
// aggregation (params.expand) — one "<label>[<element label>]" column
// per member, the label itself absent. known is false when the
// aggregation expands but its members cannot be read here (no schema):
// any "<label>[...]" name then resolves to it, and the gate that holds
// the schema judges the element. A nil Columns resolves output labels
// only. vectors.AggregationColumns builds the one every host passes.
type Columns func(i int) (cols []string, known bool)

// Scored is what a series overlay scores: Index into the aggregations,
// and Column, the Response.Data column its host value is read from —
// the aggregation's output label, or the expanded element column
// params.field names. Column is "" only when there is no aggregation.
type Scored struct {
	Index  int
	Column string
}

// Element reports whether s scores an expanded element column of its
// aggregation rather than the aggregation's own label. An empty Column
// (a zero Scored: the aggregation at Index, read by its label) is not
// one.
func (s Scored) Element(aggs []*types.Aggregation) bool {
	if s.Column == "" || s.Index < 0 || s.Index >= len(aggs) || aggs[s.Index] == nil {
		return false
	}
	return s.Column != types.AggregationLabelOf(aggs[s.Index])
}

// first is the default reading: aggregations[0] and its label.
func first(aggs []*types.Aggregation) Scored {
	if len(aggs) == 0 || aggs[0] == nil {
		return Scored{}
	}
	return Scored{Column: types.AggregationLabelOf(aggs[0])}
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

// available is the refusal's Details.available: every output label in
// request order, each expanding aggregation's element columns (cols,
// when known) right after its label.
func available(aggs []*types.Aggregation, cols Columns) []string {
	out := Labels(aggs)
	if cols == nil {
		return out
	}
	with := make([]string, 0, len(out))
	for i, label := range out {
		with = append(with, label)
		if c, known := cols(i); known {
			for _, col := range c {
				if col != label {
					with = append(with, col)
				}
			}
		}
	}
	return with
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

// elementOf returns the aggregation an expanded element column name
// belongs to: the first aggregation whose label L makes name
// "L[<element>]" and whose columns (cols) carry name — or, when its
// columns are not known here, any such name. -1 when none does; cols
// nil never resolves one.
func elementOf(aggs []*types.Aggregation, name string, cols Columns) int {
	if cols == nil || !strings.HasSuffix(name, "]") {
		return -1
	}
	for i, a := range aggs {
		if a == nil {
			continue
		}
		label := types.AggregationLabelOf(a)
		if len(name) <= len(label)+2 || !strings.HasPrefix(name, label+"[") {
			continue
		}
		c, known := cols(i)
		if !known || slices.Contains(c, name) {
			return i
		}
	}
	return -1
}

// Resolve returns what spec scores in aggs: aggregations[0] when Params
// carries no (or an empty) `field`, else the aggregation whose output
// label equals it, else — cols reporting it — the aggregation one of
// whose expanded element columns "<label>[<element label>]" it names
// (Scored.Column that column). A `field` that is not a string, or
// names neither, is refused PULSE_OVERLAY_FIELD_UNKNOWN with Details
// {index, kind, field, available} — index is the spec's position in
// Request.Overlays, available the request's labels in order, each
// expanding aggregation's element columns after its label.
//
// Params that do not decode as a JSON object carry no `field`; the
// kind's own params gate owns that refusal.
func Resolve(aggs []*types.Aggregation, spec *types.OverlaySpec, index int, cols Columns) (Scored, error) {
	if spec == nil || len(spec.Params) == 0 {
		return first(aggs), nil
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return first(aggs), nil
	}
	return resolve(aggs, spec.Kind, m, index, cols)
}

// resolve is Resolve over decoded params, shared with the Compose and
// chain hosts.
func resolve(aggs []*types.Aggregation, kind types.OverlayKind, params map[string]any, index int, cols Columns) (Scored, error) {
	raw, present := params[Param]
	if !present || raw == nil {
		return first(aggs), nil
	}
	name, isString := raw.(string)
	if isString && name == "" {
		return first(aggs), nil
	}
	if isString {
		if i := IndexOf(aggs, name); i >= 0 {
			return Scored{Index: i, Column: name}, nil
		}
		if i := elementOf(aggs, name, cols); i >= 0 {
			return Scored{Index: i, Column: name}, nil
		}
	}
	msg := "overlay " + string(kind) + " params.field must name the output label of a request aggregation"
	if isString {
		msg = "overlay " + string(kind) + " params.field \"" + name + "\" names no request aggregation"
	}
	return Scored{}, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FIELD_UNKNOWN, msg, map[string]any{
		"index":     index,
		"kind":      string(kind),
		"field":     raw,
		"available": available(aggs, cols),
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
func ComposeKinds() []types.OverlayKind { return kindsIn(composeKinds) }

// chainKinds is the set of whole-chain (CHAIN-host) kinds whose SERIES
// arm scores one aggregation of each stage: the value of a stage's Data
// row is the aggregation Param names, its key the stage's group columns
// only — the Compose rule, per stage.
var chainKinds = map[types.OverlayKind]bool{
	types.OverlayKindIndexVsStage: true,
	types.OverlayKindDeltaVsStage: true,
}

// AppliesChain reports whether kind is a whole-chain kind whose SERIES
// arm scores the stage aggregation Param names.
func AppliesChain(kind types.OverlayKind) bool { return chainKinds[kind] }

// ChainKinds returns every kind AppliesChain accepts, in
// types.AllOverlayKinds order.
func ChainKinds() []types.OverlayKind { return kindsIn(chainKinds) }

// kindsIn returns the members of set in types.AllOverlayKinds order.
func kindsIn(set map[types.OverlayKind]bool) []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(set))
	for _, k := range types.AllOverlayKinds() {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

// ResolveComposeSlot returns what a Compose overlay spec scores on one
// slot (aggs: its Request.Aggregations; cols: its columns): the
// Resolve rule over the spec's decoded Params.
// The refusal is PULSE_OVERLAY_FIELD_UNKNOWN with Details {index, kind,
// field, available, slot} — index the spec's position in
// ComposedRequest.Overlays, slot the slot's label. The runtime
// dispatcher and predict-compose both call it, so the refusal cannot
// drift between them.
func ResolveComposeSlot(aggs []*types.Aggregation, spec *types.ComposeOverlaySpec, index int, slot string, cols Columns) (Scored, error) {
	if spec == nil {
		return first(aggs), nil
	}
	return located(aggs, cols, spec.Kind, spec.Params, index,
		map[string]any{"slot": slot}, " on slot \""+slot+"\"")
}

// ResolveChainStage returns what a whole-chain overlay spec scores on
// one stage (aggs: its Request.Aggregations; cols: its columns): the
// Resolve rule over the spec's Params. The
// refusal is PULSE_OVERLAY_FIELD_UNKNOWN with Details {index, kind,
// field, available, which, stage_index} — index the spec's position in
// ChainRequest.Overlays, which "target" or "ref", stage_index the
// stage it resolved to. The runtime dispatcher and predict-chain both
// call it, target before ref, so the refusal cannot drift between them.
func ResolveChainStage(aggs []*types.Aggregation, spec *types.ChainOverlaySpec, index int, which string, stage int, cols Columns) (Scored, error) {
	if spec == nil {
		return first(aggs), nil
	}
	return located(aggs, cols, spec.Kind, spec.Params, index,
		map[string]any{"which": which, "stage_index": stage}, " on the "+which+" stage")
}

// located is resolve with the host's locating Details merged into a
// refusal and suffix appended to its message.
func located(aggs []*types.Aggregation, cols Columns, kind types.OverlayKind, params map[string]any, index int, at map[string]any, suffix string) (Scored, error) {
	i, err := resolve(aggs, kind, params, index, cols)
	if ce, ok := err.(*errors.CodedError); ok {
		for k, v := range at {
			ce.Details[k] = v
		}
		ce.Message += suffix
	}
	return i, err
}
