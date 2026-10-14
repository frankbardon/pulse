package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The slot-reference rules of the two multi-result hosts, shared by
// predict and runtime so both raise one code, message and details:
// ComposeSlotRefusals (ComposedRequest.Overlays' Reference / Targets
// labels — predict-compose's gates 1 and 2 and the runtime Compose
// dispatcher) and ResolveStageRef (ChainOverlaySpec.Ref / Target —
// predict-chain's whole-chain overlay walk and the runtime chain
// dispatcher). Before them the two sides worded and detailed the
// refusals differently, predict accepted a Compose spec with no Targets
// the runtime refused, and the runtime ran a StageRef carrying both
// Index and Name that predict refused.

// ComposeSlotRefusals returns the label refusals of the Compose-host
// spec at index, in order: a Reference that is empty or names no slot
// (known reports a slot label) → PULSE_OVERLAY_REFERENCE_UNKNOWN
// {index, kind, which: "reference"[, slot_label]} alone; else no
// Targets → PULSE_OVERLAY_TARGET_UNKNOWN {index, kind, which: "targets"}
// alone; else one PULSE_OVERLAY_TARGET_UNKNOWN {index, kind,
// target_index, which: "target"[, slot_label]} per empty or unknown
// target. The runtime raises the first; predict reports them all. nil
// when every label resolves.
func ComposeSlotRefusals(spec *types.ComposeOverlaySpec, index int, known func(label string) bool) []*errors.CodedError {
	if spec == nil {
		return nil
	}
	kind := string(spec.Kind)
	if spec.Reference == "" {
		return []*errors.CodedError{errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN,
			"compose overlay reference label is empty",
			map[string]any{"index": index, "kind": kind, "which": "reference"})}
	}
	if !known(spec.Reference) {
		return []*errors.CodedError{errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN,
			"compose overlay reference label not found: "+spec.Reference,
			map[string]any{"index": index, "kind": kind, "which": "reference", "slot_label": spec.Reference})}
	}
	if len(spec.Targets) == 0 {
		return []*errors.CodedError{errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_TARGET_UNKNOWN,
			"compose overlay spec must declare at least one target slot label",
			map[string]any{"index": index, "kind": kind, "which": "targets"})}
	}
	var out []*errors.CodedError
	for j, label := range spec.Targets {
		switch {
		case label == "":
			out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_TARGET_UNKNOWN,
				"compose overlay target label is empty",
				map[string]any{"index": index, "kind": kind, "target_index": j, "which": "target"}))
		case !known(label):
			out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_TARGET_UNKNOWN,
				"compose overlay target label not found: "+label,
				map[string]any{"index": index, "kind": kind, "target_index": j, "which": "target", "slot_label": label}))
		}
	}
	return out
}

// ResolveStageRef resolves a whole-chain overlay StageRef (Ref, or
// Target when target is set) against count stages and the stage-name
// table named (empty names absent; last write wins) for the spec at
// index. Code PULSE_OVERLAY_TARGET_UNKNOWN for a Target,
// PULSE_OVERLAY_REFERENCE_UNKNOWN for a Ref; details {index, which}
// plus the named extras. Judged in order:
//
//   - Index and Name both set → refused;
//   - neither set → a Target defaults to the last stage (refused when
//     there is none); a Ref is refused;
//   - Index outside [0, count) → refused {…, stage_index, stages_count};
//   - Name not in named → refused {…, stage_name}.
func ResolveStageRef(ref types.StageRef, count int, named map[string]int, target bool, index int) (int, *errors.CodedError) {
	which, code := "ref", errors.PULSE_OVERLAY_REFERENCE_UNKNOWN
	if target {
		which, code = "target", errors.PULSE_OVERLAY_TARGET_UNKNOWN
	}
	refuse := func(msg string, extra ...any) (int, *errors.CodedError) {
		d := map[string]any{"index": index, "which": which}
		for i := 0; i+1 < len(extra); i += 2 {
			d[extra[i].(string)] = extra[i+1]
		}
		return 0, errors.NewCodedErrorWithDetails(code, msg, d)
	}
	if ref.Index != nil && ref.Name != "" {
		return refuse("chain overlay StageRef must populate exactly one of Index / Name")
	}
	if ref.Index == nil && ref.Name == "" {
		if !target {
			return refuse("chain overlay Ref must populate exactly one of Index / Name")
		}
		if count == 0 {
			return refuse("chain overlay has no stages to resolve default Target against")
		}
		return count - 1, nil
	}
	if ref.Index != nil {
		if idx := *ref.Index; idx < 0 || idx >= count {
			return refuse("chain overlay stage Index out of range", "stage_index", idx, "stages_count", count)
		}
		return *ref.Index, nil
	}
	idx, ok := named[ref.Name]
	if !ok {
		return refuse("chain overlay stage Name not found", "stage_name", ref.Name)
	}
	return idx, nil
}
