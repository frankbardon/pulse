package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
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
	cols := composeSlotColumns(requests, slotSchema, kinds)
	refScored, err := composeSeriesScored(spec, route, specIdx, spec.Reference, refIdx, requests, cols)
	if err != nil {
		return nil, nil, err
	}
	narrowedRef := narrowComposeSeriesSlot(ref, refIdx, refScored, requests)
	out := make([]*types.Response, len(targets))
	for i, t := range targets {
		idx := composeTargetIdx(targetIdxs, i)
		sc, err := composeSeriesScored(spec, route, specIdx, composeTargetLabel(spec, i), idx, requests, cols)
		if err != nil {
			return nil, nil, err
		}
		out[i] = narrowComposeSeriesSlot(t, idx, sc, requests)
	}
	return narrowedRef, out, nil
}

// applyComposeVsRefMeasure is the two-sample measure rule (U66 #444)
// at the Compose dispatcher, the one predict-compose applies at gate
// 2e (overlayhost.ComposeVsRefMeasureRefusal): a T / Z vs-ref slot's
// scored measure is a mean, or its components carry the moments. It
// runs after the slot shape and schema gates (predict stands aside on
// a mixed-shape spec) and before the key-set gate. Then, on every
// narrowed slot whose measure carries the moments (AGG_VARIANCE /
// AGG_STDDEV / a moment-declaring extension; AGG_WELFORD's triple
// narrowSeriesHost already reads), each row's value becomes the
// group's (mean, variance, n) leg off the slot's components
// (momentComponentsLeg) — a slot that came back without them is
// PULSE_OVERLAY_COMPONENTS_REQUIRED: its value is the variance, never a
// mean. ref / targets come back in the order given; a rewritten slot is
// a shallow copy with fresh rows, the slot's own Response untouched.
func applyComposeVsRefMeasure(spec *types.ComposeOverlaySpec, route types.OverlayKind, specIdx int, ref *types.Response, refIdx int, targets []*types.Response, targetIdxs []int, requests []*types.Request, slotSchema func(j int) *encoding.Schema, kinds vectors.AggregationKinds) (*types.Response, []*types.Response, error) {
	if len(requests) == 0 || !overlayhost.VsRefMeasureKind(route) {
		return ref, targets, nil
	}
	cols := composeSlotColumns(requests, slotSchema, kinds)
	slots := map[string]int{spec.Reference: refIdx}
	for i := range spec.Targets {
		slots[spec.Targets[i]] = composeTargetIdx(targetIdxs, i)
	}
	slotIndex := func(label string) int {
		if j, ok := slots[label]; ok {
			return j
		}
		return -1
	}
	if ce := overlayhost.ComposeVsRefMeasureRefusal(spec, route, specIdx, requests, slotIndex, cols, kinds); ce != nil {
		return nil, nil, ce
	}
	ref, err := composeMomentLegs(spec, route, specIdx, spec.Reference, ref, refIdx, requests, cols, kinds)
	if err != nil {
		return nil, nil, err
	}
	out := make([]*types.Response, len(targets))
	for i, t := range targets {
		if out[i], err = composeMomentLegs(spec, route, specIdx, composeTargetLabel(spec, i), t, composeTargetIdx(targetIdxs, i), requests, cols, kinds); err != nil {
			return nil, nil, err
		}
	}
	return ref, out, nil
}

// composeSlotColumns is slot j's column lister (vectors.AggregationColumns
// over slotSchema(j)).
func composeSlotColumns(requests []*types.Request, slotSchema func(j int) *encoding.Schema, kinds vectors.AggregationKinds) func(j int) seriesfield.Columns {
	return func(j int) seriesfield.Columns {
		var schema func() *encoding.Schema
		if slotSchema != nil {
			schema = func() *encoding.Schema { return slotSchema(j) }
		}
		return vectors.AggregationColumns(requestAt(requests, j), schema, kinds)
	}
}

func composeTargetIdx(targetIdxs []int, i int) int {
	if i < len(targetIdxs) {
		return targetIdxs[i]
	}
	return -1
}

func composeTargetLabel(spec *types.ComposeOverlaySpec, i int) string {
	if i < len(spec.Targets) {
		return spec.Targets[i]
	}
	return ""
}

// composeSeriesScored is what one SERIES slot is scored on: the
// aggregation params.field names on a kind seriesfield.AppliesCompose
// accepts (its refusal returned as is), else Aggregations[0]. The zero
// Scored for a slot with no authored Request or a crosstab.
func composeSeriesScored(spec *types.ComposeOverlaySpec, route types.OverlayKind, specIdx int, label string, idx int, requests []*types.Request, cols func(j int) seriesfield.Columns) (seriesfield.Scored, error) {
	req := requestAt(requests, idx)
	if req == nil || req.Crosstab != nil {
		return seriesfield.Scored{}, nil
	}
	scored := seriesfield.Scored{}
	if len(req.Aggregations) > 0 && req.Aggregations[0] != nil {
		scored.Column = types.AggregationLabelOf(req.Aggregations[0])
	}
	if seriesfield.AppliesCompose(route) {
		return seriesfield.ResolveComposeSlot(req.Aggregations, spec, specIdx, label, cols(idx))
	}
	return scored, nil
}

// narrowComposeSeriesSlot narrows one slot (narrowSeriesHost) to its
// scored column; a slot with no authored Request or a crosstab is left
// as it is.
func narrowComposeSeriesSlot(resp *types.Response, idx int, scored seriesfield.Scored, requests []*types.Request) *types.Response {
	req := requestAt(requests, idx)
	if req == nil || req.Crosstab != nil {
		return resp
	}
	return narrowSeriesHost(req, scored, resp)
}

// composeMomentLegs returns one narrowed slot with its rows' values
// replaced by the moment legs of its moment-carrying measure
// (applyComposeVsRefMeasure); the slot as it is for any other measure.
func composeMomentLegs(spec *types.ComposeOverlaySpec, route types.OverlayKind, specIdx int, label string, slot *types.Response, idx int, requests []*types.Request, cols func(j int) seriesfield.Columns, kinds vectors.AggregationKinds) (*types.Response, error) {
	req := requestAt(requests, idx)
	if req == nil || req.Crosstab != nil || slot == nil || len(slot.Data) == 0 {
		return slot, nil
	}
	scored, err := composeSeriesScored(spec, route, specIdx, label, idx, requests, cols)
	ai := scored.Index
	if err != nil || ai < 0 || ai >= len(req.Aggregations) || req.Aggregations[ai] == nil || scored.Element(req.Aggregations) {
		return slot, nil
	}
	agg := req.Aggregations[ai]
	routed := kinds.Routed(agg.Type)
	if routed == types.AGG_WELFORD || overlayhost.VsRefMeasureRead(routed, false, kinds.ExtensionCarriesMoments(agg.Type)) != overlayhost.VsRefReadMoments {
		return slot, nil // AGG_WELFORD's triple: narrowSeriesHost's reading, as before
	}
	aggLabel := types.AggregationLabelOf(agg)
	groups := scoredGroupComponents(slot, aggLabel, ai)
	popVariance := overlayhost.PopulationVarianceCell(routed)
	out := *slot
	out.Data = make([]map[string]any, len(slot.Data))
	for r, row := range slot.Data {
		nr := make(map[string]any, len(row))
		for k, v := range row {
			nr[k] = v
		}
		out.Data[r] = nr
		if _, isLeg := row[aggLabel].(map[string]any); isLeg {
			continue
		}
		leg, ok := momentComponentsLeg(groups, r, popVariance)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_COMPONENTS_REQUIRED,
				"overlays["+strconv.Itoa(specIdx)+"] ("+string(spec.Kind)+") reads slot "+strconv.Quote(label)+"'s "+aggLabel+" ("+string(agg.Type)+
					") from its per-group components (mean, variance, n), and the slot came back without them; enable components on that slot",
				map[string]any{"overlay_index": specIdx, "kind": string(spec.Kind), "slot": label,
					"aggregation": string(agg.Type), "label": aggLabel, "reason": "components_disabled"})
		}
		nr[aggLabel] = leg
	}
	return &out, nil
}

// momentComponentsLeg is the components reading of a moment-carrying
// scalar measure's group row r (AGG_VARIANCE / AGG_STDDEV, or an
// extension declaring mean + variance): the {mean, variance, n, …} map
// the two-sample handlers read through the weighted-host rule
// (readMeanLeg) — the crosstab cell arm's reading (CrosstabHostView.
// MeanLeg). An unweighted population-variance type's variance is
// converted to the sample variance (overlayhost.SampleVariance); an
// extension's is read as is. ok is false when the group's components
// are absent or lack mean / variance.
func momentComponentsLeg(groups []types.AggregationGroupComponents, r int, popVariance bool) (map[string]any, bool) {
	if r >= len(groups) {
		return nil, false
	}
	g := groups[r]
	leg := map[string]any{"n": g.N}
	for _, k := range []string{"mean", "variance"} {
		x, ok := g.Operator[k]
		if !ok {
			return nil, false
		}
		leg[k] = x
	}
	for _, k := range []string{"m2", "origin", "shifted_mean"} {
		if x, ok := g.Operator[k]; ok {
			leg[k] = x
		}
	}
	if g.SumWeights != nil {
		leg["sum_weights"] = *g.SumWeights
	}
	if g.NEff != nil {
		leg["n_eff"] = *g.NEff
	}
	if popVariance && g.SumWeights == nil {
		v, _ := componentsNumeric(leg, "variance")
		m2, hasM2 := componentsNumeric(leg, "m2")
		n, _ := componentsNumeric(leg, "n")
		leg["variance"] = overlayhost.SampleVariance(v, m2, hasM2, n)
	}
	return leg, true
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
	for _, k := range []string{"origin", "shifted_mean"} {
		if x, ok := g.Operator[k]; ok {
			leg[k] = x
		}
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

// undefinedSeriesPair reports whether a Compose / chain SERIES pair has
// an undefined host value on either side — NaN: an expanded element no
// row answered, or any aggregate's undefined figure (AGG_AVERAGE over a
// group with no value, …). Such a value is MISSING, the rule the
// request host applies (buildSeriesHostFromGroupedResponse): the
// pair's entry keeps its key but carries no statistic (absent on the
// wire, never null), raises no warning and stays out of the layer
// summary. A null statistic stays reserved for a figure the kind
// computed and found undefined (a zero reference, a degenerate test).
func undefinedSeriesPair(target, ref float64) bool {
	return math.IsNaN(target) || math.IsNaN(ref)
}

// seriesPairDiff is a vs-ref series pair's difference of means: the
// legs' (meanLeg.diff, through both origins when both rows carry an
// AGG_WELFORD leg) when both sides are triples, else the two values'.
func seriesPairDiff(tTriple bool, tLeg meanLeg, tMean float64, rTriple bool, rLeg meanLeg, rMean float64) float64 {
	if tTriple && rTriple {
		return tLeg.diff(rLeg)
	}
	return tMean - rMean
}
