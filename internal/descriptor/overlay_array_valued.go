package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Non-scalar overlay hosts — the Compose-host and whole-chain halves
// of the rule the field-reference walk applies to Request.Overlays
// (processOverlayArrayValuedRefusals, the Process-host half). An
// overlay handler reads ONE number per host row, so scoring an
// aggregation whose row value is not one number folds nothing —
// silently. Two classes are refused (vectors.AggregationKinds, so the
// instance's routing and extension declarations apply):
//
//   - array-valued (IsArrayValued — AGG_VEC_MEAN / AGG_VEC_SUM, or an
//     extension declared ArrayValued): one figure per member; under
//     params.expand its per-element columns are presentation only.
//     details.reason "array_valued".
//   - non-scalar (IsNonScalar — the RichAggregator lifts AGG_WELFORD's
//     moment struct, AGG_SET_UNION / AGG_SET_INTERSECTION's label
//     list, AGG_SET_FREQUENCY's count map, or an extension declared
//     NonScalar): details.reason "non_scalar". A kind that declares it
//     reads that aggregator's payload (overlayReadsPayload — the
//     OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF Welford triple) is exempt.
//
// Both are PROCESSING_CONFIG. Predict (ValidateCompose /
// ValidateChain) and the runtime (Service.applyComposeOverlays /
// Service.processChain) call the same functions; the Process-host half
// rides the field-reference walk, which predict and the runtime
// (Service.checkFieldRefs) share.
//
// Only the SCORED aggregation is judged (scoredNonScalarAggregation):
// a series overlay on a grouped Process request reads only the one
// aggregation params.field names (seriesfield.Resolve), and a
// non-crosstab slot or stage is narrowed to its group columns plus
// that one value column before any gate or handler reads a row
// (processing.narrowSeriesHost), so an unscored one never reaches the
// fold. A crosstab slot or stage is not narrowed, and keeps the
// whole-request array-valued rule (its cells carry rich payloads the
// cell kinds read).

// scoredValueReasons are the details.reason values, and the message
// phrase each one reads as.
const (
	reasonArrayValued = "array_valued"
	reasonNonScalar   = "non_scalar"
)

// scoredValueLong is the reason's message clause ("... is <clause>").
func scoredValueLong(reason string) string {
	if reason == reasonNonScalar {
		return "non-scalar (its value is a label list, a count map or a struct, not one number)"
	}
	return "array-valued (one figure per vector member; expanded columns are presentation only)"
}

// overlayReadsPayload reports whether kind declares it reads the
// payload of the (routed) aggregator t — the kind's hard feature
// dependency names it (hardEdges: OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF
// read AGG_WELFORD's triple). Such a kind scores the struct itself.
func overlayReadsPayload(kind types.OverlayKind, t types.AggregationType) bool {
	for _, dep := range hardEdges[string(kind)] {
		if dep == string(t) {
			return true
		}
	}
	return false
}

// scoredValueReason is the refusal reason for kind scoring t, or "".
// gateOwns (nil: none) names a non-scalar type another, earlier rule
// refuses on both sides, which keeps it.
func scoredValueReason(kind types.OverlayKind, t types.AggregationType, kinds vectors.AggregationKinds, gateOwns func(types.AggregationType) bool) string {
	switch {
	case kinds.IsArrayValued(t):
		return reasonArrayValued
	case kinds.IsNonScalar(t) && !overlayReadsPayload(kind, kinds.Routed(t)) && (gateOwns == nil || !gateOwns(t)):
		return reasonNonScalar
	}
	return ""
}

// firstArrayValuedAggregation returns the index and slot of r's first
// array-valued aggregation under the instance's routing, or -1, nil.
func firstArrayValuedAggregation(r *types.Request, kinds vectors.AggregationKinds) (int, *types.Aggregation) {
	if r == nil {
		return -1, nil
	}
	for i, a := range r.Aggregations {
		if a != nil && kinds.IsArrayValued(a.Type) {
			return i, a
		}
	}
	return -1, nil
}

// scoredNonScalarAggregation returns the index, slot and refusal
// reason of the aggregation kind scores on r when it is array-valued
// or non-scalar (scoredValueReason), else -1, nil, "". scored resolves
// that aggregation's index (params.field, else 0 — the narrowing's own
// reading); a resolution error is left to the field rule
// (PULSE_OVERLAY_FIELD_UNKNOWN), which owns it. A crosstab request is
// never narrowed, so every aggregation counts there, under the
// array-valued rule only. kinds is the instance's aggregation routing
// (aggregationKinds). The Process host (fieldRefWalk's overlay step),
// the Compose host and the chain host all judge through it.
func scoredNonScalarAggregation(r *types.Request, kind types.OverlayKind, kinds vectors.AggregationKinds, gateOwns func(types.AggregationType) bool, scored func([]*types.Aggregation) (int, error)) (int, *types.Aggregation, string) {
	if r == nil {
		return -1, nil, ""
	}
	if r.Crosstab != nil {
		if i, a := firstArrayValuedAggregation(r, kinds); a != nil {
			return i, a, reasonArrayValued
		}
		return -1, nil, ""
	}
	ai, err := scored(r.Aggregations)
	if err != nil || ai < 0 || ai >= len(r.Aggregations) {
		return -1, nil, ""
	}
	a := r.Aggregations[ai]
	if a == nil {
		return -1, nil, ""
	}
	if reason := scoredValueReason(kind, a.Type, kinds, gateOwns); reason != "" {
		return ai, a, reason
	}
	return -1, nil, ""
}

// ComposeOverlayArrayValuedRefusal refuses a Compose overlay that
// scores (on its reference, a target, or — no targets — every slot) an
// array-valued aggregation: the one params.field names on a kind
// seriesfield.AppliesCompose accepts, else the slot's Aggregations[0]
// (any aggregation on a crosstab slot). labels are the slots' labels
// (index-aligned with requests, defaults applied). Nil when no overlay
// reads one; a kind the instance hides is left to the kind rule.
func ComposeOverlayArrayValuedRefusal(overlays []types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot) error {
	for i := range overlays {
		if err := composeOverlayArrayValuedRefusal(i, &overlays[i], requests, labels, inst); err != nil {
			return err
		}
	}
	return nil
}

func composeOverlayArrayValuedRefusal(i int, spec *types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot) *errors.CodedError {
	kind := string(spec.Kind)
	if inst.Hidden(kind) {
		return nil
	}
	reads := func(label string) bool {
		if len(spec.Targets) == 0 || label == spec.Reference {
			return true
		}
		for _, t := range spec.Targets {
			if t == label {
				return true
			}
		}
		return false
	}
	for j, r := range requests {
		if r == nil || j >= len(labels) || !reads(labels[j]) {
			continue
		}
		slot := labels[j]
		ai, agg, reason := scoredNonScalarAggregation(r, spec.Kind, aggregationKinds(inst, nil), nil, func(aggs []*types.Aggregation) (int, error) {
			if !seriesfield.AppliesCompose(spec.Kind) {
				return 0, nil
			}
			return seriesfield.ResolveComposeSlot(aggs, spec, i, slot)
		})
		if agg == nil {
			continue
		}
		label := types.AggregationLabelOf(agg)
		host := "requests[" + strconv.Itoa(j) + "].aggregations[" + strconv.Itoa(ai) + "]"
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"overlays["+strconv.Itoa(i)+"] ("+kind+") reads slot "+strconv.Quote(labels[j])+", whose "+host+" "+label+" ("+string(agg.Type)+
				") is "+scoredValueLong(reason)+"; an overlay folds one number per host row — compare a scalar aggregation instead",
			map[string]any{"overlay_index": i, "kind": kind, "slot": labels[j], "host": host,
				"aggregation": string(agg.Type), "label": label, "reason": reason})
	}
	return nil
}

// ChainOverlayArrayValuedRefusal refuses a whole-chain overlay that
// scores an array-valued aggregation on its target or reference stage
// (only the last stage can carry one: an array-valued stage may not
// feed another) — the one params.field names on a kind
// seriesfield.AppliesChain accepts, else the stage's Aggregations[0]
// (any aggregation on a crosstab stage). A
// spec whose stage references do not resolve is left to the
// stage-reference rules. Nil when no overlay reads one.
func ChainOverlayArrayValuedRefusal(req *types.ChainRequest, inst *InstanceSnapshot) error {
	if req == nil {
		return nil
	}
	for i, spec := range req.Overlays {
		if err := chainOverlayArrayValuedRefusal(req, i, spec, inst); err != nil {
			return err
		}
	}
	return nil
}

func chainOverlayArrayValuedRefusal(req *types.ChainRequest, i int, spec *types.ChainOverlaySpec, inst *InstanceSnapshot) *errors.CodedError {
	if spec == nil || inst.Hidden(string(spec.Kind)) {
		return nil
	}
	for _, side := range []struct {
		role string
		ref  types.StageRef
		dflt bool
	}{{"target", spec.Target, true}, {"ref", spec.Ref, false}} {
		idx, ok := chainStageIndex(req, side.ref, side.dflt)
		if !ok || req.Stages[idx] == nil {
			continue
		}
		role := side.role
		ai, agg, reason := scoredNonScalarAggregation(req.Stages[idx].Request, spec.Kind, aggregationKinds(inst, nil), chainGateOwns(inst), func(aggs []*types.Aggregation) (int, error) {
			if !seriesfield.AppliesChain(spec.Kind) {
				return 0, nil
			}
			return seriesfield.ResolveChainStage(aggs, spec, i, role, idx)
		})
		if agg == nil {
			continue
		}
		label := types.AggregationLabelOf(agg)
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"chain overlays["+strconv.Itoa(i)+"] ("+string(spec.Kind)+") "+side.role+" stage "+strconv.Itoa(idx)+" aggregation "+label+" ("+string(agg.Type)+
				") is "+scoredValueLong(reason)+"; an overlay folds one number per host row — compare a scalar aggregation instead",
			map[string]any{"overlay_index": i, "kind": string(spec.Kind), "role": side.role, "stage_index": idx,
				"stage_name": req.Stages[idx].Name, "aggregation_index": ai, "aggregation": string(agg.Type),
				"label": label, "reason": reason})
	}
	return nil
}

// chainGateOwns names the non-scalar aggregators the ProcessChain stage
// gate refuses first on both sides: a non-mergeable one
// (mergegate.MergeRefusal — AGG_WELFORD, an extension not declared
// Mergeable) is PULSE_CHAIN_NOT_MERGEABLE on every stage, so the
// overlay rule (which the runtime checks before any stage, predict
// after them) leaves it to that gate and the first refusal matches.
func chainGateOwns(inst *InstanceSnapshot) func(types.AggregationType) bool {
	facts := inst.Extensions().mergeFacts(inst)
	return func(t types.AggregationType) bool {
		mergeable, ext := facts.Aggregator(string(t))
		if !ext {
			mergeable = t.Mergeable()
		}
		return !mergeable
	}
}

// chainStageIndex resolves a StageRef the way the chain overlay
// resolvers do (Index, else Name, else — dflt — the last stage); ok is
// false when it does not resolve to exactly one stage.
func chainStageIndex(req *types.ChainRequest, ref types.StageRef, dflt bool) (int, bool) {
	n := len(req.Stages)
	switch {
	case ref.Index != nil && ref.Name != "":
		return 0, false
	case ref.Index != nil:
		return *ref.Index, *ref.Index >= 0 && *ref.Index < n
	case ref.Name != "":
		for i, st := range req.Stages {
			if st != nil && st.Name == ref.Name {
				return i, true
			}
		}
		return 0, false
	case dflt && n > 0:
		return n - 1, true
	}
	return 0, false
}

// processOverlayArrayValuedRefusals is the Process-host half: one
// refusal per distinct array-valued or non-scalar aggregation an entry
// of req.Overlays scores — the one params.field names on a kind
// seriesfield.Applies accepts, else Aggregations[0] (every other kind
// reads the first aggregation as its host value). A crosstab request is
// out of scope here (its overlays read cells). An unresolvable field is
// left to PULSE_OVERLAY_FIELD_UNKNOWN. Details {aggregation, label,
// slot, reason: "array_valued" | "non_scalar"}; slot is
// "aggregations[<i>]".
func processOverlayArrayValuedRefusals(req *types.Request, kinds vectors.AggregationKinds) []*errors.CodedError {
	if req == nil || len(req.Overlays) == 0 || req.Crosstab != nil {
		return nil
	}
	var out []*errors.CodedError
	seen := map[int]bool{}
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		ai, host, reason := scoredNonScalarAggregation(req, spec.Kind, kinds, nil, func(aggs []*types.Aggregation) (int, error) {
			if !seriesfield.Applies(spec.Kind) {
				return 0, nil
			}
			return seriesfield.Resolve(aggs, spec, i)
		})
		if host == nil || seen[ai] {
			continue
		}
		seen[ai] = true
		label := types.AggregationLabelOf(host)
		short := "array-valued"
		long := "array-valued (one figure per vector member)"
		if reason == reasonNonScalar {
			short = "non-scalar (a label list, a count map or a struct, not one number)"
			long = short
		}
		msg := "overlays read the first aggregation as their host value, and " + label + " (" + string(host.Type) + ") is " + short + "; put a scalar aggregation first"
		if ai != 0 {
			msg = "overlays[" + strconv.Itoa(i) + "] (" + string(spec.Kind) + ") params.field scores " + label + " (" + string(host.Type) +
				"), which is " + long + "; an overlay folds one number per host row — name a scalar aggregation instead"
		}
		out = append(out, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, msg,
			map[string]any{"aggregation": string(host.Type), "label": label, "slot": "aggregations[" + strconv.Itoa(ai) + "]", "reason": reason}))
	}
	return out
}
