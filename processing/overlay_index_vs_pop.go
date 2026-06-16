package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_INDEX_VS_POP — per-value population-comparison index against a
// FACET host.
//
// E5-S2 scope:
//
//   - First FACET-host handler in the catalog. Registered in
//     `facetOverlayHandlers` (processing/overlay_facet_dispatch.go); the
//     dispatch route is the post-host-finalize entry point for the
//     streaming-Facet orchestrator (E5-S6 wires `ApplyOverlaysFacet`
//     into `service.FacetSchema`) AND the buffered fallback entry for
//     any callers that materialise a FacetResult before calling into the
//     overlay surface.
//
//   - Per-value math: `index = subset_freq / pop_freq * 100` where
//     `subset_freq` is the host FacetResult's per-value frequency
//     (count / FilteredRecords for discrete fields; bin-count / total-
//     bin-count for numeric fields that requested a histogram) and
//     `pop_freq` is the resolver-supplied per-value frequency of the
//     reference population (via `FacetPopulationView.DiscreteFrequency` /
//     `FacetPopulationView.NumericHistogram`).
//
//   - Zero pop_freq path: when `pop_freq == 0` (the value never appeared
//     in the population OR the population is empty), the handler emits
//     ONE PULSE_OVERLAY_REF_ZERO warning per affected entry carrying the
//     kind + value and SKIPS the index for that entry (the entry's
//     Summary leaves Statistic unset — "present slot, empty summary"
//     shape). The E5-S2 acceptance criterion is explicit: "on
//     `pop_freq == 0` emit warning code `PULSE_OVERLAY_REF_ZERO` and
//     skip the index entry". Subsequent entries continue to emit indices.
//
//   - Absent-host-value policy: a host whose per-value count list is
//     empty (no discrete payload, no histogram) yields an empty Entries
//     slice without warning — the layer is well-formed but carries no
//     indices. Renderers surface that as "no comparison available".
//
// Streaming finalize hook (per E5-S2 acceptance): the handler runs at
// the FacetSchema streaming finalize point. By the time
// `ApplyOverlaysFacet` calls into this handler, the host FacetResult is
// fully finalised and the population view is fully resolved — both are
// POST-FINALIZE inputs. The streaming Facet pass keeps its existing
// online accumulators (Welford trio / per-value count map / histogram
// bins) and the overlay does NOT widen the streaming carrier — the
// streamability guarantee is that running this handler against a
// streaming Facet host vs a buffered one produces byte-identical
// SeriesPayload output because the input state is identical.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go / overlay_index_vs_total.go).
//
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation; numeric bin labels render via
//     strconv (the no-Sprintf ban covers fmt only).
//
// Forward-compat notes for E5-S3 / E5-S4 / E5-S5:
//
//   - The categorical fast path is the canonical implementation. The
//     numeric histogram path is a structural placeholder — E5-S5
//     (`OVERLAY_KS_VS_POP`) is the canonical numeric population-
//     comparison kind, walking the percentile / histogram surface as a
//     KS test. INDEX_VS_POP's numeric path stays narrow on purpose:
//     per-bin frequency indexing is the simplest stream-compatible
//     surface that does NOT need the percentile sort.
//
//   - The dispatch + handler signature mirror `overlay_series.go`
//     verbatim so future FACET kinds (`OVERLAY_ZSCORE_VS_POP` /
//     `OVERLAY_CHISQ_VS_POP` / `OVERLAY_KS_VS_POP`) drop in by adding
//     a row to `facetOverlayHandlers` plus a per-kind handler file.

// applyIndexVsPop is the OVERLAY_INDEX_VS_POP runtime handler. For every
// host value (categorical fast path) or every host histogram bin
// (numeric path) it surfaces `(subset_freq / pop_freq) * 100` on the
// SeriesEntry's Summary.Statistic. The host's per-value distribution is
// already materialised on the FacetResult; the population view is the
// resolver-supplied FacetPopulationView that E5-S1 ships.
//
// Two dispatch arms branch on the host's FacetField.Kind:
//
//  1. Discrete: walks `host.Discrete.Values` in payload order. Each entry
//     emits the per-value index against the population's per-value
//     frequency (via `FacetPopulationView.DiscreteFrequency`).
//  2. Numeric: walks `host.Numeric.Histogram.Bins` in bin-index order
//     when the host requested a histogram. Each entry emits the per-
//     bin index against the population's per-bin frequency (via
//     `FacetPopulationView.NumericHistogram` against bin-aligned
//     subsets). When the host did NOT request a histogram the handler
//     emits zero entries (no per-value buckets to index).
//
// Both arms share the same emission shape (SeriesEntry per host value /
// bin with Key carrying the value-string tuple) and the same
// zero-pop_freq warning contract.
//
// Defense in depth: the descriptor validator (lands in E5-S6) rejects
// ref / scope mismatches at predict time. The handler still defends
// against a nil host (caller passed nil into `ApplyOverlaysFacet`) and
// a nil population view by returning a coded PROCESSING_INTERNAL error.
// The E5-S1 resolver itself returns a coded PULSE_OVERLAY_REF_UNKNOWN
// error when the named population FIELD is unknown — that error
// surfaces from `ResolveFacetPopulation` BEFORE this handler runs, so
// the handler sees only resolved views.
func applyIndexVsPop(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"overlay OVERLAY_INDEX_VS_POP requires a non-nil OverlaySpec")
	}
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil FacetField host",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}
	if pop == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil FacetPopulationView",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	switch host.Kind {
	case "discrete":
		return applyIndexVsPopDiscrete(spec, host, pop)
	case "numeric":
		return applyIndexVsPopNumeric(spec, host, pop)
	}
	// Unknown host kind — emit an empty layer rather than failing the
	// whole request. The host shape is set by FacetSchema; an unknown
	// value here means a future kind extension landed without updating
	// this handler. Defense in depth.
	return emptyIndexVsPopLayer(spec), nil, nil
}

// applyIndexVsPopDiscrete walks the host's discrete `(value, count)`
// payload in payload order (descending by count; ties ascending by
// value-string — the canonical FacetSchema sort) and emits one
// SeriesEntry per value. Each entry's Summary carries the per-value
// index `subset_freq / pop_freq * 100` on Statistic. The categorical
// fast path is one division per host value plus one map lookup per
// host value through the resolver — O(host values × 1).
//
// Zero-denominator path: when `pop_freq == 0` (value absent from the
// population OR population total records is zero), one
// PULSE_OVERLAY_REF_ZERO warning is emitted per affected entry and the
// entry's Statistic stays unset. The parallel-slice contract holds —
// the entry still appears in payload order, just with an empty Summary.
//
// Zero subset-denominator (host FilteredRecords == 0): the entire host
// distribution is empty, so every entry's subset_freq is zero. The
// handler emits indices of 0 for entries whose pop_freq > 0 (the value
// appeared in the population but not in the subset — well-defined as
// zero index, not a denominator failure). When pop_freq is also zero
// the zero-denominator warning still fires.
func applyIndexVsPopDiscrete(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []OverlayWarning, error) {
	if host.Discrete == nil {
		return emptyIndexVsPopLayer(spec), nil, nil
	}

	subsetTotal := subsetDiscreteDenominator(host.Discrete)
	values := host.Discrete.Values

	entries := make([]types.SeriesEntry, 0, len(values))
	var warnings []OverlayWarning
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := range values {
		vc := values[i]
		// Subset frequency: count divided by the host's denominator. When
		// the host carries zero filtered records (degenerate cohort) the
		// subset frequency is zero — the per-value index reduces to
		// zero whenever the population value is present and to the zero-
		// denominator path otherwise.
		var subsetFreq float64
		if subsetTotal > 0 {
			subsetFreq = float64(vc.Count) / float64(subsetTotal)
		}

		// Population frequency lookup. The resolver hides the per-kind
		// denominator (DiscreteFrequency uses TotalRecords from the
		// underlying FacetResult — the canonical post-filter denominator
		// for the population view).
		popFreq, popOk := pop.DiscreteFrequency(vc.Value)
		if !popOk || popFreq == 0 {
			// Zero-denominator path per E5-S2 acceptance: emit ONE
			// PULSE_OVERLAY_REF_ZERO warning carrying the value + kind
			// and SKIP the index. The entry's Summary stays empty so the
			// parallel-slice contract holds (one entry per host value).
			warnings = append(warnings, OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) +
					" population frequency is zero or absent for value " + indexVsPopValueDisplay(vc.Value),
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"host":         "facet",
					"value":        vc.Value,
					"pop_present":  popOk,
					"pop_freq":     popFreq,
					"subset_freq":  subsetFreq,
					"subset_count": vc.Count,
				},
			})
			entries = append(entries, types.SeriesEntry{
				Key: indexVsPopValueKey(vc.Value),
			})
			continue
		}

		index := subsetFreq / popFreq * 100.0
		if math.IsNaN(index) || math.IsInf(index, 0) {
			// Non-finite path: subset_freq / pop_freq with both finite
			// inputs should never produce NaN or Inf because we gated
			// pop_freq == 0 above; defense in depth surfaces the same
			// PULSE_OVERLAY_REF_ZERO contract.
			warnings = append(warnings, OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) +
					" produced a non-finite index for value " + indexVsPopValueDisplay(vc.Value),
				Details: map[string]any{
					"kind":  string(spec.Kind),
					"host":  "facet",
					"value": vc.Value,
				},
			})
			entries = append(entries, types.SeriesEntry{
				Key: indexVsPopValueKey(vc.Value),
			})
			continue
		}

		indexCopy := index
		entries = append(entries, types.SeriesEntry{
			Key: indexVsPopValueKey(vc.Value),
			Summary: types.OverlaySummary{
				Statistic: &indexCopy,
			},
		})
		if seen == 0 {
			minV, maxV = index, index
		} else {
			if index < minV {
				minV = index
			}
			if index > maxV {
				maxV = index
			}
		}
		seen++
	}

	return finalizeIndexVsPopLayer(spec, entries, warnings, minV, maxV, seen), warnings, nil
}

// subsetDiscreteDenominator returns the canonical denominator the
// discrete per-value frequencies are normalized against. Mirrors the
// resolver's `TotalRecords()` shape — for the categorical fast path the
// denominator is the host FacetResult's `FilteredRecords` minus its
// `NullCount`. We approximate it by summing every per-value count
// because the FacetField shape declared in types/facet.go does NOT
// expose FilteredRecords directly (the sum-of-counts equals
// FilteredRecords - NullCount), which is the correct denominator for
// the population-comparison math: both numerator (subset_freq) and
// denominator (pop_freq) are folded across the SAME set of "non-null
// filtered records", so using the sum-of-counts here keeps the math
// consistent across handler and resolver.
//
// Edge cases:
//   - Empty Values slice ⇒ total = 0 ⇒ subset_freq = 0 (handled in the
//     caller without a warning — the host is degenerate).
//   - All counts zero ⇒ total = 0 ⇒ subset_freq = 0 (degenerate).
//
// Free function (not a method) because Go's method-on-named-types rule
// forbids defining a method on a type alias / foreign-package named
// type. The alternative (move the helper into the types package) would
// force overlay-runtime knowledge into the types layer where it does
// not belong — the "sum of per-value counts is the canonical non-null
// denominator" rule is overlay-runtime, not type-system, knowledge.
func subsetDiscreteDenominator(d *types.FacetDiscrete) int64 {
	if d == nil {
		return 0
	}
	var total int64
	for i := range d.Values {
		total += d.Values[i].Count
	}
	return total
}

// applyIndexVsPopNumeric walks the host's numeric histogram in bin-index
// order. Each bin emits one SeriesEntry whose Key carries the bin
// label (the bin's lower edge formatted via strconv) and whose Summary
// carries `subset_bin_freq / pop_bin_freq * 100`. When the host did
// NOT request a histogram the handler emits zero entries — the Welford
// summary alone (mean / sd / count) carries no per-value buckets the
// index can compare against.
//
// Bin alignment requirement: the population view's histogram MUST be
// aligned with the host's histogram (same `Min` / `Max` / bin count).
// The handler's contract is that the caller materialises both
// FacetResults with the same histogram request (a single FacetRequest
// authoring rule the E5-S6 service-side validator enforces; the
// handler does NOT compare bins when the shapes disagree and emits
// zero entries with a warning).
//
// Zero pop_bin_freq path mirrors the discrete path — one warning per
// affected entry, Statistic skipped.
//
// Edge: histogram shape on the host but absent / mismatched on the
// population view — emit one PULSE_OVERLAY_REF_ZERO warning carrying
// the kind and return an empty Entries slice. The population view does
// not surface a per-bin denominator, so every index would be
// zero-denominator anyway.
func applyIndexVsPopNumeric(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []OverlayWarning, error) {
	if host.Numeric == nil || host.Numeric.Histogram == nil {
		// Host did not carry a histogram — emit an empty layer. The
		// caller's FacetRequest may need to set IncludeHistogram=true
		// for INDEX_VS_POP to produce numeric output; documented in the
		// skill, not enforced here (the validator surfaces the missing
		// histogram at predict time when E5-S6 lands).
		return emptyIndexVsPopLayer(spec), nil, nil
	}
	popHist, popOk := pop.NumericHistogram()
	if !popOk || popHist == nil {
		// Population view has no histogram — every per-bin lookup would
		// fail. One layer-level warning carrying the kind; zero entries.
		warnings := []OverlayWarning{{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) +
				" requires a population histogram to index numeric host bins; none present",
			Details: map[string]any{
				"kind": string(spec.Kind),
				"host": "facet",
			},
		}}
		return emptyIndexVsPopLayer(spec), warnings, nil
	}

	hostHist := host.Numeric.Histogram
	// Bin alignment guard: identical Min / Max / bin count. Anything
	// else and the per-bin frequencies are not comparable.
	if len(hostHist.Bins) != len(popHist.Bins) ||
		hostHist.Min != popHist.Min ||
		hostHist.Max != popHist.Max {
		warnings := []OverlayWarning{{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) +
				" host and population histograms have mismatched shapes; indices not computable",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"host":         "facet",
				"host_bin_min": hostHist.Min,
				"host_bin_max": hostHist.Max,
				"host_bin_n":   len(hostHist.Bins),
				"pop_bin_min":  popHist.Min,
				"pop_bin_max":  popHist.Max,
				"pop_bin_n":    len(popHist.Bins),
			},
		}}
		return emptyIndexVsPopLayer(spec), warnings, nil
	}

	// Compute per-histogram totals so we can convert raw bin counts
	// into per-bin frequencies. Both totals are folded across the same
	// "non-null filtered records within histogram range" subset.
	var hostTotal, popTotal int64
	for _, c := range hostHist.Bins {
		hostTotal += c
	}
	for _, c := range popHist.Bins {
		popTotal += c
	}

	bins := hostHist.Bins
	step := (hostHist.Max - hostHist.Min) / float64(len(bins))
	entries := make([]types.SeriesEntry, 0, len(bins))
	var warnings []OverlayWarning
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := range bins {
		// Build the bin label as the lower edge of the bin. Renderers
		// can format the bin range using the layer-level histogram
		// metadata (not surfaced here — the SeriesEntry key carries
		// just the lower-edge ordinal so the parallel-slice contract
		// holds).
		binLower := hostHist.Min + float64(i)*step
		key := types.AxisKey{strconv.FormatFloat(binLower, 'f', -1, 64)}

		var subsetFreq, popFreq float64
		if hostTotal > 0 {
			subsetFreq = float64(bins[i]) / float64(hostTotal)
		}
		if popTotal > 0 {
			popFreq = float64(popHist.Bins[i]) / float64(popTotal)
		}

		if popFreq == 0 {
			warnings = append(warnings, OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) +
					" population bin frequency is zero for bin " + strconv.Itoa(i),
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"host":         "facet",
					"bin_index":    i,
					"bin_lower":    binLower,
					"pop_count":    popHist.Bins[i],
					"subset_count": bins[i],
				},
			})
			entries = append(entries, types.SeriesEntry{Key: key})
			continue
		}

		index := subsetFreq / popFreq * 100.0
		if math.IsNaN(index) || math.IsInf(index, 0) {
			warnings = append(warnings, OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) +
					" produced a non-finite index for bin " + strconv.Itoa(i),
				Details: map[string]any{
					"kind":      string(spec.Kind),
					"host":      "facet",
					"bin_index": i,
				},
			})
			entries = append(entries, types.SeriesEntry{Key: key})
			continue
		}

		indexCopy := index
		entries = append(entries, types.SeriesEntry{
			Key: key,
			Summary: types.OverlaySummary{
				Statistic: &indexCopy,
			},
		})
		if seen == 0 {
			minV, maxV = index, index
		} else {
			if index < minV {
				minV = index
			}
			if index > maxV {
				maxV = index
			}
		}
		seen++
	}

	return finalizeIndexVsPopLayer(spec, entries, warnings, minV, maxV, seen), warnings, nil
}

// emptyIndexVsPopLayer builds the layer shell for the no-entries cases
// (host carries no discrete payload OR no numeric histogram). Renderers
// see a well-formed SERIES layer with zero entries — distinct from a
// missing overlay layer slot (which would mean "the request did not
// ask for this overlay at all"). The layer-level Summary carries
// Count=0 + Baseline=100 so the renderer's colour-ramp pivot stays
// consistent across populated and empty cases.
func emptyIndexVsPopLayer(spec *types.OverlaySpec) types.OverlayLayer {
	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: []types.SeriesEntry{},
			},
		},
	}
	baseline := 100.0
	zeroCount := 0
	layer.Summary = &types.OverlaySummary{
		Baseline: &baseline,
		Count:    &zeroCount,
	}
	return layer
}

// finalizeIndexVsPopLayer wraps the entries + warnings into the final
// OverlayLayer. The layer-level Summary mirrors the share / index
// family's MATRIX-host summary convention: baseline=100 (overperform >
// 100, underperform < 100), Min/Max/Count populated from present + non-
// NaN entries. The per-entry Statistic carries the renderer-facing
// detail; the layer-level Summary surfaces the colour-ramp pivot.
func finalizeIndexVsPopLayer(spec *types.OverlaySpec, entries []types.SeriesEntry, _ []OverlayWarning, minV, maxV float64, seen int) types.OverlayLayer {
	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: entries,
			},
		},
	}
	baseline := 100.0
	summary := &types.OverlaySummary{Baseline: &baseline}
	if seen > 0 {
		mn, mx, count := minV, maxV, seen
		summary.Min = &mn
		summary.Max = &mx
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}
	layer.Summary = summary
	return layer
}

// indexVsPopValueKey wraps the value string into the canonical AxisKey
// shape so the SeriesEntry keys are uniform with other SERIES overlays.
// Each entry carries a single-element tuple: AxisKey{value}.
func indexVsPopValueKey(value string) types.AxisKey {
	return types.AxisKey{value}
}

// indexVsPopValueDisplay renders the per-value string for inclusion in
// a warning message. Empty values surface as `""`; non-empty values
// surface verbatim wrapped in double quotes. Concatenation only — no
// fmt.Sprintf per the descriptor / processing no-fmt-Sprintf ban.
func indexVsPopValueDisplay(value string) string {
	if value == "" {
		return "\"\""
	}
	return "\"" + value + "\""
}
