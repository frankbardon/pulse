package processing

import (
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// projectComposeSeriesSlots narrows each SERIES slot a Compose overlay
// spec reads to the columns the series arm keys and scores: the slot's
// GROUP columns (vectors.GroupKeyField of every Request.Groups entry)
// and ONE value column, the output label of the aggregation the spec
// scores on that slot. That aggregation is the one params.field names
// (seriesfield.ResolveComposeSlot) on a kind seriesfield.AppliesCompose
// accepts, else the slot's Aggregations[0].
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
func projectComposeSeriesSlots(spec *types.ComposeOverlaySpec, route types.OverlayKind, specIdx int, ref *types.Response, refIdx int, targets []*types.Response, targetIdxs []int, requests []*types.Request) (*types.Response, []*types.Response, error) {
	if len(requests) == 0 {
		return ref, targets, nil
	}
	narrowedRef, err := projectComposeSeriesSlot(spec, route, specIdx, spec.Reference, ref, refIdx, requests)
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
		if out[i], err = projectComposeSeriesSlot(spec, route, specIdx, label, t, idx, requests); err != nil {
			return nil, nil, err
		}
	}
	return narrowedRef, out, nil
}

// projectComposeSeriesSlot is projectComposeSeriesSlots for one slot.
func projectComposeSeriesSlot(spec *types.ComposeOverlaySpec, route types.OverlayKind, specIdx int, label string, resp *types.Response, idx int, requests []*types.Request) (*types.Response, error) {
	if idx < 0 || idx >= len(requests) {
		return resp, nil
	}
	req := requests[idx]
	if req == nil || req.Crosstab != nil {
		return resp, nil
	}
	aggIdx := 0
	if seriesfield.AppliesCompose(route) {
		i, err := seriesfield.ResolveComposeSlot(req.Aggregations, spec, specIdx, label)
		if err != nil {
			return nil, err
		}
		aggIdx = i
	}
	return narrowSeriesHost(req, aggIdx, resp), nil
}

// narrowSeriesHost is the narrowing both the Compose and the chain
// (projectChainSeriesStages) series arms share: resp's Data rows cut
// to req's GROUP columns (vectors.GroupKeyField of every Groups entry)
// plus the output label of req.Aggregations[aggIdx], in a shallow copy
// with fresh rows — resp itself is never touched. resp comes back as
// it is when aggIdx is out of range, it carries a matrix, or it has no
// Data.
func narrowSeriesHost(req *types.Request, aggIdx int, resp *types.Response) *types.Response {
	if aggIdx >= len(req.Aggregations) || resp == nil || len(resp.Data) == 0 ||
		(resp.Crosstab != nil && resp.Crosstab.Matrix != nil) {
		return resp
	}
	valueCol := types.AggregationLabelOf(req.Aggregations[aggIdx])
	keep := make([]string, 0, len(req.Groups)+1)
	for _, g := range req.Groups {
		if g != nil {
			keep = append(keep, vectors.GroupKeyField(g.Type, g))
		}
	}
	keep = append(keep, valueCol)
	narrowed := *resp
	narrowed.Data = make([]map[string]any, len(resp.Data))
	for r, row := range resp.Data {
		nr := make(map[string]any, len(keep))
		for _, col := range keep {
			if v, ok := row[col]; ok {
				nr[col] = v
			}
		}
		narrowed.Data[r] = nr
	}
	return &narrowed
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
func projectChainSeriesStages(spec *types.ChainOverlaySpec, route types.OverlayKind, specIdx int, target, ref *types.Response, targetIdx, refIdx int, stageReqs []*types.Request) (*types.Response, *types.Response, error) {
	if len(stageReqs) == 0 {
		return target, ref, nil
	}
	narrowedTarget, err := projectChainSeriesStage(spec, route, specIdx, "target", target, targetIdx, stageReqs)
	if err != nil {
		return nil, nil, err
	}
	narrowedRef, err := projectChainSeriesStage(spec, route, specIdx, "ref", ref, refIdx, stageReqs)
	if err != nil {
		return nil, nil, err
	}
	return narrowedTarget, narrowedRef, nil
}

// projectChainSeriesStage is projectChainSeriesStages for one stage.
func projectChainSeriesStage(spec *types.ChainOverlaySpec, route types.OverlayKind, specIdx int, which string, resp *types.Response, idx int, stageReqs []*types.Request) (*types.Response, error) {
	if idx < 0 || idx >= len(stageReqs) {
		return resp, nil
	}
	req := stageReqs[idx]
	if req == nil || req.Crosstab != nil {
		return resp, nil
	}
	aggIdx := 0
	if seriesfield.AppliesChain(route) {
		i, err := seriesfield.ResolveChainStage(req.Aggregations, spec, specIdx, which, idx)
		if err != nil {
			return nil, err
		}
		aggIdx = i
	}
	return narrowSeriesHost(req, aggIdx, resp), nil
}
