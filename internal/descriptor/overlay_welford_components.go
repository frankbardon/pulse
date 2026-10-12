package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/types"
)

// ComposeOverlayWelfordComponentsRefusal refuses a Compose overlay
// that scores a slot's AGG_WELFORD through its payload
// (overlayReadsPayload: OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF) on a
// grouped, non-crosstab slot whose components are disabled —
// disabled(slot) is the runtime's effective gate
// (EffectiveDisableComponents: the request's disable_components, else
// Options.DisableComponents; a `return` never removes them, the
// Compose veto keeps every sub-part of a slot an overlay names). The
// series arm reads each row's {mean, variance, m2, n} (+ the weighted
// floor) from that slot's per-group Components
// (processing.narrowSeriesHost); without them the row value is the
// in-process moment struct, which no handler reads, and the layer came
// back EMPTY with no error (U46 E4-S13). The slots read and the scored
// aggregation resolve exactly as ComposeOverlayArrayValuedRefusal's
// (reference + targets, every slot without targets; params.field,
// else Aggregations[0]).
//
// PULSE_OVERLAY_COMPONENTS_REQUIRED, Details {overlay_index, kind,
// slot, host, aggregation, label, reason: "components_disabled"} — the
// scored-aggregation refusal's coordinates with the hidden-floor
// rule's reason. The runtime (Service.applyComposeOverlays) and
// ValidateComposeWithOptions call it with the raw slots; the runtime
// re-runs it at fold time against the components each slot actually
// returned (an engine invariant, PROCESSING_INTERNAL). A kind the
// instance hides is left to the kind rule.
func ComposeOverlayWelfordComponentsRefusal(overlays []types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot, disabled func(*types.Request) bool) error {
	for i := range overlays {
		if err := composeOverlayWelfordComponentsRefusal(i, &overlays[i], requests, labels, inst, disabled); err != nil {
			return err
		}
	}
	return nil
}

func composeOverlayWelfordComponentsRefusal(i int, spec *types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot, disabled func(*types.Request) bool) *errors.CodedError {
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
	kinds := aggregationKinds(inst, nil)
	for j, r := range requests {
		if r == nil || r.Crosstab != nil || j >= len(labels) || !reads(labels[j]) || !disabled(r) {
			continue
		}
		ai := 0
		if seriesfield.AppliesCompose(spec.Kind) {
			idx, err := seriesfield.ResolveComposeSlot(r.Aggregations, spec, i, labels[j])
			if err != nil {
				continue // PULSE_OVERLAY_FIELD_UNKNOWN owns it
			}
			ai = idx
		}
		if ai < 0 || ai >= len(r.Aggregations) || r.Aggregations[ai] == nil {
			continue
		}
		agg := r.Aggregations[ai]
		if !kinds.IsNonScalar(agg.Type) || !overlayReadsPayload(spec.Kind, kinds.Routed(agg.Type)) {
			continue
		}
		label := types.AggregationLabelOf(agg)
		host := "requests[" + strconv.Itoa(j) + "].aggregations[" + strconv.Itoa(ai) + "]"
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_COMPONENTS_REQUIRED,
			"overlays["+strconv.Itoa(i)+"] ("+kind+") reads slot "+strconv.Quote(labels[j])+"'s "+host+" "+label+" ("+string(agg.Type)+
				") from its per-group components (mean, variance, n and the weighted floor), and that slot was built with components disabled, "+
				"so the layer would be empty; enable components on that slot (drop disable_components / Options.DisableComponents) "+
				"or compare a scalar mean with the params triple",
			map[string]any{"overlay_index": i, "kind": kind, "slot": labels[j], "host": host,
				"aggregation": string(agg.Type), "label": label, "reason": "components_disabled"})
	}
	return nil
}
