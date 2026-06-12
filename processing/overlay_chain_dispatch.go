package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// CHAIN-host overlay fold engine — runtime side of the whole-chain
// overlay catalog.
//
// E6-S3 scope: structural pre-req for the two whole-chain overlay kinds
// the E6 epic ships (`OVERLAY_INDEX_VS_STAGE` / E6-S4,
// `OVERLAY_DELTA_VS_STAGE` / E6-S5). The MATRIX-host overlay path
// (`overlay.go`) folds against a crosstab; the SERIES-host overlay path
// (`overlay_series.go`) folds against a grouped Process result; the
// FACET-host overlay path (`overlay_facet_dispatch.go`) folds against a
// finalised `*types.FacetResult`. This file is its parallel for a CHAIN
// host — an already-finalised list of `*types.Response` objects, one
// per `ChainStage`, with an optional `Name → Index` lookup for
// resolving `StageRef.Name` references.
//
// What this file lands (per the E6-S3 acceptance criteria):
//
//   - chainOverlayHandler is the per-kind execution signature for the
//     CHAIN host path. Handlers receive the spec, the target stage's
//     response (resolved via Target), the reference stage's response
//     (resolved via Ref), and the same index pair so warnings can
//     reference the originating stage by index.
//
//   - chainOverlayHandlers is the per-kind dispatch table. E6-S3 lands
//     stub handlers for `OVERLAY_INDEX_VS_STAGE` and
//     `OVERLAY_DELTA_VS_STAGE`; E6-S4 / E6-S5 replace the stubs with
//     real arithmetic.
//
//   - ApplyChainOverlays walks the spec list in matching order,
//     resolves each spec's Target + Ref via the StageRef resolver,
//     dispatches each via chainOverlayHandlers, and returns one
//     OverlayLayer per spec in matching order plus a flat warnings
//     slice. Unknown kinds short-circuit with the same
//     PROCESSING_INTERNAL + PULSE_OVERLAY_KIND_UNKNOWN details shape
//     the MATRIX / SERIES / FACET paths emit.
//
// Service-side wiring (per the E6-S3 acceptance "ChainResponse.Overlays
// populated after the stage loop finishes and BEFORE the response is
// returned"): the `service.ProcessChain` orchestrator calls
// `ApplyChainOverlays` at the post-stage-loop barrier — once every
// stage has produced a finalised `*Response` the whole-chain fold runs
// over the already-materialised stage responses. The fold operates
// EXCLUSIVELY on `*Response` objects; no record re-traversal. Per-stage
// `Response.Overlays` (populated via the per-stage `Request.Overlays`
// slot per E6-S1) is left UNTOUCHED by this barrier.
//
// Streamability discipline: every whole-chain kind is BUFFERED by
// construction — the barrier runs after every stage has finalised, so
// there is no streamable arm for the whole-chain kind family. The
// dispatch entry does NOT inspect `types.OverlayStreamability` because
// the buffered guarantee is structural, not policy-driven.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution stays inside processing/ alongside overlay.go
//     and overlay_series.go.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban (CLAUDE.md "Output
//     Format Contract").

// chainOverlayHandler is the per-kind execution signature for the
// CHAIN host path. Handlers receive the request spec, the target
// stage's *Response (resolved via spec.Target), the reference stage's
// *Response (resolved via spec.Ref), and the (targetIdx, refIdx) pair
// so warnings can reference the originating stage by index. They
// return a fully-shaped OverlayLayer (Name + Kind + Scope + Payload +
// optional Summary) plus a slice of warnings for any per-coordinate
// degeneracies (zero reference value, shape divergence).
//
// Signature parity with the MATRIX (overlayHandler) / SERIES
// (seriesOverlayHandler) / FACET (facetOverlayHandler) paths is
// intentional — the host parameters change but the
// `(OverlayLayer, []OverlayWarning, error)` return shape stays
// uniform so the orchestrator-side promotion logic looks the same on
// every host.
type chainOverlayHandler func(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []OverlayWarning, error)

// chainOverlayHandlers is the per-kind dispatch table for the CHAIN
// host path. E6-S3 ships compile-time stub handlers for both
// whole-chain kinds — the chassis demonstrates end-to-end wiring
// (Layer.Kind matches spec.Kind, spec-order preservation, per-stage
// Overlays untouched) without per-kind arithmetic. E6-S4 / E6-S5
// replace the stubs with real INDEX / DELTA math.
//
// Tests register synthetic handlers by writing into this map and using
// `t.Cleanup` to restore the prior state — the test stub pattern
// documented alongside processing/overlay_series.go's analogous table.
// The dispatch is intentionally not hidden behind a registration
// function: keeping the table as a plain package-level map lets the
// test infra swap entries without touching production-only code paths.
//
// Adding a new CHAIN OverlayKind: declare the constant + streamability
// row (types/overlay.go + types/overlay_streamability.go), add the
// runtime handler in this package, and add the dispatch entry here.
var chainOverlayHandlers = map[types.OverlayKind]chainOverlayHandler{
	// OVERLAY_DELTA_VS_STAGE (E6-S5 will land the real handler): stub
	// emits a zero-payload OverlayLayer whose shape inherits from the
	// target stage's host so the round-trip test sees a one-to-one
	// mapping between specs and layers. The stub is byte-equivalent at
	// the per-layer envelope level; the underlying arithmetic ships
	// with the E6-S5 follow-up.
	types.OverlayKindDeltaVsStage: applyChainStub,
	// OVERLAY_INDEX_VS_STAGE (E6-S4 will land the real handler): same
	// stub shape as DELTA_VS_STAGE. The chassis verifies the dispatcher
	// resolves Target + Ref via the StageRef resolver, dispatches
	// through the handler table, and surfaces one OverlayLayer per
	// spec in matching order. The stub is byte-equivalent at the
	// per-layer envelope level; the underlying arithmetic ships with
	// the E6-S4 follow-up.
	types.OverlayKindIndexVsStage: applyChainStub,
}

// ApplyChainOverlays executes every spec in specs against the chain
// of finalised stage responses and returns one OverlayLayer per spec
// in matching order (FR-A2: stable spec-order layer emission), plus a
// flat warning slice. Returns (nil, nil, nil) when specs is empty so
// the service-side ProcessChain post-stage-loop call site can call
// ApplyChainOverlays unconditionally — mirrors the MATRIX
// ApplyOverlays / SERIES ApplyOverlaysSeries / FACET ApplyOverlaysFacet
// short-circuit contract.
//
// `stages` is the ordered list of per-stage *Response objects in the
// same order as the originating `ChainRequest.Stages` slice;
// `stageNames` is the parallel ordered list of `ChainStage.Name`
// values (empty string where the stage had no Name) — the resolver
// uses it to build the `Name → Index` lookup table for `StageRef.Name`
// resolution.
//
// Resolution policy:
//
//   - StageRef.Index (non-nil) resolves directly; out-of-range fires a
//     coded error (PULSE_OVERLAY_REF_UNKNOWN until the E6 catalog adds
//     PULSE_OVERLAY_TARGET_UNKNOWN / PULSE_OVERLAY_REFERENCE_UNKNOWN).
//   - StageRef.Name (non-empty) resolves against the namedIndex
//     lookup; absent name fires the same coded error.
//   - Target with both slots empty defaults to `len(stages) - 1` (the
//     latest stage). Ref with both slots empty fires the coded error
//     immediately — every spec MUST name a baseline.
//
// Defense in depth: the descriptor.ValidateOverlays gate (E6-S7) will
// reject bad kinds at predict time, so a missing dispatch entry should
// never reach the runtime in practice; nonetheless ApplyChainOverlays
// guards against an unknown kind and returns a CodedError whose
// details carry errors.PULSE_OVERLAY_KIND_UNKNOWN so the orchestrator
// surfaces the same failure mode predict would have flagged.
//
// FIXME E6-S6: surface PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT here
// when the resolved target stage and reference stage host shapes
// diverge. Until the error code lands the stub handler skips the
// divergence check (no warning emitted; the layer inherits the target
// stage's shape).
func ApplyChainOverlays(specs []*types.ChainOverlaySpec, stages []*types.Response, stageNames []string) ([]*types.OverlayLayer, []OverlayWarning, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	// Build the Name → Index lookup once per barrier entry. Empty
	// names do NOT participate (the resolver only consults this table
	// for non-empty StageRef.Name references); duplicate names follow
	// last-write-wins which is benign because the ChainStage.Name
	// uniqueness contract is a caller-side discipline (the chain
	// executor does not enforce it).
	namedIndex := make(map[string]int, len(stageNames))
	for i, name := range stageNames {
		if name == "" {
			continue
		}
		namedIndex[name] = i
	}
	layers := make([]*types.OverlayLayer, 0, len(specs))
	var warnings []OverlayWarning
	for i, spec := range specs {
		if spec == nil {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"whole-chain overlay spec is nil",
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
					"index": i,
				})
		}
		handler, ok := chainOverlayHandlers[spec.Kind]
		if !ok {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay kind has no CHAIN runtime handler: "+string(spec.Kind),
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
					"index": i,
					"kind":  string(spec.Kind),
					"host":  "chain",
				})
		}
		// Target defaults to latest stage when both slots are unset
		// per the research doc convention; Ref has no default. Both
		// resolvers surface the same coded error shape on failure.
		targetIdx, err := resolveChainStageRef(spec.Target, stages, namedIndex, true, i)
		if err != nil {
			return nil, nil, err
		}
		refIdx, err := resolveChainStageRef(spec.Ref, stages, namedIndex, false, i)
		if err != nil {
			return nil, nil, err
		}
		layer, ws, err := handler(spec, stages[targetIdx], stages[refIdx], targetIdx, refIdx)
		if err != nil {
			return nil, nil, err
		}
		// Append a copy so the caller-visible slice is decoupled from
		// the handler's stack frame.
		l := layer
		layers = append(layers, &l)
		if len(ws) > 0 {
			warnings = append(warnings, ws...)
		}
	}
	return layers, warnings, nil
}

// resolveChainStageRef resolves a StageRef into a `stages` slice
// index. Returns a coded error on out-of-range Index or unknown Name.
// `targetDefault` controls the "both slots empty" semantics: a Target
// reference defaults to the latest stage (`len(stages) - 1`), a Ref
// reference is rejected.
//
// FIXME E6-S3: the canonical codes for this surface are
// `PULSE_OVERLAY_TARGET_UNKNOWN` (Target arm) and
// `PULSE_OVERLAY_REFERENCE_UNKNOWN` (Ref arm). Neither code is
// declared yet (E1 was supposed to add them); the chassis falls back
// to `PULSE_OVERLAY_REF_UNKNOWN` for both arms with a `which` Detail
// distinguishing the arm so callers can still branch. The fallback
// will flip when the canonical codes land in a follow-up.
func resolveChainStageRef(ref types.StageRef, stages []*types.Response, namedIndex map[string]int, targetDefault bool, specIdx int) (int, error) {
	whichArm := "ref"
	if targetDefault {
		whichArm = "target"
	}
	// Empty Target defaults to the latest stage; empty Ref is an error.
	if ref.Index == nil && ref.Name == "" {
		if targetDefault {
			if len(stages) == 0 {
				return 0, errors.NewCodedErrorWithDetails(
					errors.PROCESSING_INTERNAL,
					"chain overlay has no stages to resolve default Target against",
					map[string]any{
						"code":  string(errors.PULSE_OVERLAY_REF_UNKNOWN),
						"index": specIdx,
						"which": whichArm,
					})
			}
			return len(stages) - 1, nil
		}
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"chain overlay Ref must populate exactly one of Index / Name",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_REF_UNKNOWN),
				"index": specIdx,
				"which": whichArm,
			})
	}
	// Index takes precedence when populated. Out-of-range Index fires
	// the unknown-stage coded error. Negative Index is rejected the
	// same way (the StageRef contract documents `Index = &zero` as the
	// canonical "stage 0" call site).
	if ref.Index != nil {
		idx := *ref.Index
		if idx < 0 || idx >= len(stages) {
			return 0, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"chain overlay stage Index out of range",
				map[string]any{
					"code":         string(errors.PULSE_OVERLAY_REF_UNKNOWN),
					"index":        specIdx,
					"which":        whichArm,
					"stage_index":  idx,
					"stages_count": len(stages),
				})
		}
		return idx, nil
	}
	// Name resolves against the precomputed namedIndex lookup.
	idx, ok := namedIndex[ref.Name]
	if !ok {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"chain overlay stage Name not found",
			map[string]any{
				"code":       string(errors.PULSE_OVERLAY_REF_UNKNOWN),
				"index":      specIdx,
				"which":      whichArm,
				"stage_name": ref.Name,
			})
	}
	return idx, nil
}

// applyChainStub is the compile-time stub handler shared by both
// E6-S3 whole-chain kinds. Each invocation emits a single
// OverlayLayer carrying:
//
//   - Name  — the spec's Name when set, else "OVERLAY_{KIND}" so the
//             renderer-facing label is always populated.
//   - Kind  — the spec's Kind, echoed verbatim.
//   - Scope — the spec's Scope, echoed verbatim (validator enforces
//             scope policy at predict / runtime entry; the stub trusts
//             the caller).
//   - Payload — a zero-value payload whose Shape inherits from the
//             target stage's host shape: matrix when the target stage
//             carries a Crosstab matrix; series when it carries grouped
//             Process Data; scalar otherwise. The stub does NOT
//             populate the payload's per-coordinate value slots — the
//             real arithmetic ships with E6-S4 / E6-S5.
//   - Summary — nil (descriptive summary slots are kind-specific and
//             land with the real handlers).
//
// The stub emits no warnings and no error. The chassis test exercises
// the round-trip: spec count == layer count, layer.Kind matches
// spec.Kind, per-stage Stages[i].Overlays untouched. Real arithmetic
// (per-coordinate target / ref folding, zero-denominator handling,
// shape-divergence detection) ships with the per-kind handlers in
// E6-S4 (INDEX_VS_STAGE) and E6-S5 (DELTA_VS_STAGE).
func applyChainStub(spec *types.ChainOverlaySpec, target, ref *types.Response, targetIdx, refIdx int) (types.OverlayLayer, []OverlayWarning, error) {
	name := spec.Name
	if name == "" {
		name = string(spec.Kind)
	}
	// Inherit the target stage's host shape so the layer Shape echoes
	// the visualisation surface the renderer expects. Targets without
	// a Crosstab payload AND without Data rows fall through to scalar.
	shape := inferChainStubShape(target)
	return types.OverlayLayer{
		Name:  name,
		Kind:  spec.Kind,
		Scope: spec.Scope,
		// Ref slot on the response side is the OverlayRef union, NOT
		// the ChainOverlaySpec's StageRef pair (the spec's Ref lives
		// on a different struct). The stub leaves Ref empty — the
		// per-kind validator + real handler in E6-S4 / E6-S5 will
		// surface the resolved stage indices through a kind-specific
		// summary slot when the math lands.
		Payload: types.OverlayPayload{
			Shape: shape,
		},
	}, nil, nil
}

// inferChainStubShape picks an OverlayShape for the stub layer based
// on the target stage's host result shape. Reads only the top-level
// Crosstab / Data slots so it never touches per-record state.
//
// Precedence:
//
//   - Target carries a non-nil Crosstab matrix ⇒ OverlayShapeMatrix.
//   - Target carries non-empty Data ⇒ OverlayShapeSeries (the canonical
//     grouped Process shape).
//   - Otherwise (scalar facet result OR empty Data) ⇒ OverlayShapeScalar.
//
// The real handlers in E6-S4 / E6-S5 inherit the same precedence
// rule; codifying it here keeps the chassis byte-identical to the
// shipping shape selection logic.
func inferChainStubShape(target *types.Response) types.OverlayShape {
	if target == nil {
		return types.OverlayShapeScalar
	}
	if target.Crosstab != nil && target.Crosstab.Matrix != nil {
		return types.OverlayShapeMatrix
	}
	if len(target.Data) > 0 {
		return types.OverlayShapeSeries
	}
	return types.OverlayShapeScalar
}
