package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Array-valued overlay hosts — the Compose-host and whole-chain halves
// of the rule the field-reference walk applies to Request.Overlays. A
// vector aggregation (types.AggregationType.ArrayValued — AGG_VEC_MEAN /
// AGG_VEC_SUM) holds one figure per member, and under params.expand its
// per-element columns are presentation only. An overlay handler reads
// one number per host row (a SERIES host takes the row's first numeric
// column), so a slot or stage carrying one would fold an array as NaN —
// or, expanded, whichever element column sorts first — silently. Both
// rules refuse instead: PROCESSING_CONFIG, details.reason
// "array_valued". Predict (ValidateCompose / ValidateChain) and the
// runtime (Service.applyComposeOverlays / Service.processChain) call the
// same functions.

// firstArrayValuedAggregation returns the index and slot of r's first
// array-valued aggregation under the instance's routing, or -1, nil.
func firstArrayValuedAggregation(r *types.Request, inst *InstanceSnapshot) (int, *types.Aggregation) {
	if r == nil {
		return -1, nil
	}
	for i, a := range r.Aggregations {
		if a != nil && opRoute(inst, a.Type).ArrayValued() {
			return i, a
		}
	}
	return -1, nil
}

// ComposeOverlayArrayValuedRefusal refuses a Compose overlay that reads
// (as its reference, a target, or — no targets — every slot) a slot
// carrying an array-valued aggregation. labels are the slots' labels
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
		ai, agg := firstArrayValuedAggregation(r, inst)
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

// ChainOverlayArrayValuedRefusal refuses a whole-chain overlay whose
// target or reference stage carries an array-valued aggregation (only
// the last stage can: an array-valued stage may not feed another). A
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
		ai, agg := firstArrayValuedAggregation(req.Stages[idx].Request, inst)
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
