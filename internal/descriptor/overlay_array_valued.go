package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/types"
)

// Array-valued overlay hosts — the Compose-host and whole-chain halves
// of the rule the field-reference walk applies to Request.Overlays. A
// vector aggregation (types.AggregationType.ArrayValued — AGG_VEC_MEAN /
// AGG_VEC_SUM — or an extension declared ArrayValued; aggregationKinds)
// holds one figure per member, and under params.expand its
// per-element columns are presentation only. An overlay handler reads
// one number per host row, so scoring one would fold an array as NaN —
// silently. Both rules refuse instead: PROCESSING_CONFIG,
// details.reason "array_valued". Predict (ValidateCompose /
// ValidateChain) and the runtime (Service.applyComposeOverlays /
// Service.processChain) call the same functions.
//
// Only the SCORED aggregation is judged (scoredArrayValuedAggregation):
// a non-crosstab slot or stage is narrowed to its group columns plus
// that one value column before any gate or handler reads a row
// (processing.narrowSeriesHost), so an unscored array-valued
// aggregation never reaches the fold. A crosstab slot or stage is not
// narrowed, and keeps the whole-request rule.

// firstArrayValuedAggregation returns the index and slot of r's first
// array-valued aggregation under the instance's routing, or -1, nil.
func firstArrayValuedAggregation(r *types.Request, inst *InstanceSnapshot) (int, *types.Aggregation) {
	if r == nil {
		return -1, nil
	}
	kinds := aggregationKinds(inst, nil)
	for i, a := range r.Aggregations {
		if a != nil && kinds.IsArrayValued(a.Type) {
			return i, a
		}
	}
	return -1, nil
}

// scoredArrayValuedAggregation returns the index and slot of the
// aggregation an overlay scores on r when it is array-valued, else -1,
// nil. scored resolves that aggregation's index (params.field, else 0 —
// the narrowing's own reading); a resolution error is left to the field
// rule (PULSE_OVERLAY_FIELD_UNKNOWN), which owns it. A crosstab request
// is never narrowed, so every aggregation counts there.
func scoredArrayValuedAggregation(r *types.Request, inst *InstanceSnapshot, scored func([]*types.Aggregation) (int, error)) (int, *types.Aggregation) {
	if r == nil {
		return -1, nil
	}
	if r.Crosstab != nil {
		return firstArrayValuedAggregation(r, inst)
	}
	ai, err := scored(r.Aggregations)
	if err != nil || ai < 0 || ai >= len(r.Aggregations) {
		return -1, nil
	}
	if a := r.Aggregations[ai]; a != nil && aggregationKinds(inst, nil).IsArrayValued(a.Type) {
		return ai, a
	}
	return -1, nil
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
		ai, agg := scoredArrayValuedAggregation(r, inst, func(aggs []*types.Aggregation) (int, error) {
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
				") is array-valued (one figure per vector member; expanded columns are presentation only); an overlay folds one number per host row — compare a scalar aggregation instead",
			map[string]any{"overlay_index": i, "kind": kind, "slot": labels[j], "host": host,
				"aggregation": string(agg.Type), "label": label, "reason": "array_valued"})
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
		ai, agg := scoredArrayValuedAggregation(req.Stages[idx].Request, inst, func(aggs []*types.Aggregation) (int, error) {
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
				") is array-valued (one figure per vector member; expanded columns are presentation only); an overlay folds one number per host row — compare a scalar aggregation instead",
			map[string]any{"overlay_index": i, "kind": string(spec.Kind), "role": side.role, "stage_index": idx,
				"stage_name": req.Stages[idx].Name, "aggregation_index": ai, "aggregation": string(agg.Type),
				"label": label, "reason": "array_valued"})
	}
	return nil
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
