package descriptor

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E6-S7: whole-chain overlay predict-time validator.
//
// Extends ValidateChain (descriptor/chain.go) with an Overlays walk that
// runs AFTER the per-stage chain gate but BEFORE any stage executes.
// Three classes of failure surface here:
//
//   - Per-kind validity: only OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE
//     are accepted in ChainOverlaySpec.Kind. Anything else fires
//     PULSE_OVERLAY_KIND_UNKNOWN with the offending spec index + kind.
//
//   - StageRef resolution: Ref / Target must resolve to a known stage.
//     XOR is enforced on Index / Name; Target defaults to the latest
//     stage when both slots are nil/empty (mirroring the runtime
//     resolveChainStageRef contract in processing/overlay_chain_dispatch.go).
//     Ref has no default — empty Ref fires PULSE_OVERLAY_REFERENCE_UNKNOWN.
//     Out-of-range Index or unknown Name fires
//     PULSE_OVERLAY_REFERENCE_UNKNOWN / PULSE_OVERLAY_TARGET_UNKNOWN per arm.
//
//   - Shape divergence: when inferChainStageShape(stages[ref]) differs
//     from inferChainStageShape(stages[target]) the validator fires
//     PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT and appends the rejected
//     (ref, target) pair to ChainValidationResult.OverlaysSchemaDivergence
//     per PRD §I-FR-I3.
//
// Pure function of *types.ChainRequest — no record decode, no
// service / processing imports. Descriptor's no-execute contract holds.

// inferChainStageShape returns the OverlayShape a chain stage's
// *types.Request slot will produce after execution. Pure function — no
// service / processing imports; reads only top-level Request slots.
//
// Precedence mirrors the runtime processing.chainStageShape helper that
// resolves a *Response into the same OverlayShape:
//
//   - req.Crosstab != nil ⇒ OverlayShapeMatrix.
//   - len(req.Aggregations) > 0 && len(req.Groups) > 0 ⇒ OverlayShapeSeries.
//   - len(req.Aggregations) > 0 && len(req.Groups) == 0 ⇒ OverlayShapeScalar.
//   - otherwise (nil request or no aggregations) ⇒ OverlayShapeScalar
//     (matches the runtime helper's empty-Data fallthrough).
//
// The chain gate (chainGateOK) already rejects req == nil and
// len(req.Aggregations) == 0 before the overlay walk runs, so the
// fallthrough case is defensive — it keeps the helper total over every
// *types.Request value so test fixtures can exercise the shape table
// independently of the chain gate.
func inferChainStageShape(req *types.Request) types.OverlayShape {
	if req == nil {
		return types.OverlayShapeScalar
	}
	if req.Crosstab != nil {
		return types.OverlayShapeMatrix
	}
	if len(req.Aggregations) == 0 {
		return types.OverlayShapeScalar
	}
	if len(req.Groups) > 0 {
		return types.OverlayShapeSeries
	}
	return types.OverlayShapeScalar
}

// chainOverlayKindAllowed reports whether kind is one of the two
// whole-chain overlay kinds the catalog ships today. Per the kind
// catalog only OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE are
// accepted in ChainOverlaySpec.Kind; every other OverlayKind belongs to
// the MATRIX / SERIES / FACET host families and rides Request.Overlays
// (or the per-stage Stages[i].Request.Overlays slot) instead.
func chainOverlayKindAllowed(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindIndexVsStage, types.OverlayKindDeltaVsStage:
		return true
	}
	return false
}

// validateChainOverlays walks req.Overlays and appends per-spec
// validation errors / divergence pairs onto env / result. Called from
// ValidateChain in descriptor/chain.go after the per-stage gate loop
// finishes; no-op when req.Overlays is empty.
//
// The validator runs even if the per-stage gate already added stage-
// level errors so the LLM caller sees the whole-chain failures
// alongside the per-stage ones in a single envelope (mirrors how
// Predict accumulates errors across overlay / field / streamability
// gates).
func validateChainOverlays(env *Envelope, result *ChainValidationResult, req *types.ChainRequest) {
	if req == nil || len(req.Overlays) == 0 {
		return
	}
	// Build a Name → Index lookup for StageRef.Name resolution; mirrors
	// the runtime resolveChainStageRef table. Empty names do not
	// participate (the resolver only consults the table for non-empty
	// StageRef.Name references).
	namedIndex := make(map[string]int, len(req.Stages))
	for i, stage := range req.Stages {
		if stage == nil || stage.Name == "" {
			continue
		}
		namedIndex[stage.Name] = i
	}
	stagesCount := len(req.Stages)
	for i, spec := range req.Overlays {
		if spec == nil {
			env.AddError(string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
				"chain overlay spec is nil",
				map[string]any{"index": i})
			continue
		}
		// Per-kind validity check: only OVERLAY_INDEX_VS_STAGE /
		// OVERLAY_DELTA_VS_STAGE accepted in ChainOverlaySpec.Kind.
		if !chainOverlayKindAllowed(spec.Kind) {
			env.AddError(string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
				"chain overlay kind not supported on ChainRequest.Overlays: "+string(spec.Kind),
				map[string]any{
					"index": i,
					"kind":  string(spec.Kind),
				})
			continue
		}
		// Resolve Target (defaults to latest stage) and Ref (no default).
		targetIdx, ok := resolveChainOverlayStageRef(env, spec.Target, namedIndex, stagesCount, i, true)
		if !ok {
			continue
		}
		refIdx, ok := resolveChainOverlayStageRef(env, spec.Ref, namedIndex, stagesCount, i, false)
		if !ok {
			continue
		}
		// Shape inference + divergence detection. The handler runs
		// against finalised *Response objects at runtime, so a predict-
		// time divergence catches the failure before any stage executes.
		var refReq, targetReq *types.Request
		if refIdx >= 0 && refIdx < stagesCount && req.Stages[refIdx] != nil {
			refReq = req.Stages[refIdx].Request
		}
		if targetIdx >= 0 && targetIdx < stagesCount && req.Stages[targetIdx] != nil {
			targetReq = req.Stages[targetIdx].Request
		}
		refShape := inferChainStageShape(refReq)
		targetShape := inferChainStageShape(targetReq)
		if refShape == targetShape {
			continue
		}
		env.AddError(string(errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT),
			"chain overlay reference and target stages produce different host shapes",
			map[string]any{
				"index":        i,
				"kind":         string(spec.Kind),
				"ref_index":    refIdx,
				"ref_shape":    string(refShape),
				"target_index": targetIdx,
				"target_shape": string(targetShape),
				"stage_index":  targetIdx,
				"stage_name":   stageNameAt(req, targetIdx),
			})
		if result != nil {
			result.OverlaysSchemaDivergence = append(result.OverlaysSchemaDivergence,
				ChainOverlaySchemaDivergence{
					Index:       i,
					Kind:        spec.Kind,
					RefIndex:    refIdx,
					RefShape:    refShape,
					TargetIndex: targetIdx,
					TargetShape: targetShape,
				})
		}
	}
}

// resolveChainOverlayStageRef resolves a StageRef into a stages-slice
// index. Mirrors the runtime resolveChainStageRef contract
// (processing/overlay_chain_dispatch.go) at predict time:
//
//   - Index non-nil takes precedence; out-of-range fires the unknown-
//     stage coded error for the matching arm.
//   - Name non-empty resolves against namedIndex; absent name fires the
//     unknown-stage coded error.
//   - Both slots empty + targetDefault=true defaults to the latest
//     stage (len(stages) - 1). Both slots empty + targetDefault=false
//     (Ref arm) fires PULSE_OVERLAY_REFERENCE_UNKNOWN immediately.
//   - XOR enforcement: both Index AND Name populated is rejected with
//     the matching arm's unknown-stage code so the predict failure
//     mirrors what the runtime would have surfaced after a bad
//     round-trip.
//
// The returned bool is true iff resolution succeeded; the int is the
// resolved stage index when ok, undefined otherwise.
func resolveChainOverlayStageRef(env *Envelope, ref types.StageRef, namedIndex map[string]int, stagesCount int, specIdx int, targetDefault bool) (int, bool) {
	whichArm := "ref"
	armCode := errors.PULSE_OVERLAY_REFERENCE_UNKNOWN
	if targetDefault {
		whichArm = "target"
		armCode = errors.PULSE_OVERLAY_TARGET_UNKNOWN
	}
	// XOR check: both populated is malformed.
	if ref.Index != nil && ref.Name != "" {
		env.AddError(string(armCode),
			"chain overlay StageRef must populate exactly one of Index / Name",
			map[string]any{
				"index": specIdx,
				"which": whichArm,
			})
		return 0, false
	}
	// Both unset: Target defaults to latest stage; Ref is malformed.
	if ref.Index == nil && ref.Name == "" {
		if targetDefault {
			if stagesCount == 0 {
				env.AddError(string(armCode),
					"chain overlay has no stages to resolve default Target against",
					map[string]any{
						"index": specIdx,
						"which": whichArm,
					})
				return 0, false
			}
			return stagesCount - 1, true
		}
		env.AddError(string(armCode),
			"chain overlay Ref must populate exactly one of Index / Name",
			map[string]any{
				"index": specIdx,
				"which": whichArm,
			})
		return 0, false
	}
	// Index takes precedence when populated.
	if ref.Index != nil {
		idx := *ref.Index
		if idx < 0 || idx >= stagesCount {
			env.AddError(string(armCode),
				"chain overlay stage Index out of range",
				map[string]any{
					"index":        specIdx,
					"which":        whichArm,
					"stage_index":  idx,
					"stages_count": stagesCount,
				})
			return 0, false
		}
		return idx, true
	}
	// Name resolves against namedIndex.
	idx, ok := namedIndex[ref.Name]
	if !ok {
		env.AddError(string(armCode),
			"chain overlay stage Name not found",
			map[string]any{
				"index":      specIdx,
				"which":      whichArm,
				"stage_name": ref.Name,
			})
		return 0, false
	}
	return idx, true
}

// stageNameAt returns the optional ChainStage.Name at the given index
// for the divergence Details map. Returns empty string for an
// out-of-range index or a nil stage.
func stageNameAt(req *types.ChainRequest, idx int) string {
	if req == nil || idx < 0 || idx >= len(req.Stages) {
		return ""
	}
	stage := req.Stages[idx]
	if stage == nil {
		return ""
	}
	return stage.Name
}
