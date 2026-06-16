package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// FACET-host overlay fold engine — runtime side of the Facet overlay
// catalog.
//
// E5-S2 scope: structural pre-req for the four Facet overlay kinds the
// E5 epic ships (`OVERLAY_INDEX_VS_POP` / E5-S2, `OVERLAY_ZSCORE_VS_POP`
// / E5-S3, `OVERLAY_CHISQ_VS_POP` / E5-S4, `OVERLAY_KS_VS_POP` /
// E5-S5). The MATRIX-host overlay path (`overlay.go`) folds against a
// crosstab; the SERIES-host overlay path (`overlay_series.go`) folds
// against a grouped Process result. This file is its parallel for a
// FACET host — a finalised `*types.FacetResult` plus a resolver-built
// `FacetPopulationView` (E5-S1 foundation) the per-kind handler reads
// against to emit the per-value population-comparison projection.
//
// What this file lands (per the E5-S2 acceptance criteria):
//
//   - facetOverlayHandler is the per-kind execution signature for the
//     FACET host path. Handlers receive the spec, the host FacetField,
//     and the resolver's population view and return a fully-shaped
//     OverlayLayer plus warnings.
//
//   - facetOverlayHandlers is the per-kind dispatch table. E5-S2 lands
//     `OVERLAY_INDEX_VS_POP`; E5-S3 / E5-S4 / E5-S5 add the other three
//     kinds.
//
//   - ApplyOverlaysFacet walks `specs` in matching order, dispatches
//     each via facetOverlayHandlers, and returns one OverlayLayer per
//     spec in matching order plus a flat warnings slice. Unknown kinds
//     short-circuit with the same PROCESSING_INTERNAL +
//     PULSE_OVERLAY_KIND_UNKNOWN details shape the MATRIX / SERIES
//     paths emit.
//
// Service-side wiring (per the E5-S2 acceptance "Streaming finalize
// hook engaged when host Facet streams"): the service layer
// (`service/facet_rich.go`) will call ApplyOverlaysFacet at the
// FacetSchema streaming finalize point — once the per-value `(value,
// count)` map is folded into the FacetField.Discrete.Values slice
// (categorical) or the Welford summary + histogram + percentile slots
// are populated (numeric) the overlay reads the already-finalized host
// state. The wiring itself lands in E5-S6; this file ships the
// dispatch + the handler so the service-side call has somewhere to
// land.
//
// Streamability discipline: FACET handlers may be streamable (E5-S2 /
// E5-S3 / E5-S4) or buffered (E5-S5 walks the percentile sort). The
// dispatch entry does NOT commit to a memory model at fold-entry —
// that decision rides on the per-kind handler. ApplyOverlaysFacet is
// purely structural; it does not inspect
// `types.OverlayStreamability`.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution stays inside processing/ alongside overlay.go
//     and overlay_series.go.
//
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays grep-
//     clean against the structural defense ban (CLAUDE.md "Output
//     Format Contract").

// facetOverlayHandler is the per-kind execution signature for the
// FACET host path. Handlers receive the request spec, the host
// FacetField (one slot from `FacetResult.Fields[req.Field]` — the
// caller resolves which field via the spec / request configuration),
// and the population view the resolver materialised. They return a
// fully-shaped OverlayLayer (Name + Kind + Scope + Ref + Payload +
// optional Summary) plus a slice of warnings for values the handler
// could not resolve (zero pop_freq, missing population entry).
//
// Signature parity with `overlayHandler` (MATRIX path) and
// `seriesOverlayHandler` (SERIES path) is intentional — the only
// difference is the host parameter type (FacetField + population
// view). Future stories may unify the three through a shared
// interface; v1 keeps them separate so each path's host-shape
// contract stays explicit at the type level.
type facetOverlayHandler func(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []types.OverlayWarning, error)

// facetOverlayHandlers is the per-kind dispatch table for the FACET
// host path. Stories E5-S2 / E5-S3 / E5-S4 / E5-S5 register the real
// handlers (and add the matching kind constants + capability rows +
// skill mentions per the CLAUDE.md Update Demand row for overlays).
//
// Tests register synthetic handlers by writing into this map and
// using `t.Cleanup` to restore the prior state — the test stub
// pattern documented in processing/overlay_series.go's analogous
// table. The dispatch is intentionally not hidden behind a
// registration function: keeping the table as a plain package-level
// map lets the test infra swap entries without touching production-
// only code paths.
//
// Adding a new FACET OverlayKind: declare the constant + streamability
// row (types/overlay.go + types/overlay_streamability.go), add the
// runtime handler in this package, and add the dispatch entry here.
var facetOverlayHandlers = map[types.OverlayKind]facetOverlayHandler{
	// OVERLAY_CHISQ_VS_POP (E5-S4): single scalar χ² goodness-of-fit
	// statistic against a FACET host. First inferential FACET-host
	// kind — buffered per PRD §2 Non-Goals ("Streaming overlay path
	// for inferential kinds"). Pairs with the MATRIX-host CHISQ family
	// (CHISQ_MATRIX / CHISQ_ROW / CHISQ_COL) as the canonical χ²
	// family — viz developer renders the SCALAR statistic as a
	// goodness-of-fit badge near the facet header. Handler:
	// applyChiSqVsPop in processing/overlay_chisq_vs_pop.go.
	types.OverlayKindChiSqVsPop: applyChiSqVsPop,
	// OVERLAY_INDEX_VS_POP (E5-S2): per-value population-comparison
	// index against a FACET host (categorical fast path or numeric
	// histogram path). First streamable FACET-host kind in the
	// catalog. Handler: applyIndexVsPop in
	// processing/overlay_index_vs_pop.go.
	types.OverlayKindIndexVsPop: applyIndexVsPop,
	// OVERLAY_KS_VS_POP (E5-S5): single scalar Kolmogorov-Smirnov
	// distance + asymptotic p-value against a FACET host. Fourth and
	// final FACET-host kind in the catalog — second inferential FACET
	// kind (sibling to OVERLAY_CHISQ_VS_POP). KS is the numeric-arm
	// distributional-shift indicator (CHISQ_VS_POP is the discrete-arm
	// equivalent — the two kinds partition the inferential FACET surface
	// by host arm). Buffered per PRD §2 Non-Goals ("Streaming overlay
	// path for inferential kinds"). Handler: applyKSVsPop in
	// processing/overlay_ks_vs_pop.go.
	types.OverlayKindKSVsPop: applyKSVsPop,
	// OVERLAY_ZSCORE_VS_POP (E5-S3): per-value population-comparison
	// z-score against a FACET host. Sibling streamable FACET-host kind
	// to OVERLAY_INDEX_VS_POP — pairs as the two streamable Facet
	// overlay kinds. Handler: applyZScoreVsPop in
	// processing/overlay_zscore_vs_pop.go.
	types.OverlayKindZScoreVsPop: applyZScoreVsPop,
}

// ApplyOverlaysFacet executes every spec in specs against the FACET
// host view and returns one OverlayLayer per spec in matching order
// (FR-A2: stable spec-order layer emission), plus a flat warning
// slice. Returns (nil, nil, nil) when specs is empty so the
// service-side FacetSchema finalize call site can call
// ApplyOverlaysFacet unconditionally — mirrors the MATRIX
// ApplyOverlays and SERIES ApplyOverlaysSeries short-circuit
// contract.
//
// Host policy:
//
//   - When specs is empty, host AND pop may be nil. The dispatch
//     short-circuits before touching either.
//   - When specs is non-empty, host may still be nil OR carry an
//     empty discrete / numeric payload — the dispatch passes the
//     view straight through to the handler. The per-kind handler
//     decides how to surface an empty host (typically an empty
//     Entries slice without warning; INDEX_VS_POP does exactly
//     that).
//   - When specs is non-empty, pop SHOULD be the result of a
//     successful `ResolveFacetPopulation` call — a nil pop indicates
//     the caller bypassed the resolver. Handlers fail closed with
//     a coded PROCESSING_INTERNAL error on a nil pop; the resolver
//     itself surfaces `PULSE_OVERLAY_REF_UNKNOWN` for the unknown-
//     field case BEFORE this dispatch is reached.
//
// Defense in depth: the descriptor.ValidateOverlays gate (E5-S6) will
// reject bad kinds at predict time, so a missing dispatch entry
// should never reach the runtime in practice; nonetheless
// ApplyOverlaysFacet guards against an unknown kind and returns a
// CodedError whose details carry errors.PULSE_OVERLAY_KIND_UNKNOWN so
// the orchestrator surfaces the same failure mode predict would have
// flagged. The Level / Within belt-and-suspenders runtime gate the
// MATRIX path runs at `validateOverlayLevelWithinRuntime` does NOT
// fire here — FACET kinds in v1 do not engage prefix-bucket
// denominators, and the per-kind E5-S2..S5 handlers add their own
// gates when their math requires it.
func ApplyOverlaysFacet(specs []types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) ([]types.OverlayLayer, []types.OverlayWarning, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	layers := make([]types.OverlayLayer, 0, len(specs))
	var warnings []types.OverlayWarning
	for i := range specs {
		spec := &specs[i]
		handler, ok := facetOverlayHandlers[spec.Kind]
		if !ok {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay kind has no FACET runtime handler: "+string(spec.Kind),
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
					"index": i,
					"kind":  string(spec.Kind),
					"host":  "facet",
				})
		}
		layer, ws, err := handler(spec, host, pop)
		if err != nil {
			return nil, nil, err
		}
		layers = append(layers, layer)
		if len(ws) > 0 {
			warnings = append(warnings, ws...)
		}
	}
	return layers, warnings, nil
}
