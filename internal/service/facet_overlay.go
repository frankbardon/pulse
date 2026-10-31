package service

import (
	"context"
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// FACET-host overlay wiring — service-layer orchestration of the four
// FACET-host overlay handlers (OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP /
// OVERLAY_CHISQ_VS_POP / OVERLAY_KS_VS_POP) at the FacetSchema buffered
// exit.
//
// The runtime handlers live entirely inside internal/processing/ (the overlay
// catalog stays free of internal/service/ imports per CLAUDE.md "What NOT to
// Do"); this file owns:
//
//   - Population cohort opening (the service-layer cohort opener is the
//     only surface that can resolve a Ref.Population.Cohort to a
//     *FacetResult — the resolver in internal/processing/ consumes already-
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
//     the per-kind handlers emit, and each entry lands on
//     FacetResult.Warnings as a coded {code, message, details}
//     ResponseWarning — the shape of the top-K truncation and label
//     diagnostics beside it.
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
//   - Labels mirror the host: applyFacetLabels relabels the host's
//     discrete values BEFORE the overlays run, so the population
//     FacetRequest carries the host's bindings on the overlay field
//     (and only those) — host and population values then share one key
//     space, the labelled one the caller sees on the host. Without them
//     a replace-mode binding left every host key unmatched
//     (PULSE_OVERLAY_REF_ZERO) and the raw population categories looked
//     absent from the subset. Carrying the field's bindings, augment
//     ones included, also keeps the population's default-label
//     injection (applyAutoLabels skips a bound field) in step with the
//     host's.

// applyFacetOverlays runs the FACET-host overlay dispatch against a
// finalised FacetResult and attaches the resulting layers in spec
// order. No-op when req.Overlays is empty (additive byte-identity
// contract).
//
// mult is the overlays' resolved multiplicity
// (descx.ResolveFacetMultiplicity, index-aligned with req.Overlays; nil
// when nothing names a block). Each spec's layers fold on their own
// right after dispatch: a facet overlay offers only the per-layer
// families (`layer`, and `row` / `column` on a MATRIX kind), so no
// family ever spans two specs.
func (s *Service) applyFacetOverlays(ctx context.Context, req *types.FacetRequest, result *types.FacetResult, mult []descx.ResolvedMultiplicity) error {
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
		// A catalog kind that does not run on FacetRequest.Overlays —
		// the shared wrong-slot refusal predict raises
		// (validateFacetOverlaySpec), ahead of the host-field and
		// population work every FACET kind needs.
		if err := processing.WrongHostRefusal(overlayhost.Facet, spec.Kind, s.extensions, i); err != nil {
			return err
		}
		// Then the shared Ref / Scope / Level contract predict raises at
		// the same point (overlayhost.FacetSpecRefusal): Ref.Population
		// alone and set, group scope, Level / Within 0.
		if err := processing.FacetSpecRefusal(spec, s.extensions, i); err != nil {
			return err
		}
		hostFieldName, err := resolveFacetOverlayHostFieldRuntime(req, spec, i)
		if err != nil {
			return err
		}
		hostField, ok := result.Fields[hostFieldName]
		if !ok || hostField == nil {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_UNKNOWN,
				"facet overlay host field "+hostFieldName+" missing from finalised FacetResult",
				map[string]any{
					"index": i,
					"kind":  string(spec.Kind),
					"field": hostFieldName,
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
		dispatched, warnings, err := processing.ApplyOverlaysFacetWithExtensions(singleSpec, hostField, popView, s.extensions)
		if err != nil {
			return err
		}
		if i < len(mult) && mult[i].Member {
			fams := newMultFamilies()
			collectOverlaySites(fams, "facet.", mult[i:i+1], dispatched)
			if err := fams.fold(); err != nil {
				return err
			}
		}
		layers = append(layers, dispatched...)
		for _, w := range warnings {
			result.Warnings = append(result.Warnings, &types.ResponseWarning{Code: w.Code, Message: w.Message, Details: w.Details})
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
// (coded PULSE_OVERLAY_PARAM_MISSING or
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) when neither slot
// resolves to a valid host-field name.
//
// Defense in depth: the descriptor validator (ValidateFacetOverlays)
// already catches these failure modes at predict time; the runtime
// mirror surfaces a coded error when a caller bypassed predict and
// dispatched directly into FacetSchema with an unresolved spec.
func resolveFacetOverlayHostFieldRuntime(req *types.FacetRequest, spec *types.OverlaySpec, index int) (string, error) {
	paramread.Read(paramread.Overlay, string(spec.Kind), "field")
	requested, hasParam := readFacetOverlayFieldParamRuntime(spec.Params)
	if hasParam {
		for _, name := range req.Fields {
			if name == requested {
				return requested, nil
			}
		}
		return "", errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"facet overlay Params[\"field\"] does not reference any FacetRequest.Fields entry: "+requested,
			map[string]any{
				"index":            index,
				"kind":             string(spec.Kind),
				"field":            requested,
				"available_fields": append([]string(nil), req.Fields...),
			})
	}
	if len(req.Fields) == 1 {
		return req.Fields[0], nil
	}
	return "", errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING,
		"facet overlay requires Params[\"field\"] when FacetRequest.Fields declares multiple fields",
		map[string]any{
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
// additive-fields from the population FacetRequest so the population
// always represents the FLAT, UNFILTERED distribution of the host field
// over the comparison cohort. It keeps the host's label bindings on that
// field (facetFieldLabels) so the population's values are keyed exactly
// as the host's labelled values are. The cycle guard documented at
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
		// KS_VS_POP reads `NumericPercentiles` for the population
		// CDF and the histogram path for INDEX_VS_POP. Forward the host
		// request's matching slots so the population materialisation
		// carries the same numerical surface the handlers expect to
		// find on the host (per-kind handlers fail closed when the
		// population view lacks the percentile / histogram payload).
		NumericPercentiles: hostReq.NumericPercentiles,
		IncludeHistogram:   hostReq.IncludeHistogram,
		HistogramBins:      hostReq.HistogramBins,
		HistogramRange:     hostReq.HistogramRange,
		Labels:             facetFieldLabels(hostReq.Labels, field),
	}
	popResult, err := s.FacetSchema(ctx, popReq)
	if err != nil {
		return nil, err
	}
	c.byKey[key] = popResult
	return popResult, nil
}

// facetFieldLabels returns the bindings of labels that name field, in
// order — the host's bindings the population FacetRequest must carry so
// its discrete values land in the host's key space. Nil when none do.
func facetFieldLabels(labels []*types.LabelBinding, field string) []*types.LabelBinding {
	var out []*types.LabelBinding
	for _, b := range labels {
		if b != nil && b.Field == field {
			out = append(out, b)
		}
	}
	return out
}
