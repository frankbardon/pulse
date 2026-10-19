package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Whole-chain overlay predict-time validator.
//
// Extends ValidateChainWithOptions (internal/descriptor/chain.go) with an Overlays walk that
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
//     resolveChainStageRef contract in internal/processing/overlay_chain_dispatch.go).
//     Ref has no default — empty Ref fires PULSE_OVERLAY_REFERENCE_UNKNOWN.
//     Out-of-range Index or unknown Name fires
//     PULSE_OVERLAY_REFERENCE_UNKNOWN / PULSE_OVERLAY_TARGET_UNKNOWN per arm.
//
//   - Shape divergence: when inferChainStageShape(stages[ref]) differs
//     from inferChainStageShape(stages[target]) the validator WARNS
//     PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT (warning-class, as the
//     runtime's empty layer + warning; the chain stays valid) and
//     appends the (ref, target) pair to
//     ChainValidationResult.OverlaysSchemaDivergence per PRD §I-FR-I3.
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
// The chain gate (mergegate.ChainRefusal) already rejects req == nil and
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
// validation errors, shape-divergence warnings and divergence pairs
// onto env / result. Called from
// ValidateChainWithOptions in internal/descriptor/chain.go after the per-stage gate loop
// finishes; no-op when req.Overlays is empty.
//
// The validator runs even if the per-stage gate already added stage-
// level errors so the LLM caller sees the whole-chain failures
// alongside the per-stage ones in a single envelope (mirrors how
// Predict accumulates errors across overlay / field / streamability
// gates).
func validateChainOverlays(env *descriptor.Envelope, result *ChainValidationResult, req *types.ChainRequest, staged []*types.Request, stage0 *encoding.Schema, opts *PredictOptions) {
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
		// OVERLAY_DELTA_VS_STAGE accepted in ChainOverlaySpec.Kind. A
		// kind the instance hides keys on its route, as a kind not in
		// the catalog.
		// A catalog kind that runs on another slot gets the shared
		// overlayhost refusal the runtime chain dispatcher raises; only
		// a kind outside the catalog stays PULSE_OVERLAY_KIND_UNKNOWN.
		if cerr := overlayhost.Refusal(overlayhost.Chain, spec.Kind, opts.overlayRoute(spec.Kind), i); cerr != nil {
			addCodedError(env, cerr)
			continue
		}
		if !chainOverlayKindAllowed(opts.overlayRoute(spec.Kind)) {
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
		// An array-valued (vector) or non-scalar scored aggregation on
		// either stage (PROCESSING_CONFIG, reason array_valued /
		// non_scalar) — the runtime's check in Service.processChain,
		// before any stage runs — or a categorical-code one, judged
		// against the stage's input schema (stage0, else synthesised
		// from the defaulted stage before it): the runtime's second
		// call, once every stage ran.
		if aerr := chainOverlayArrayValuedRefusal(req, i, spec, opts.instance(), staged, stage0); aerr != nil {
			addCodedError(env, aerr)
			continue
		}
		// The SERIES value rule (U46): a non-crosstab stage is scored on
		// the aggregation params.field names (else Aggregations[0]), the
		// runtime's processing.projectChainSeriesStages — target first,
		// then ref. A stage whose untyped, unlabelled aggregation still
		// has no Type after defaults (DisableDefaults, an unknown field)
		// is left to the runtime, which refuses it first.
		// Labels resolve on the stage as executed (stagedAt: smart
		// defaults applied against the stage's input schema, the joined
		// one for a joined stage 0).
		// An expanded element column resolves against the stage's
		// members over its input schema (chainStageInputSchema).
		kinds := aggregationKinds(opts.instance(), nil)
		// One error: the runtime resolves the target first and stops
		// at its refusal (#436).
		targetOK := validateChainOverlaySeriesField(env, spec, opts.overlayRoute(spec.Kind), i, "target", targetIdx, stagedAt(staged, targetIdx, targetReq),
			chainStageSchemaFunc(staged, targetIdx, stage0, kinds), kinds)
		if targetOK && refIdx != targetIdx {
			validateChainOverlaySeriesField(env, spec, opts.overlayRoute(spec.Kind), i, "ref", refIdx, stagedAt(staged, refIdx, refReq),
				chainStageSchemaFunc(staged, refIdx, stage0, kinds), kinds)
		}
		refShape := inferChainStageShape(refReq)
		targetShape := inferChainStageShape(targetReq)
		if refShape == targetShape {
			continue
		}
		// Warning-class, as at runtime (U46 E4-S9): the runtime answers
		// an empty layer plus one warning and the chain still runs, so
		// predict reports the divergence as an envelope WARNING and the
		// chain stays valid. Predict's Strict promotes it, as it does
		// every predict-root warning (finishPredictRoot, U35 #255); the
		// runtime never promotes an overlay layer warning. Details carry every key
		// the runtime warning does (overlay_index included), so the two
		// deep-equal.
		env.AddWarning(string(errors.PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT),
			"chain overlay reference and target stages produce different host shapes; the runtime surfaces an empty layer",
			map[string]any{
				"index":         i,
				"overlay_index": i,
				"kind":          string(spec.Kind),
				"ref_index":     refIdx,
				"ref_shape":     string(refShape),
				"target_index":  targetIdx,
				"target_shape":  string(targetShape),
				"stage_index":   targetIdx,
				"stage_name":    stageNameAt(req, targetIdx),
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

// stagedAt is stage idx as the runtime executes it (staged[idx], its
// defaults resolved), else the stage as written.
func stagedAt(staged []*types.Request, idx int, written *types.Request) *types.Request {
	if idx >= 0 && idx < len(staged) && staged[idx] != nil {
		return staged[idx]
	}
	return written
}

// validateChainOverlaySeriesField refuses a params.field that names no
// aggregation of one stage, PULSE_OVERLAY_FIELD_UNKNOWN via the
// seriesfield.ResolveChainStage call the runtime makes on the same
// stage Request, its columns read over schema (the stage's input
// schema; nil: an element column resolves syntactically). It reports
// whether the stage passed (true when the rule does not apply).
func validateChainOverlaySeriesField(env *descriptor.Envelope, spec *types.ChainOverlaySpec, route types.OverlayKind, specIdx int, which string, stageIdx int, req *types.Request, schema func() *encoding.Schema, kinds vectors.AggregationKinds) bool {
	if !seriesfield.AppliesChain(route) || req == nil || req.Crosstab != nil || requestLabelsDeferred(req) {
		return true
	}
	if _, err := seriesfield.ResolveChainStage(req.Aggregations, spec, specIdx, which, stageIdx, vectors.AggregationColumns(req, schema, kinds)); err != nil {
		addCodedError(env, err)
		return false
	}
	return true
}

// resolveChainOverlayStageRef resolves a StageRef into a stages-slice
// index through overlayhost.ResolveStageRef — the rule the runtime
// resolveChainStageRef (internal/processing/overlay_chain_dispatch.go)
// calls, so both refuse with one code, message and details (Index and
// Name both set, neither set on a Ref, an out-of-range Index, an
// unknown Name; an unset Target defaults to the last stage).
//
// The returned bool is true iff resolution succeeded; the int is the
// resolved stage index when ok, undefined otherwise.
func resolveChainOverlayStageRef(env *descriptor.Envelope, ref types.StageRef, namedIndex map[string]int, stagesCount int, specIdx int, targetDefault bool) (int, bool) {
	// The shared rule the runtime chain dispatcher resolves with.
	idx, cerr := overlayhost.ResolveStageRef(ref, stagesCount, namedIndex, targetDefault, specIdx)
	if cerr != nil {
		addCodedError(env, cerr)
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
