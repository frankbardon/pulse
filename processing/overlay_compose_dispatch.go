package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// COMPOSE-host overlay fold engine — runtime side of the Compose-only
// overlay catalog.
//
// E7-S4 scope: structural pre-req for the nine Compose-only overlay
// kinds the E7 epic ships (the per-kind handlers land in E7-S9..S12 and
// the multi-ref kinds in E7-S15+). The MATRIX-host overlay path
// (`overlay.go`) folds against a crosstab; the SERIES-host overlay
// path (`overlay_series.go`) folds against a grouped Process result;
// the FACET-host overlay path (`overlay_facet_dispatch.go`) folds
// against a finalised `*types.FacetResult`; the CHAIN-host overlay
// path (`overlay_chain_dispatch.go`) folds against a chain of stage
// responses. This file is its parallel for a COMPOSE host — an
// already-finalised list of per-slot `*types.Response` objects from a
// ComposedRequest, with an optional `Label → Index` lookup for
// resolving `ComposeOverlaySpec.Reference` and per-target labels.
//
// What this file lands (per the E7-S4 acceptance criteria):
//
//   - composeOverlayHandler is the per-kind execution signature for
//     the COMPOSE host path. Handlers receive the spec, the reference
//     slot's response (resolved via spec.Reference), the per-target
//     slot responses (resolved via spec.Targets), and the parallel
//     index pair so warnings can reference the originating slot by
//     index.
//
//   - composeOverlayHandlers is the per-kind dispatch table. E7-S4
//     ships an EMPTY dispatch table — the chassis falls through to a
//     compile-time stub handler (`applyComposeStub`) so the wiring
//     can be exercised end-to-end without per-kind arithmetic. E7-S9
//     onward replace the stub with real handlers as each Compose-only
//     kind lands.
//
//   - ApplyComposeOverlays walks the spec list in matching order,
//     resolves each spec's Reference + Targets via the Label resolver,
//     dispatches each via composeOverlayHandlers (falling back to the
//     stub when a kind is not yet registered), and returns one
//     OverlayLayer per spec in matching order plus a flat warnings
//     slice. Unknown kinds with no registered handler use the same
//     stub-layer fallback as the chassis tests exercise; once the
//     per-kind handlers register (E7-S9+) the stub fallback path is
//     reserved for genuinely unknown kinds (which fire a coded error).
//
// Service-side wiring (per the E7-S4 acceptance "post-slot-barrier
// overlay fold (CanFailFast aware)"): the `service.Compose` /
// `service.ComposeParallel` orchestrators call `ApplyComposeOverlays`
// at the post-slot-barrier — once every slot has produced a finalised
// `*Response` the cross-slot fold runs over the already-materialised
// responses. The fold operates EXCLUSIVELY on `*Response` objects; no
// record re-traversal. Per-slot `Responses[i].Overlays` (populated
// via the per-Request `Request.Overlays` slot) is left UNTOUCHED by
// this barrier.
//
// FailFast handling: when `ComposeOptions.FailFast = true` (default)
// and any slot fails, the orchestrator SKIPS the overlay fold
// entirely — no `ApplyComposeOverlays` call, no partial emission.
// FailFast=false runs the fold over the subset of slots that
// succeeded; the call site filters the responses slice to the present
// entries before dispatching.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution stays inside processing/ alongside overlay.go
//     and overlay_series.go.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays grep-
//     clean against the structural defense ban (CLAUDE.md "Output
//     Format Contract").

// composeOverlayHandler is the per-kind execution signature for the
// COMPOSE host path. Handlers receive the request spec, the reference
// slot's *Response (resolved via spec.Reference), the per-target slot
// *Response slice (resolved via spec.Targets — single-target kinds
// consume targets[0], multi-target kinds walk the full slice), and the
// parallel resolved-index slice so warnings can reference the
// originating slot by index. They return a fully-shaped OverlayLayer
// (Name + Kind + Scope + Payload + optional Summary) plus a slice of
// warnings for any per-slot degeneracies (zero reference value, shape
// divergence, missing target slot).
//
// Signature parity with the MATRIX (overlayHandler) / SERIES
// (seriesOverlayHandler) / FACET (facetOverlayHandler) / CHAIN
// (chainOverlayHandler) paths is intentional — the host parameters
// change but the `(OverlayLayer, []types.OverlayWarning, error)` return
// shape stays uniform so the orchestrator-side promotion logic looks
// the same on every host.
type composeOverlayHandler func(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []types.OverlayWarning, error)

// composeOverlayMultiLayerHandler is the per-kind execution signature
// for COMPOSE-only kinds that emit MULTIPLE OverlayLayer entries per
// ComposeOverlaySpec. Parallel surface to composeOverlayHandler — same
// resolution / gates / dispatch contract, but the return slot widens
// to `[]types.OverlayLayer` so a single spec can produce N layers in
// the parent ComposedResponse.Overlays slice.
//
// First consumer: OVERLAY_PANEL_INDEX_VS_REF (E7-S12) — the
// multi-reference descriptive twin of OVERLAY_PROP_Z_PANEL. The
// PANEL_INDEX_VS_REF authoring shape names N target slots against one
// shared reference slot and the chassis emits one OverlayLayer per
// target. Renderers consume the returned slice as a panel: every
// emitted layer shares the reference slot's coord space (matrix host)
// or group key set (series host) so the panel lays on one canvas.
//
// Dispatch precedence: the chassis checks the multi-layer table FIRST,
// then the single-layer table, then the stub fallback. Adding a kind
// to the multi-layer table opts that kind out of the single-layer
// path; the two tables are mutually exclusive per kind.
//
// Signature parity with composeOverlayHandler is intentional — every
// COMPOSE-only kind sees the same (spec, refResp, targetResps,
// refIdx, targetIdxs) input shape regardless of how many layers it
// emits. The chassis is the only place that branches on the table.
type composeOverlayMultiLayerHandler func(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) ([]types.OverlayLayer, []types.OverlayWarning, error)

// composeOverlayHandlers is the per-kind dispatch table for the
// COMPOSE host path. E7-S4 lands an EMPTY table — the chassis falls
// through to `applyComposeStub` for every kind so end-to-end wiring
// can be exercised without per-kind arithmetic. Subsequent E7 stories
// register real handlers as each Compose-only kind lands.
//
// Tests register synthetic handlers by writing into this map and using
// `t.Cleanup` to restore the prior state — the test stub pattern
// documented alongside processing/overlay_series.go's analogous table.
// The dispatch is intentionally not hidden behind a registration
// function: keeping the table as a plain package-level map lets the
// test infra swap entries without touching production-only code paths.
//
// Adding a new COMPOSE OverlayKind: declare the constant + streamability
// row (types/overlay.go + types/overlay_streamability.go), add the
// runtime handler in this package, and add the dispatch entry here.
var composeOverlayHandlers = map[types.OverlayKind]composeOverlayHandler{
	types.OverlayKindIndexVsRef: applyIndexVsRef,
	types.OverlayKindDeltaVsRef: applyDeltaVsRef,
	types.OverlayKindPropZCell:  applyPropZCell,
	types.OverlayKindPropZPanel: applyPropZPanel,
	types.OverlayKindTCell:      applyTCell,
	types.OverlayKindTVsRef:     applyTVsRef,
	types.OverlayKindZCell:      applyZCell,
	types.OverlayKindZVsRef:     applyZVsRef,
	types.OverlayKindChiSqVsRef: applyChiSqVsRef,
	types.OverlayKindRank:       applyRank,
}

// composeOverlayMultiLayerHandlers is the per-kind dispatch table for
// COMPOSE-only kinds that emit MULTIPLE OverlayLayer entries per spec.
// Mutually exclusive with composeOverlayHandlers — a kind appears in
// exactly one table. The chassis checks this table FIRST in the
// per-spec dispatch loop; on a hit it appends every emitted layer to
// the parent layers slice in spec order then target order.
//
// First entry: OVERLAY_PANEL_INDEX_VS_REF (E7-S12). The handler emits
// `len(spec.Targets)` layers, each one mathematically equivalent to
// the single-target OVERLAY_INDEX_VS_REF output for that
// (reference, target[i]) pair. Layer naming convention:
// `<spec.Name>__<target_label>` per the kind's catalog entry.
var composeOverlayMultiLayerHandlers = map[types.OverlayKind]composeOverlayMultiLayerHandler{
	types.OverlayKindPanelIndexVsRef: applyPanelIndexVsRef,
}

// ApplyComposeOverlays executes every spec in specs against the
// already-finalised per-slot responses and returns one OverlayLayer
// per spec in matching order, plus a flat warning slice. Returns
// (nil, nil, nil) when specs is empty so the service-side Compose /
// ComposeParallel post-slot-barrier call site can call
// ApplyComposeOverlays unconditionally — mirrors the MATRIX /
// SERIES / FACET / CHAIN short-circuit contract.
//
// `responses` is the ordered list of per-slot *Response objects in the
// same order as the originating `ComposedRequest.Requests` slice; nil
// entries correspond to slots that failed when `FailFast=false`.
// `labels` is the parallel ordered list of per-slot `Request.Label`
// values (after applyComposeLabelDefaults has filled empty slots with
// `request_<index+1>`) — the resolver uses it to build the
// `Label → Index` lookup table for `Reference` / `Targets` resolution.
//
// Resolution policy (E7-S5):
//
//   - Reference is REQUIRED on every spec. Empty Reference (or a
//     Reference that does not resolve to any label in `labels`) fires
//     a coded error carrying errors.PULSE_OVERLAY_REFERENCE_UNKNOWN
//     via processing.LookupReference.
//   - Targets is REQUIRED to have at least one entry. Empty Targets
//     fires PULSE_OVERLAY_TARGET_UNKNOWN.
//   - A target label that does not resolve to any label in `labels`
//     OR resolves to a nil slot (failed under FailFast=false) fires
//     PULSE_OVERLAY_TARGET_UNKNOWN via processing.LookupTarget.
//
// Defense in depth: the descriptor.ValidateComposedRequest gate
// (E7-S14 will lift it) will reject bad references / unknown kinds at
// predict time, so a missing dispatch entry should rarely reach the
// runtime in practice; nonetheless ApplyComposeOverlays guards
// against missing slot labels and returns a CodedError whose details
// carry the appropriate canonical code so the orchestrator surfaces
// the same failure mode predict would have flagged.
//
// Stub fallback: until the per-kind handlers register (E7-S9..S12 +
// E7-S15+), every spec routes through `applyComposeStub` which emits
// a zero-payload OverlayLayer that inherits the reference slot's host
// shape. The stub emits no warnings and no per-coordinate values;
// real arithmetic ships with each per-kind handler.
func ApplyComposeOverlays(specs []types.ComposeOverlaySpec, responses []*types.Response, labels []string) ([]types.OverlayLayer, []types.OverlayWarning, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	// Build the per-slot lookup map ONCE per barrier entry via the
	// canonical resolver. The chassis adapter builds a synthetic
	// *types.ComposedRequest from the parallel labels slice so the
	// resolver's pure surface stays the single source of truth for
	// slot-label → *Response binding (E7-S5).
	byLabel, byIndex, err := resolveComposeSlotsFromLabels(labels, responses)
	if err != nil {
		return nil, nil, err
	}
	layers := make([]types.OverlayLayer, 0, len(specs))
	var warnings []types.OverlayWarning
	for i := range specs {
		spec := &specs[i]
		// Reference must resolve to a known label AND a non-nil slot.
		// LookupReference owns the canonical-code dispatch for the
		// reference arm — empty label, unknown label, or nil slot all
		// surface PULSE_OVERLAY_REFERENCE_UNKNOWN.
		refResp, err := LookupReference(byLabel, spec.Reference, i)
		if err != nil {
			return nil, nil, err
		}
		refIdx := byIndex[spec.Reference]
		if len(spec.Targets) == 0 {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"compose overlay spec must declare at least one target slot label",
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_TARGET_UNKNOWN),
					"index": i,
					"kind":  string(spec.Kind),
					"which": "targets",
				})
		}
		targetIdxs := make([]int, 0, len(spec.Targets))
		targetResps := make([]*types.Response, 0, len(spec.Targets))
		for tIdx, label := range spec.Targets {
			tResp, err := LookupTarget(byLabel, label, i, tIdx)
			if err != nil {
				return nil, nil, err
			}
			targetIdxs = append(targetIdxs, byIndex[label])
			targetResps = append(targetResps, tResp)
		}
		// Strict cross-Request key-set alignment (E7-S6). Runs AFTER
		// reference + targets have resolved to concrete *Response
		// objects but BEFORE the per-kind handler dispatches — key-set
		// divergence is the cheapest signal to fail on so we surface it
		// before any per-kind handler walks the matrix / series payload
		// a second time. Schema-match (E7-S7) and dict-drift (E7-S8)
		// gates fire AFTER this one.
		if err := checkKeySetAlignment(refResp, targetResps, *spec, i); err != nil {
			return nil, nil, err
		}
		// Strict structural schema match across slots (E7-S7). Runs
		// AFTER key-set alignment and BEFORE the per-kind handler
		// dispatches. Three orthogonal gates fire from the same entry
		// point: PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT (reference vs
		// target host shape disagrees), PULSE_OVERLAY_SLOT_NOT_CROSSTAB
		// (spec.Kind requires MATRIX but a slot is non-MATRIX — gated
		// by `kindRequiresMatrix`, stub-false today), and
		// PULSE_OVERLAY_SCHEMA_DIVERGENT (per-axis grouper-kind tuples
		// disagree across slots). Dict-drift (E7-S8) fires AFTER this
		// one.
		if err := checkSlotShapeAndSchema(refResp, targetResps, *spec, i); err != nil {
			return nil, nil, err
		}
		// Categorical dictionary-prefix drift probe (E7-S8). Runs ONLY
		// when the caller opted into the byte-equal fast path via
		// ComposeOverlaySpec.Options.DictPrefixFast = true. Default
		// behaviour (Options == nil OR DictPrefixFast == false) is the
		// SAFE by-label join — every cell / group key compares via its
		// decoded label string regardless of underlying categorical
		// index assignment, so dictionary reordering is tolerated. The
		// fast path opts into direct-index comparison; we fail loud
		// with PULSE_OVERLAY_DICT_PREFIX_DRIFT when any slot
		// dictionary diverges. See processing/compose_overlay_dictprobe.go
		// for the probe-vs-pulse.New scoping discussion.
		if spec.Options != nil && spec.Options.DictPrefixFast {
			if err := checkDictPrefixEquality(refResp, targetResps, *spec, i); err != nil {
				return nil, nil, err
			}
		}
		// Multi-layer dispatch (E7-S12) runs FIRST — kinds in this table
		// emit N layers per spec (one per target). The single-layer
		// dispatch below is the historical default; the two tables are
		// mutually exclusive per kind.
		if mhandler, ok := composeOverlayMultiLayerHandlers[spec.Kind]; ok {
			multi, ws, err := mhandler(spec, refResp, targetResps, refIdx, targetIdxs)
			if err != nil {
				return nil, nil, err
			}
			layers = append(layers, multi...)
			if len(ws) > 0 {
				// Stamp the originating overlay spec index on every warning
				// so the service-layer barrier hook (service.Compose's
				// warning distribution) can route each warning to the
				// matching `OverlayLayer.Warnings` slot deterministically.
				// Mirrors the E2-S1 chain-dispatch convention in
				// processing.ApplyChainOverlays — the dispatcher is the
				// natural owner of the spec index (per-kind handlers
				// receive (refIdx, targetIdxs) but not their own spec
				// position), so stamping here keeps the per-handler
				// signature stable while making the flat warning slice
				// routable downstream.
				for j := range ws {
					if ws[j].Details == nil {
						ws[j].Details = map[string]any{}
					}
					ws[j].Details["overlay_index"] = i
				}
				warnings = append(warnings, ws...)
			}
			continue
		}
		handler, ok := composeOverlayHandlers[spec.Kind]
		if !ok {
			// E7-S4 chassis fallback: no per-kind handler registered
			// yet for any Compose-only kind, so every spec routes
			// through the stub. Once the per-kind handlers register
			// (E7-S9..S12 + E7-S15+), an unknown kind reaching this
			// branch is a genuine kind-unknown error and the stub
			// fallback becomes the predict / runtime defense in depth.
			// For E7-S4 the stub is the production path; predict
			// catches genuinely unknown kinds upstream.
			handler = applyComposeStub
		}
		layer, ws, err := handler(spec, refResp, targetResps, refIdx, targetIdxs)
		if err != nil {
			return nil, nil, err
		}
		layers = append(layers, layer)
		if len(ws) > 0 {
			// Stamp the originating overlay spec index on every warning
			// so the service-layer barrier hook in `service.Compose`
			// can route each warning to the matching
			// `OverlayLayer.Warnings` slot deterministically. Mirrors
			// the E2-S1 chain-dispatch convention in
			// `processing.ApplyChainOverlays`.
			for j := range ws {
				if ws[j].Details == nil {
					ws[j].Details = map[string]any{}
				}
				ws[j].Details["overlay_index"] = i
			}
			warnings = append(warnings, ws...)
		}
	}
	return layers, warnings, nil
}

// resolveComposeSlotsFromLabels is the chassis-side adapter that lifts
// the parallel `labels []string` slice the service-layer call site
// hands to ApplyComposeOverlays into the canonical resolver shape. The
// resolver (ResolveComposeSlots) takes a *types.ComposedRequest by
// design — the per-slot Label lives on each *types.Request — so this
// adapter synthesises a minimal ComposedRequest carrying only the
// label slots the resolver needs.
//
// Auto-default policy is applied here exactly as ResolveComposeSlots
// would apply it on a real *ComposedRequest: empty label at slot i →
// `request_<i+1>`. Nil-label slots correspond to nil *Request entries
// in the synthetic ComposedRequest, which the resolver skips entirely.
//
// Returns a coded error from ResolveComposeSlots on collision or
// length mismatch — no additional failure modes are introduced by the
// adapter.
func resolveComposeSlotsFromLabels(labels []string, responses []*types.Response) (map[string]*types.Response, map[string]int, error) {
	if len(labels) == 0 && len(responses) == 0 {
		return map[string]*types.Response{}, map[string]int{}, nil
	}
	// Build a synthetic *ComposedRequest from the labels slice. Empty
	// labels at slot i translate into a non-nil *Request with empty
	// Label, which the resolver fills with `request_<i+1>` per the
	// same auto-default rule the service-side normaliser uses.
	requests := make([]*types.Request, len(labels))
	for i, name := range labels {
		requests[i] = &types.Request{Label: name}
	}
	composed := &types.ComposedRequest{Requests: requests}
	return ResolveComposeSlots(composed, responses)
}

// applyComposeStub is the compile-time stub handler shared by every
// E7-S4 Compose-only kind. Each invocation emits a single
// OverlayLayer carrying:
//
//   - Name  — the spec's Name when set, else "OVERLAY_{KIND}" so the
//     renderer-facing label is always populated.
//   - Kind  — the spec's Kind, echoed verbatim.
//   - Scope — the spec's Scope, echoed verbatim (validator enforces
//     scope policy at predict / runtime entry; the stub trusts
//     the caller).
//   - Payload — a zero-value payload whose Shape inherits from the
//     reference slot's host shape: matrix when the reference
//     carries a Crosstab matrix; series when it carries
//     grouped Process Data; scalar otherwise. The stub does
//     NOT populate the payload's per-coordinate value slots —
//     the real arithmetic ships with E7-S9..S12 + E7-S15+.
//   - Summary — nil (descriptive summary slots are kind-specific and
//     land with the real handlers).
//
// The stub emits no warnings and no error. The chassis test exercises
// the round-trip: spec count == layer count, layer.Kind matches
// spec.Kind, per-slot Responses[i].Overlays untouched. Real arithmetic
// (per-coordinate reference / target folding, zero-denominator
// handling, shape-divergence detection) ships with the per-kind
// handlers in E7-S9 through E7-S15.
func applyComposeStub(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) (types.OverlayLayer, []types.OverlayWarning, error) {
	name := spec.Name
	if name == "" {
		name = string(spec.Kind)
	}
	// Inherit the reference slot's host shape so the layer Shape
	// echoes the visualisation surface the renderer expects. The
	// CHAIN dispatcher's `inferChainStubShape` uses the same
	// precedence rule against the target stage; the COMPOSE chassis
	// uses the REFERENCE slot because the Compose-only kinds compare
	// targets AGAINST the reference (the reference's shape is the
	// canonical anchor — mirroring the SHARE_OF_* family contract).
	shape := inferComposeStubShape(reference)
	return types.OverlayLayer{
		Name:  name,
		Kind:  spec.Kind,
		Scope: spec.Scope,
		// Ref slot on the response side is the OverlayRef union, NOT
		// the ComposeOverlaySpec's Reference/Targets pair (the spec's
		// slot labels live on a different struct). The stub leaves
		// Ref empty — the per-kind validator + real handler in
		// E7-S9..S15 will surface the resolved slot labels through a
		// kind-specific summary slot when the math lands.
		Payload: types.OverlayPayload{
			Shape: shape,
		},
	}, nil, nil
}

// inferComposeStubShape picks an OverlayShape for the stub layer
// based on the reference slot's host result shape. Reads only the
// top-level Crosstab / Data slots so it never touches per-record
// state.
//
// Precedence:
//
//   - Reference carries a non-nil Crosstab matrix ⇒ OverlayShapeMatrix.
//   - Reference carries non-empty Data ⇒ OverlayShapeSeries (the
//     canonical grouped Process shape).
//   - Otherwise (scalar facet result OR empty Data) ⇒ OverlayShapeScalar.
//
// The real handlers in E7-S9..S15 inherit the same precedence rule;
// codifying it here keeps the chassis byte-identical to the shipping
// shape selection logic.
func inferComposeStubShape(reference *types.Response) types.OverlayShape {
	if reference == nil {
		return types.OverlayShapeScalar
	}
	if reference.Crosstab != nil && reference.Crosstab.Matrix != nil {
		return types.OverlayShapeMatrix
	}
	if len(reference.Data) > 0 {
		return types.OverlayShapeSeries
	}
	return types.OverlayShapeScalar
}
