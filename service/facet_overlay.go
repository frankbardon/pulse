package service

import (
	"context"
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// FACET-host overlay wiring — service-layer orchestration of the four
// FACET-host overlay handlers (OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP /
// OVERLAY_CHISQ_VS_POP / OVERLAY_KS_VS_POP) at the FacetSchema buffered
// exit (E5-S6).
//
// The runtime handlers live entirely inside processing/ (the overlay
// catalog stays free of service/ imports per CLAUDE.md "What NOT to
// Do"); this file owns:
//
//   - Population cohort opening (the service-layer cohort opener is the
//     only surface that can resolve a Ref.Population.Cohort to a
//     *FacetResult — the resolver in processing/ consumes already-
//     materialised state).
//   - Population FacetResult materialisation via FacetSchema recursion
//     (every population-comparison kind reads against a finalised
//     *types.FacetResult; the per-overlay population reuses the
//     existing FacetSchema entry point).
//   - Per-spec dispatch through processing.ApplyOverlaysFacet, which
//     walks the spec slice in matching order and hands each spec to the
//     matching facetOverlayHandlers entry.
//   - Warning promotion onto FacetResult.Warnings — the dispatch's
//     OverlayWarning slice carries the canonical PULSE_OVERLAY_* codes
//     the per-kind handlers emit and the FacetResult's []string warnings
//     surface absorbs each entry as a `<code>: <message>` line (matches
//     the existing warning shape FacetSchema produces for top-K
//     truncation and label-collision diagnostics — the FacetResult
//     warnings slot is structurally []string today; a future story may
//     widen it to []*ResponseWarning).
//
// Streaming-vs-buffered byte-identity: the per-handler streamability flag
// (types.OverlayStreamability) classifies INDEX_VS_POP / ZSCORE_VS_POP as
// streamable and CHISQ_VS_POP / KS_VS_POP as buffered. The host
// FacetSchema path is structurally buffered today (the streaming
// accumulators finalise at the end of the pass), so all four kinds run
// at the SAME exit point regardless of streamability. The streamability
// flag still drives MCP discoverability and predict cost reporting; the
// runtime wiring is uniform.
//
// Population cohort cache: when multiple specs reference the same
// Ref.Population.Cohort the helper materialises the population
// FacetResult ONCE per cohort+field combination and reuses the view
// across specs. The cache lifetime is the single FacetSchema call —
// callers issuing repeated FacetSchema requests over the same population
// pay the materialisation cost each time (the cache is intentionally
// per-call to keep memory bounded; later epics may lift the cache up
// when LRU semantics are spec'd).
//
// Structural invariants:
//
//   - Recursion guard: the population-side FacetSchema call strips
//     Overlays from its FacetRequest before recursing so a recursive
//     overlay chain does not run on the population cohort. The
//     population is always a flat distribution.
//   - Cycle guard: the population cohort may be the SAME .pulse file as
//     the host cohort (the legitimate "compare a filtered cohort
//     against itself with no filters" use case). The cycle guard does
//     NOT reject this — the population FacetRequest carries no
//     Filterers / AdditiveFields so the same-cohort recursion produces
//     the unfiltered baseline, which is exactly what the population-
//     comparison family wants.
//   - Label suppression on population: the population FacetRequest does
//     NOT carry the host's Labels slot. Labels are display-only and
//     rewriting the population's per-value keys would break the
//     resolver's value-name matching against the host's payload.

// applyFacetOverlays runs the FACET-host overlay dispatch against a
// finalised FacetResult and attaches the resulting layers in spec
// order. No-op when req.Overlays is empty (additive byte-identity
// contract).
func (s *Service) applyFacetOverlays(ctx context.Context, req *types.FacetRequest, result *types.FacetResult) error {
	if req == nil || len(req.Overlays) == 0 {
		return nil
	}
	if result == nil {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"facet overlay dispatch requires a finalised FacetResult")
	}

	cache := newPopulationCache()
	layers := make([]types.OverlayLayer, 0, len(req.Overlays))

	for i := range req.Overlays {
		spec := &req.Overlays[i]
		hostFieldName, err := resolveFacetOverlayHostFieldRuntime(req, spec, i)
		if err != nil {
			return err
		}
		hostField, ok := result.Fields[hostFieldName]
		if !ok || hostField == nil {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"facet overlay host field "+hostFieldName+" missing from finalised FacetResult",
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_REF_UNKNOWN),
					"index": i,
					"kind":  string(spec.Kind),
					"field": hostFieldName,
				})
		}
		if spec.Ref.Population == nil || spec.Ref.Population.Cohort == "" {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"facet overlay "+string(spec.Kind)+" requires Ref.Population.Cohort",
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
					"index": i,
					"kind":  string(spec.Kind),
				})
		}
		popResult, err := cache.populationResult(ctx, s, spec.Ref.Population.Cohort, hostFieldName, req)
		if err != nil {
			return err
		}
		popView, err := processing.ResolveFacetPopulation(popResult, hostFieldName)
		if err != nil {
			return err
		}
		// Run a single-spec dispatch so warnings stay attributable to
		// this spec's index. ApplyOverlaysFacet is the canonical entry
		// point; we pass a one-element slice and accept the returned
		// layer / warning slice verbatim.
		singleSpec := []types.OverlaySpec{*spec}
		dispatched, warnings, err := processing.ApplyOverlaysFacet(singleSpec, hostField, popView)
		if err != nil {
			return err
		}
		layers = append(layers, dispatched...)
		for _, w := range warnings {
			result.Warnings = append(result.Warnings, formatOverlayWarning(w))
		}
	}

	if len(layers) > 0 {
		result.Overlays = layers
	}
	return nil
}

// resolveFacetOverlayHostFieldRuntime is the runtime mirror of the
// descriptor.resolveFacetOverlayHostField helper. Reads spec.Params
// ["field"] first; when absent falls back to req.Fields[0] when
// FacetRequest.Fields declares exactly one entry. Returns an error
// (coded PROCESSING_INTERNAL with PULSE_OVERLAY_PARAM_MISSING or
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE details) when neither slot
// resolves to a valid host-field name.
//
// Defense in depth: the descriptor validator (ValidateFacetOverlays)
// already catches these failure modes at predict time; the runtime
// mirror surfaces a coded error when a caller bypassed predict and
// dispatched directly into FacetSchema with an unresolved spec.
func resolveFacetOverlayHostFieldRuntime(req *types.FacetRequest, spec *types.OverlaySpec, index int) (string, error) {
	requested, hasParam := readFacetOverlayFieldParamRuntime(spec.Params)
	if hasParam {
		for _, name := range req.Fields {
			if name == requested {
				return requested, nil
			}
		}
		return "", errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"facet overlay Params[\"field\"] does not reference any FacetRequest.Fields entry: "+requested,
			map[string]any{
				"code":             string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"index":            index,
				"kind":             string(spec.Kind),
				"field":            requested,
				"available_fields": append([]string(nil), req.Fields...),
			})
	}
	if len(req.Fields) == 1 {
		return req.Fields[0], nil
	}
	return "", errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"facet overlay requires Params[\"field\"] when FacetRequest.Fields declares multiple fields",
		map[string]any{
			"code":             string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"index":            index,
			"kind":             string(spec.Kind),
			"param":            "field",
			"available_fields": append([]string(nil), req.Fields...),
		})
}

// readFacetOverlayFieldParamRuntime mirrors descriptor's
// readFacetOverlayFieldParam — pulls the "field" key out of a raw JSON
// Params blob. Kept here so the service layer does not import
// descriptor/ (predict / service separation per CLAUDE.md).
func readFacetOverlayFieldParamRuntime(params json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal(params, &m); err != nil {
		return "", false
	}
	raw, present := m["field"]
	if !present {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}

// populationCache memoises per-cohort FacetResult materialisation so
// multiple specs against the same comparison cohort share the
// materialisation cost. Cache lifetime is the single FacetSchema call.
type populationCache struct {
	byKey map[string]*types.FacetResult
}

func newPopulationCache() *populationCache {
	return &populationCache{byKey: map[string]*types.FacetResult{}}
}

// populationResult materialises a FacetResult against the population
// cohort for the requested host field, reusing a prior materialisation
// when the same cohort+field pair has already been resolved during the
// current FacetSchema call.
//
// The recursive FacetSchema call strips overlays / filterers /
// additive-fields / labels from the population FacetRequest so the
// population always represents the FLAT, UNFILTERED distribution of the
// host field over the comparison cohort. The cycle guard documented at
// the top of this file relies on this — the same-cohort recursion path
// produces the unfiltered baseline by construction.
func (c *populationCache) populationResult(ctx context.Context, s *Service, cohortName, field string, hostReq *types.FacetRequest) (*types.FacetResult, error) {
	key := cohortName + "\x00" + field
	if cached, ok := c.byKey[key]; ok {
		return cached, nil
	}
	popReq := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: cohortName},
		Fields: []string{field},
		// E5-S5 KS_VS_POP reads `NumericPercentiles` for the population
		// CDF and the histogram path for INDEX_VS_POP. Forward the host
		// request's matching slots so the population materialisation
		// carries the same numerical surface the handlers expect to
		// find on the host (per-kind handlers fail closed when the
		// population view lacks the percentile / histogram payload).
		NumericPercentiles: hostReq.NumericPercentiles,
		IncludeHistogram:   hostReq.IncludeHistogram,
		HistogramBins:      hostReq.HistogramBins,
		HistogramRange:     hostReq.HistogramRange,
	}
	popResult, err := s.FacetSchema(ctx, popReq)
	if err != nil {
		return nil, err
	}
	c.byKey[key] = popResult
	return popResult, nil
}

// formatOverlayWarning renders an OverlayWarning as a single warning
// line for FacetResult.Warnings. The FacetResult warnings slot is
// []string today (the per-field accumulator warnings about top-K
// truncation use the same shape); the line carries `<code>: <message>`
// so a caller surfacing FacetResult.Warnings into a UI sees the
// canonical PULSE_OVERLAY_* code alongside the human-readable summary.
// String concatenation only — no fmt.Sprintf per the descriptor /
// processing no-fmt-Sprintf ban (mirrored here for grep parity).
func formatOverlayWarning(w types.OverlayWarning) string {
	if w.Code == "" {
		return w.Message
	}
	if w.Message == "" {
		return w.Code
	}
	return w.Code + ": " + w.Message
}
