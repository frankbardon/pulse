package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_ZSCORE_VS_POP — per-value population-comparison z-score
// against a FACET host (E5-S3).
//
// Sibling streamable FACET-host kind to `OVERLAY_INDEX_VS_POP` (E5-S2).
// Pairs with INDEX_VS_POP as the two streamable Facet overlay kinds —
// the viz developer requesting both together gets two parallel series
// layers from a single Facet pass. Structure mirrors
// `processing/overlay_index_vs_pop.go` verbatim — same dispatch arms
// (discrete fast path + numeric path), same per-entry Summary shape,
// same zero-sd_pop warning contract.
//
// Math:
//
//   - Discrete host (categorical fast path):
//
//     subset_freq[v] = host.Discrete.Values[v].Count / sum(host counts)
//     pop_freq[v]    = FacetPopulationView.DiscreteFrequency(v)
//     sd_pop          = FacetPopulationView.DiscreteFrequencyStdev()
//     z[v]            = (subset_freq[v] - pop_freq[v]) / sd_pop
//
//     sd_pop is computed ONCE per layer from the resolver's
//     `DiscreteFrequencyStdev()` accessor (population SD over the
//     population's per-category frequencies, allocation-free). Single-
//     entry populations (n=1) and constant-frequency populations both
//     yield `sd_pop == 0` and route every entry through the zero-
//     denominator arm.
//
//   - Numeric host (Welford summary path):
//
//     For numeric fields the "value" set IS the histogram bins. Each
//     bin emits a z-score against the population's Welford summary —
//     `pop_mean` from `FacetPopulationView.NumericSummary().Mean` and
//     `pop_sd` from `FacetPopulationView.NumericSummary().StdDev`. The
//     per-bin value is the bin's lower-edge mid-point (`min +
//     (i+0.5)*step`); each entry's Summary.Statistic carries
//     `(bin_center - pop_mean) / pop_sd`. When the host carries no
//     histogram OR the population view carries no Welford summary the
//     layer emits zero entries; when `pop_sd == 0` (constant
//     population) the handler emits one layer-level zero-denominator
//     warning and zero entries.
//
// Zero-sd_pop path (per E5-S3 acceptance): when `sd_pop == 0` the
// handler emits ONE PULSE_OVERLAY_REF_ZERO warning per affected entry
// (discrete arm — value-level granularity) OR one layer-level warning
// (numeric arm — sd_pop is a single layer-scoped denominator). The
// affected entries' Summary.Statistic stays unset so the parallel-
// slice contract holds (one entry per host value).
//
// Streaming finalize hook (per E5-S3 acceptance "Streaming finalize
// hook engaged when host Facet streams; reuses Welford state from S1
// resolver"): the handler runs at the FacetSchema streaming finalize
// point — once the host FacetField is fully finalised and the
// population view's already-folded Welford state is available, the
// overlay reads against POST-FINALIZE state and emits the per-value
// z-score. No second pass over records. The streamable guarantee is
// that running this handler against a streaming Facet host vs a
// buffered one produces byte-identical SeriesPayload output because
// the input state is identical (mirrors the INDEX_VS_POP streaming
// finalize hook).
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the rest
//     of the overlay family.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation; numeric bin labels render via
//     strconv (the no-Sprintf ban covers fmt only).
//
// Forward-compat notes:
//
//   - E5-S4 (`OVERLAY_CHISQ_VS_POP`) and E5-S5 (`OVERLAY_KS_VS_POP`)
//     drop in the same way: per-kind handler file + dispatch row +
//     capability row + skill row + streamability row + manifest
//     regen. The discrete-arm population-SD accessor on
//     `FacetPopulationView` (DiscreteFrequencyStdev) is structured for
//     reuse by E5-S4 if it needs frequency variance.

// applyZScoreVsPop is the OVERLAY_ZSCORE_VS_POP runtime handler. Dispatches
// to the discrete or numeric arm depending on the host's FacetField.Kind.
//
// Defense in depth: nil spec / host / pop fail closed with a coded
// PROCESSING_INTERNAL error carrying PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// in the Details map — mirrors the INDEX_VS_POP guard. An unknown host
// kind emits an empty layer rather than failing the whole request (the
// per-kind validator should have rejected the request at predict time).
func applyZScoreVsPop(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []types.OverlayWarning, error) {
	if spec == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"overlay OVERLAY_ZSCORE_VS_POP requires a non-nil OverlaySpec")
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
		return applyZScoreVsPopDiscrete(spec, host, pop)
	case "numeric":
		return applyZScoreVsPopNumeric(spec, host, pop)
	}
	// Unknown host kind — emit an empty layer rather than failing the
	// whole request. The host shape is set by FacetSchema; an unknown
	// value here means a future kind extension landed without updating
	// this handler. Defense in depth.
	return emptyZScoreVsPopLayer(spec), nil, nil
}

// applyZScoreVsPopDiscrete walks the host's discrete `(value, count)`
// payload in payload order and emits one SeriesEntry per value with
// `Statistic = (subset_freq - pop_freq) / sd_pop`.
//
// sd_pop is the population SD across the population's per-category
// frequencies; computed ONCE up front via the resolver's
// `DiscreteFrequencyStdev()` accessor. When `sd_pop == 0` (single-entry
// population OR every category has equal frequency) the handler emits
// ONE PULSE_OVERLAY_REF_ZERO warning per affected entry and the
// entries' Statistic stays unset — mirrors the INDEX_VS_POP per-entry
// zero-denominator emission shape.
//
// When the population view does not surface sd_pop (the resolver
// returns `(0, false)` — discrete payload missing OR population total
// records is zero) the handler emits one layer-level
// PULSE_OVERLAY_REF_ZERO warning and zero entries.
func applyZScoreVsPopDiscrete(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []types.OverlayWarning, error) {
	if host.Discrete == nil {
		return emptyZScoreVsPopLayer(spec), nil, nil
	}

	sdPop, sdOk := pop.DiscreteFrequencyStdev()
	if !sdOk {
		// Population view has no usable discrete-frequency SD (no
		// discrete payload OR zero total records). One layer-level
		// warning; zero entries.
		warnings := []types.OverlayWarning{{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) +
				" requires a population discrete distribution to compute sd_pop; none present",
			Details: map[string]any{
				"kind": string(spec.Kind),
				"host": "facet",
			},
		}}
		return emptyZScoreVsPopLayer(spec), warnings, nil
	}

	subsetTotal := subsetDiscreteDenominator(host.Discrete)
	values := host.Discrete.Values

	entries := make([]types.SeriesEntry, 0, len(values))
	var warnings []types.OverlayWarning
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := range values {
		vc := values[i]
		var subsetFreq float64
		if subsetTotal > 0 {
			subsetFreq = float64(vc.Count) / float64(subsetTotal)
		}

		popFreq, popOk := pop.DiscreteFrequency(vc.Value)
		// sd_pop is layer-scoped. When sd_pop == 0 every entry hits the
		// zero-denominator path — emit one warning per affected entry
		// (mirrors INDEX_VS_POP per-entry zero-denominator emission
		// shape) and skip the Statistic.
		if sdPop == 0 {
			warnings = append(warnings, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) +
					" population frequency standard deviation is zero; cannot standardise value " + zscoreVsPopValueDisplay(vc.Value),
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"host":         "facet",
					"value":        vc.Value,
					"pop_freq":     popFreq,
					"pop_present":  popOk,
					"sd_pop":       sdPop,
					"subset_freq":  subsetFreq,
					"subset_count": vc.Count,
				},
			})
			entries = append(entries, types.SeriesEntry{
				Key: zscoreVsPopValueKey(vc.Value),
			})
			continue
		}

		// Absent population value: per the discrete-arm
		// "missing-population-entry = treat as zero" contract (mirrors
		// INDEX_VS_POP), pop_freq is 0. The z-score is well-defined
		// (`(subset_freq - 0) / sd_pop`) so emit the entry without a
		// warning. Distinct from PULSE_OVERLAY_REF_UNKNOWN which fires
		// at the resolver boundary when the named population FIELD is
		// unknown.
		_ = popOk

		zscore := (subsetFreq - popFreq) / sdPop
		if math.IsNaN(zscore) || math.IsInf(zscore, 0) {
			// Non-finite path: defense in depth against future host
			// shapes that surface NaN cells.
			warnings = append(warnings, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) +
					" produced a non-finite z-score for value " + zscoreVsPopValueDisplay(vc.Value),
				Details: map[string]any{
					"kind":  string(spec.Kind),
					"host":  "facet",
					"value": vc.Value,
				},
			})
			entries = append(entries, types.SeriesEntry{
				Key: zscoreVsPopValueKey(vc.Value),
			})
			continue
		}

		zscoreCopy := zscore
		entries = append(entries, types.SeriesEntry{
			Key: zscoreVsPopValueKey(vc.Value),
			Summary: types.OverlaySummary{
				Statistic: &zscoreCopy,
			},
		})
		if seen == 0 {
			minV, maxV = zscore, zscore
		} else {
			if zscore < minV {
				minV = zscore
			}
			if zscore > maxV {
				maxV = zscore
			}
		}
		seen++
	}

	return finalizeZScoreVsPopLayer(spec, entries, minV, maxV, seen), warnings, nil
}

// applyZScoreVsPopNumeric walks the host's numeric histogram bins and
// emits one SeriesEntry per bin with `Statistic = (bin_center -
// pop_mean) / pop_sd`. When the host carries no histogram OR the
// population view carries no Welford summary the handler emits zero
// entries. When `pop_sd == 0` (constant population) the handler emits
// one layer-level PULSE_OVERLAY_REF_ZERO warning and zero entries.
//
// Unlike INDEX_VS_POP which reads per-bin frequencies (and therefore
// requires a histogram on BOTH host and population), ZSCORE_VS_POP
// operates on the population's Welford summary directly — no per-bin
// histogram alignment is required on the population side. The host's
// histogram bins remain the per-entry value set so the SeriesPayload
// has parallel-slice alignment with the INDEX_VS_POP layer (both kinds
// emit one entry per bin in bin-index order when both are requested
// alongside).
func applyZScoreVsPopNumeric(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []types.OverlayWarning, error) {
	if host.Numeric == nil || host.Numeric.Histogram == nil {
		// Host did not carry a histogram — emit an empty layer. The
		// caller's FacetRequest may need to set IncludeHistogram=true
		// for ZSCORE_VS_POP to produce numeric output.
		return emptyZScoreVsPopLayer(spec), nil, nil
	}
	popNum, popOk := pop.NumericSummary()
	if !popOk || popNum == nil {
		// Population view has no Welford summary — every per-bin
		// z-score would lack a centerpoint. One layer-level warning;
		// zero entries.
		warnings := []types.OverlayWarning{{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) +
				" requires a population Welford summary to standardise numeric host bins; none present",
			Details: map[string]any{
				"kind": string(spec.Kind),
				"host": "facet",
			},
		}}
		return emptyZScoreVsPopLayer(spec), warnings, nil
	}
	if popNum.StdDev == 0 {
		// Constant population (every value equal) — sd_pop is zero so
		// every per-bin z-score is undefined. One layer-level warning;
		// zero entries (mirrors the INDEX_VS_POP layer-level missing-
		// histogram-pop warning shape).
		warnings := []types.OverlayWarning{{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) +
				" population standard deviation is zero; cannot standardise host bins",
			Details: map[string]any{
				"kind":     string(spec.Kind),
				"host":     "facet",
				"pop_mean": popNum.Mean,
				"sd_pop":   popNum.StdDev,
			},
		}}
		return emptyZScoreVsPopLayer(spec), warnings, nil
	}

	hostHist := host.Numeric.Histogram
	bins := hostHist.Bins
	step := (hostHist.Max - hostHist.Min) / float64(len(bins))
	entries := make([]types.SeriesEntry, 0, len(bins))
	var warnings []types.OverlayWarning
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := range bins {
		// Bin label: lower edge of the bin (matches the INDEX_VS_POP
		// numeric-arm convention so parallel-slice rendering against a
		// sibling INDEX_VS_POP layer aligns key-for-key).
		binLower := hostHist.Min + float64(i)*step
		// Bin centre: midpoint of the bin (lower + half-step). Used as
		// the per-entry value being standardised against the population
		// Welford summary.
		binCenter := binLower + 0.5*step
		key := types.AxisKey{strconv.FormatFloat(binLower, 'f', -1, 64)}

		zscore := (binCenter - popNum.Mean) / popNum.StdDev
		if math.IsNaN(zscore) || math.IsInf(zscore, 0) {
			warnings = append(warnings, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) +
					" produced a non-finite z-score for bin " + strconv.Itoa(i),
				Details: map[string]any{
					"kind":      string(spec.Kind),
					"host":      "facet",
					"bin_index": i,
				},
			})
			entries = append(entries, types.SeriesEntry{Key: key})
			continue
		}

		zscoreCopy := zscore
		entries = append(entries, types.SeriesEntry{
			Key: key,
			Summary: types.OverlaySummary{
				Statistic: &zscoreCopy,
			},
		})
		if seen == 0 {
			minV, maxV = zscore, zscore
		} else {
			if zscore < minV {
				minV = zscore
			}
			if zscore > maxV {
				maxV = zscore
			}
		}
		seen++
	}

	return finalizeZScoreVsPopLayer(spec, entries, minV, maxV, seen), warnings, nil
}

// emptyZScoreVsPopLayer builds the layer shell for the no-entries
// cases (host carries no discrete payload OR no numeric histogram).
// Renderers see a well-formed SERIES layer with zero entries —
// distinct from a missing overlay layer slot.
//
// Layer-level Summary carries Count=0 + Baseline=0 (z-score family
// centres diverging colour ramps on zero).
func emptyZScoreVsPopLayer(spec *types.OverlaySpec) types.OverlayLayer {
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
	baseline := 0.0
	zeroCount := 0
	layer.Summary = &types.OverlaySummary{
		Baseline: &baseline,
		Count:    &zeroCount,
	}
	return layer
}

// finalizeZScoreVsPopLayer wraps the entries into the final
// OverlayLayer. Mirrors the share / index family's layer-level Summary
// convention but with baseline=0 (z-score centres diverging colour
// ramps on zero, not on 100).
func finalizeZScoreVsPopLayer(spec *types.OverlaySpec, entries []types.SeriesEntry, minV, maxV float64, seen int) types.OverlayLayer {
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
	baseline := 0.0
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

// zscoreVsPopValueKey wraps the value string into the canonical AxisKey
// shape so the SeriesEntry keys are uniform with other SERIES overlays
// (one-element tuple: `AxisKey{value}`).
func zscoreVsPopValueKey(value string) types.AxisKey {
	return types.AxisKey{value}
}

// zscoreVsPopValueDisplay renders the per-value string for inclusion in
// a warning message. Empty values surface as `""`; non-empty values
// surface verbatim wrapped in double quotes. Concatenation only — no
// fmt.Sprintf per the no-fmt-Sprintf ban.
func zscoreVsPopValueDisplay(value string) string {
	if value == "" {
		return "\"\""
	}
	return "\"" + value + "\""
}
