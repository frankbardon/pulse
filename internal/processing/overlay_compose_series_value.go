package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// projectComposeSeriesSlots narrows each SERIES slot a Compose overlay
// spec reads to the columns the series arm keys and scores: the slot's
// GROUP columns (vectors.GroupKeyField of every Request.Groups entry)
// and ONE value column, the column the spec scores on that slot: the
// output label of the aggregation params.field names
// (seriesfield.ResolveComposeSlot) on a kind seriesfield.AppliesCompose
// accepts, or the expanded element column "<label>[<element label>]"
// it names (resolved against the slot's members over slotSchema(j) —
// vectors.AggregationColumns), else the slot's Aggregations[0].
//
// Without it, encodeSeriesRow keyed a row on every column but the
// alphabetically first numeric one: a slot with two or more
// aggregations scored whichever label sorted first and folded the
// others' VALUES into the key, so reference and target diverged
// (PULSE_OVERLAY_KEY_SET_DIVERGENT) or the wrong measure was scored.
// After it every downstream reader — the key-set gate, the schema
// gate, the series handlers — sees exactly one numeric column per row,
// so encodeSeriesRow's key is the group columns alone in the encoding
// it always used (the value fold is all that drops).
//
// The narrowed slot is a shallow copy with fresh Data rows: the slot's
// own Response (and so ComposedResponse.Responses) is never touched.
// A slot is left as it is when no authored Request is in reach (the
// bare ApplyComposeOverlays entry), its Request declares a crosstab,
// or its Response carries a matrix or no Data. ref / targets are
// returned in the order given; a field refusal (reference first, then
// targets in order) is returned as is, the same refusal predict-compose
// raises from the same Requests.
func projectComposeSeriesSlots(spec *types.ComposeOverlaySpec, route types.OverlayKind, specIdx int, ref *types.Response, refIdx int, targets []*types.Response, targetIdxs []int, requests []*types.Request, slotSchema func(j int) *encoding.Schema, kinds vectors.AggregationKinds) (*types.Response, []*types.Response, error) {
	if len(requests) == 0 {
		return ref, targets, nil
	}
	narrowedRef, err := projectComposeSeriesSlot(spec, route, specIdx, spec.Reference, ref, refIdx, requests, slotSchema, kinds)
	if err != nil {
		return nil, nil, err
	}
	out := make([]*types.Response, len(targets))
	for i, t := range targets {
		label := ""
		if i < len(spec.Targets) {
			label = spec.Targets[i]
		}
		idx := -1
		if i < len(targetIdxs) {
			idx = targetIdxs[i]
		}
		if out[i], err = projectComposeSeriesSlot(spec, route, specIdx, label, t, idx, requests, slotSchema, kinds); err != nil {
			return nil, nil, err
		}
	}
	return narrowedRef, out, nil
}

// projectComposeSeriesSlot is projectComposeSeriesSlots for one slot.
func projectComposeSeriesSlot(spec *types.ComposeOverlaySpec, route types.OverlayKind, specIdx int, label string, resp *types.Response, idx int, requests []*types.Request, slotSchema func(j int) *encoding.Schema, kinds vectors.AggregationKinds) (*types.Response, error) {
	if idx < 0 || idx >= len(requests) {
		return resp, nil
	}
	req := requests[idx]
	if req == nil || req.Crosstab != nil {
		return resp, nil
	}
	scored := seriesfield.Scored{}
	if len(req.Aggregations) > 0 && req.Aggregations[0] != nil {
		scored.Column = types.AggregationLabelOf(req.Aggregations[0])
	}
	if seriesfield.AppliesCompose(route) {
		var schema func() *encoding.Schema
		if slotSchema != nil {
			schema = func() *encoding.Schema { return slotSchema(idx) }
		}
		sc, err := seriesfield.ResolveComposeSlot(req.Aggregations, spec, specIdx, label, vectors.AggregationColumns(req, schema, kinds))
		if err != nil {
			return nil, err
		}
		scored = sc
	}
	return narrowSeriesHost(req, scored, resp), nil
}

// narrowSeriesHost is the narrowing both the Compose and the chain
// (projectChainSeriesStages) series arms share: resp's Data rows cut
// to req's GROUP columns (vectors.GroupKeyField of every Groups entry)
// plus the scored column — the output label of
// req.Aggregations[scored.Index], or the expanded element column
// scored.Column names (an ordinary f64 column; an element no row
// answered is NaN, read like any scalar aggregate's undefined figure)
// — in a shallow copy with fresh rows; resp itself is never touched.
// resp comes back as it is when the index is out of range, it carries
// a matrix, or it has no Data.
func narrowSeriesHost(req *types.Request, scored seriesfield.Scored, resp *types.Response) *types.Response {
	aggIdx := scored.Index
	if aggIdx < 0 || aggIdx >= len(req.Aggregations) || resp == nil || len(resp.Data) == 0 ||
		(resp.Crosstab != nil && resp.Crosstab.Matrix != nil) {
		return resp
	}
	valueCol := scored.Column
	if valueCol == "" {
		valueCol = types.AggregationLabelOf(req.Aggregations[aggIdx])
	}
	keep := make([]string, 0, len(req.Groups)+1)
	for _, g := range req.Groups {
		if g != nil {
			keep = append(keep, vectors.GroupKeyField(g.Type, g))
		}
	}
	keep = append(keep, valueCol)
	// The components belong to the scored AGGREGATION (its label),
	// whichever of its columns is scored.
	groups := scoredGroupComponents(resp, types.AggregationLabelOf(req.Aggregations[aggIdx]), aggIdx)
	narrowed := *resp
	narrowed.Data = make([]map[string]any, len(resp.Data))
	for r, row := range resp.Data {
		nr := make(map[string]any, len(keep))
		for _, col := range keep {
			if v, ok := row[col]; ok {
				nr[col] = v
			}
		}
		if leg, ok := welfordComponentsLeg(nr[valueCol], groups, r); ok {
			nr[valueCol] = leg
		}
		narrowed.Data[r] = nr
	}
	return &narrowed
}

// scoredGroupComponents returns the per-row components (Groups, in
// Response.Data order) of the scored aggregation — the
// Components.Aggregations entry labelled valueCol, else the one at
// aggIdx — or nil when the response carries none.
func scoredGroupComponents(resp *types.Response, valueCol string, aggIdx int) []types.AggregationGroupComponents {
	if resp.Components == nil {
		return nil
	}
	aggs := resp.Components.Aggregations
	for _, a := range aggs {
		if a.Label == valueCol {
			return a.Groups
		}
	}
	if aggIdx < len(aggs) {
		return aggs[aggIdx].Groups
	}
	return nil
}

// welfordComponentsLeg is the components-source reading of an
// AGG_WELFORD row value: the in-process value is the WelfordTriple
// struct, which the two-sample vs-ref handlers (OVERLAY_T_VS_REF /
// OVERLAY_Z_VS_REF) never read — it carries no weighting — so a
// Compose / chain slot scoring it came back as an empty layer. The
// row's bucket components carry the full leg (mean, variance, m2, n
// and, weighted, sum_weights / n_eff): they replace the struct with
// the {mean, variance, n, …} map the handlers read through the
// weighted-host rule (readMeanLeg), exactly as a JSON-decoded slot's
// components would. ok is false — the value kept — for any other
// value, or when the bucket's components are absent (components
// disabled) or lack the Welford keys.
func welfordComponentsLeg(v any, groups []types.AggregationGroupComponents, r int) (map[string]any, bool) {
	if _, isTriple := v.(WelfordTriple); !isTriple || r >= len(groups) {
		return nil, false
	}
	g := groups[r]
	leg := map[string]any{"n": g.N}
	for _, k := range []string{"mean", "variance", "m2"} {
		x, ok := g.Operator[k]
		if !ok {
			return nil, false
		}
		leg[k] = x
	}
	if g.SumWeights != nil {
		leg["sum_weights"] = *g.SumWeights
	}
	if g.NEff != nil {
		leg["n_eff"] = *g.NEff
	}
	return leg, true
}

// projectChainSeriesStages is projectComposeSeriesSlots for a
// whole-chain overlay spec: the target and the reference stage each
// narrow (narrowSeriesHost) to their group columns plus the one scored
// aggregation — the one params.field names (seriesfield.ResolveChainStage)
// on a kind seriesfield.AppliesChain accepts, else the stage's
// Aggregations[0]. Before it, encodeSeriesRow scored a stage on its
// alphabetically first numeric column and folded every other
// aggregation's value into the row key.
//
// A stage is left as it is when no stage Request is in reach (the bare
// ApplyChainOverlays entry) or its Request declares a crosstab. The
// field refusal is the target's, then the reference's — the order
// predict-chain checks them in. ChainResponse.Stages is never touched:
// a narrowed stage is a shallow copy.
func projectChainSeriesStages(spec *types.ChainOverlaySpec, route types.OverlayKind, specIdx int, target, ref *types.Response, targetIdx, refIdx int, stageReqs []*types.Request, stageSchema func(i int) *encoding.Schema, kinds vectors.AggregationKinds) (*types.Response, *types.Response, error) {
	if len(stageReqs) == 0 {
		return target, ref, nil
	}
	narrowedTarget, err := projectChainSeriesStage(spec, route, specIdx, "target", target, targetIdx, stageReqs, stageSchema, kinds)
	if err != nil {
		return nil, nil, err
	}
	narrowedRef, err := projectChainSeriesStage(spec, route, specIdx, "ref", ref, refIdx, stageReqs, stageSchema, kinds)
	if err != nil {
		return nil, nil, err
	}
	return narrowedTarget, narrowedRef, nil
}

// projectChainSeriesStage is projectChainSeriesStages for one stage.
func projectChainSeriesStage(spec *types.ChainOverlaySpec, route types.OverlayKind, specIdx int, which string, resp *types.Response, idx int, stageReqs []*types.Request, stageSchema func(i int) *encoding.Schema, kinds vectors.AggregationKinds) (*types.Response, error) {
	if idx < 0 || idx >= len(stageReqs) {
		return resp, nil
	}
	req := stageReqs[idx]
	if req == nil || req.Crosstab != nil {
		return resp, nil
	}
	scored := seriesfield.Scored{}
	if len(req.Aggregations) > 0 && req.Aggregations[0] != nil {
		scored.Column = types.AggregationLabelOf(req.Aggregations[0])
	}
	if seriesfield.AppliesChain(route) {
		var schema func() *encoding.Schema
		if stageSchema != nil {
			schema = func() *encoding.Schema { return stageSchema(idx) }
		}
		sc, err := seriesfield.ResolveChainStage(req.Aggregations, spec, specIdx, which, idx, vectors.AggregationColumns(req, schema, kinds))
		if err != nil {
			return nil, err
		}
		scored = sc
	}
	return narrowSeriesHost(req, scored, resp), nil
}
