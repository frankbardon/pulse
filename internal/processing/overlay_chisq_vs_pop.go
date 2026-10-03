package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_CHISQ_VS_POP — single scalar χ² goodness-of-fit statistic
// against a FACET host.
//
// Inferential FACET-host kind. Pairs with the MATRIX-host CHISQ
// family (CHISQ_MATRIX / CHISQ_ROW / CHISQ_COL) as the canonical χ²
// family — the viz developer renders the SCALAR statistic as a
// goodness-of-fit badge near the facet header. Sibling FACET-host
// kinds: OVERLAY_INDEX_VS_POP (descriptive per-value index,
// streamable), OVERLAY_ZSCORE_VS_POP (descriptive per-value z-score,
// streamable). CHISQ_VS_POP emits a single scalar statistic instead
// of a per-value series.
//
// Math:
//
//	C             = subset values whose population count is > 0
//	observed[v]   = host.Discrete.Values[v].Count            // v ∈ C
//	subset_N      = Σ_{v∈C} observed[v]
//	share[v]      = pop_count[v] / Σ_{u∈C} pop_count[u]      // renormalised
//	expected[v]   = share[v] * subset_N                       // Σ expected = subset_N
//	chisq         = Σ_{v∈C} (observed[v] - expected[v])² / expected[v]
//	df            = |C| - 1
//	p_value       = chiSquareSurvival(chisq, df)  // = 1 - chi2_cdf
//
// Shares are renormalised over the compared categories (U08 E4 review
// OS-01): population null rows and categories truncated out of a
// DiscreteTopK listing never shrink the expected counts. A subset value
// the population lacks is outside C (OS-02) and reported with one
// PULSE_OVERLAY_REF_ZERO warning per value. Equals R's
// chisq.test(x, p, rescale.p = TRUE) over C.
//
// Discrete arm only: χ² goodness-of-fit requires categorical buckets to
// form the observed × expected contingency. A numeric host (no discrete
// payload) emits a coded PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// error — the validator rejects
// numeric hosts at predict time so this runtime arm is defense in
// depth.
//
// Reuses the χ² survival helper backing TEST_CHISQ and the MATRIX-host
// CHISQ family (`chiSquareSurvival` in internal/processing/test_stat.go) so the
// overlay surface produces identical p-values to TEST_CHISQ for the
// same contingency. The implementation does NOT reimplement the χ²
// CDF — every χ² surface in the codebase routes through the same
// regularised-gamma helper.
//
// Streaming finalize hook (mirrors INDEX_VS_POP / ZSCORE_VS_POP): the
// handler runs at the FacetSchema post-host-finalize entry — once the
// per-value `(value, count)` map is folded into the host
// FacetField.Discrete.Values slice the overlay reads the already-
// finalized host distribution + the resolver's population view to emit
// the SCALAR statistic. The streaming Facet pass keeps its existing
// online accumulators (Welford trio / per-value count map); the
// overlay does NOT widen the streaming carrier — it consumes finalized
// host state only. No second pass over records.
//
// Inherently BUFFERED per PRD §2 Non-Goals ("Streaming overlay path
// for inferential kinds"). Even though the runtime handler depends only
// on POST-FINALIZE state (so running it against a streaming Facet host
// vs a buffered one would produce byte-identical output), the
// streamability row in types/overlay_streamability.go is false because
// inferential overlays as a family stay buffered until a streamable-
// test path is plumbed. Distinct from the streamable INDEX_VS_POP /
// ZSCORE_VS_POP siblings which emit per-value descriptive statistics.
//
// Structural invariants:
//
//   - This file MUST NOT import internal/service/ or descriptor/. Runtime
//     overlay execution rides inside internal/processing/ alongside the rest
//     of the overlay family.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation only (the no-Sprintf ban covers
//     fmt only).
//   - Population resolution is a pure function of the host FacetResult
//     + the requested overlay field. Same FacetResult + same field
//     name → byte-equal lookup return on every invocation (mirrors the
//     INDEX_VS_POP / ZSCORE_VS_POP idempotence guarantee).

// applyChiSqVsPop is the OVERLAY_CHISQ_VS_POP runtime handler. Reads
// the host's already-finalised discrete payload and the resolver's
// population view; emits a SCALAR OverlayPayload carrying the χ²
// statistic, an OverlaySummary carrying {Statistic, PValue,
// Parameters{"df"}}, and a low-expected-cell warning when any
// expected count is below 5.
//
// Defense in depth: nil spec / host / pop fail closed with a coded
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE error — the code IS the
// CodedError's own Code; a nil spec is a caller bug with no user-facing
// code and stays PROCESSING_INTERNAL. Mirrors the INDEX_VS_POP / ZSCORE_VS_POP guard.
// Numeric host (no discrete payload) fails with the same code — the
// per-kind validator rejects numeric hosts at predict time; this
// runtime arm is defense in depth.
func applyChiSqVsPop(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []types.OverlayWarning, error) {
	if spec == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"overlay OVERLAY_CHISQ_VS_POP requires a non-nil OverlaySpec")
	}
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires a non-nil FacetField host",
			map[string]any{
				"kind": string(spec.Kind),
			})
	}
	if pop == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires a non-nil FacetPopulationView",
			map[string]any{
				"kind": string(spec.Kind),
			})
	}

	// Discrete-arm only. The per-kind validator rejects numeric hosts
	// at predict time; this runtime arm is defense in depth — a numeric
	// host (no Discrete payload) fails with the same code the
	// validator would emit.
	if host.Kind != "discrete" || host.Discrete == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires a discrete FacetField host (χ² goodness-of-fit needs categorical buckets)",
			map[string]any{
				"kind":      string(spec.Kind),
				"host_kind": host.Kind,
			})
	}

	values := host.Discrete.Values

	// Subset N: sum of per-value counts. Mirrors the
	// subsetDiscreteDenominator helper used by INDEX_VS_POP /
	// ZSCORE_VS_POP — the canonical non-null filtered-record
	// denominator for the categorical fast path.
	var subsetN int64
	for i := range values {
		subsetN += values[i].Count
	}

	// Degenerate-cohort guard: empty host distribution OR every observed
	// count is zero. No χ² test can be computed without observed counts —
	// emit a SCALAR payload with NaN statistic + NaN p-value and one
	// layer-level PULSE_OVERLAY_REF_ZERO warning. The renderer surfaces
	// "no comparison available" rather than a degenerate badge.
	if len(values) == 0 || subsetN <= 0 {
		nanStat := math.NaN()
		nanPV := math.NaN()
		zeroDf := 0.0
		warnings := []types.OverlayWarning{{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) +
				" host distribution is empty; cannot compute χ² goodness-of-fit",
			Details: map[string]any{
				"kind":     string(spec.Kind),
				"host":     "facet",
				"subset_n": subsetN,
				"value_n":  len(values),
			},
		}}
		layer := buildChiSqVsPopLayer(spec, nanStat, &nanStat, &nanPV, zeroDf, 0, math.Inf(1))
		return layer, warnings, nil
	}

	// Empty population: no shares to compare against. Checked before
	// the per-category pass so an empty population surfaces ONE
	// layer-level warning, not one per subset category.
	popCounts, popOK := pop.DiscreteCounts()
	if !popOK || popTotalIsZero(pop) {
		return chiSqVsPopDegenerate(spec, subsetN, len(values),
			" population distribution has zero total records; cannot compute χ² goodness-of-fit")
	}
	popByValue := make(map[string]int64, len(popCounts))
	for _, vc := range popCounts {
		popByValue[vc.Value] = vc.Count
	}

	// Compared categories: the subset's listed values that the
	// population also shows. The population shares are RENORMALISED
	// over exactly these categories (U08 E4 review, OS-01), so the
	// expected counts sum to the compared subset total — Pearson's
	// goodness-of-fit with matched totals, R's
	// chisq.test(x, p, rescale.p = TRUE). Dividing by the population's
	// FilteredRecords instead would count its null rows and the
	// categories truncated out of a DiscreteTopK listing, shrinking every
	// expected count and inflating χ².
	//
	// A subset category the population never shows (OS-02) is
	// impossible under the population's mix: it is left out of the
	// statistic, the compared total AND df, and reported with one
	// PULSE_OVERLAY_REF_ZERO warning per category (the INDEX_VS_POP
	// absent-category contract), so the departure is visible instead of
	// silently diluting df.
	var (
		warnings  []types.OverlayWarning
		comparedX []float64
		comparedP []float64
		observedT float64
		popCompT  float64
		observedN int
	)
	for i := range values {
		pc := popByValue[values[i].Value]
		if pc <= 0 {
			warnings = append(warnings, types.OverlayWarning{
				Code: string(errors.PULSE_OVERLAY_REF_ZERO),
				Message: "overlay " + string(spec.Kind) + " subset category " + strconv.Quote(values[i].Value) +
					" has no population rows; it is left out of the χ² comparison and df",
				Details: map[string]any{
					"kind":         string(spec.Kind),
					"host":         "facet",
					"value":        values[i].Value,
					"subset_count": values[i].Count,
				},
			})
			continue
		}
		comparedX = append(comparedX, float64(values[i].Count))
		comparedP = append(comparedP, float64(pc))
		observedT += float64(values[i].Count)
		popCompT += float64(pc)
		observedN++
	}
	if observedN == 0 || observedT <= 0 {
		return chiSqVsPopDegenerate(spec, subsetN, len(values),
			" has no subset category with population rows; cannot compute χ² goodness-of-fit")
	}

	var stat float64
	expectedMin := math.Inf(1)
	var lowExpectedCells int
	for i := range comparedX {
		expected := comparedP[i] / popCompT * observedT
		if expected < expectedMin {
			expectedMin = expected
		}
		if expected < 5 {
			lowExpectedCells++
		}
		diff := comparedX[i] - expected
		stat += diff * diff / expected
	}

	df := float64(observedN - 1)
	if df < 0 {
		df = 0
	}
	pValue := chiSquareSurvival(stat, df)

	if lowExpectedCells > 0 {
		// Canonical χ² low-expected-count warning — same code the
		// MATRIX-host CHISQ family + FISHER_EXACT_CELL surface emit
		// (PRD FR-J1 shares the code across overlay surfaces). The
		// statistic + p-value are still emitted alongside; the warning
		// flags categories where the approximation may be unreliable.
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_EXPECTED_LOW),
			Message: "overlay " + string(spec.Kind) +
				" χ² approximation may be unreliable: categories with expected count < 5 detected",
			Details: map[string]any{
				"kind":               string(spec.Kind),
				"host":               "facet",
				"low_expected_cells": lowExpectedCells,
				"expected_min":       expectedMin,
				"subset_n":           int64(observedT),
				"value_n":            observedN,
			},
		})
	}

	layer := buildChiSqVsPopLayer(spec, stat, &stat, &pValue, df, observedN, expectedMin)
	return layer, warnings, nil
}

// chiSqVsPopDegenerate emits the "no comparison available" layer: NaN
// statistic + NaN p-value, df 0, and one layer-level
// PULSE_OVERLAY_REF_ZERO warning carrying reason.
func chiSqVsPopDegenerate(spec *types.OverlaySpec, subsetN int64, valueN int, reason string) (types.OverlayLayer, []types.OverlayWarning, error) {
	nanStat := math.NaN()
	nanPV := math.NaN()
	warnings := []types.OverlayWarning{{
		Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
		Message: "overlay " + string(spec.Kind) + reason,
		Details: map[string]any{
			"kind":     string(spec.Kind),
			"host":     "facet",
			"subset_n": subsetN,
			"value_n":  valueN,
		},
	}}
	return buildChiSqVsPopLayer(spec, nanStat, &nanStat, &nanPV, 0, 0, 0), warnings, nil
}

// buildChiSqVsPopLayer wraps the χ² recurrence output into the
// canonical SCALAR-shape OverlayLayer. Mirrors applyChiSqMatrix's
// SCALAR layer construction (internal/processing/overlay.go:676) — SCALAR
// payload carries the χ² statistic on Payload.Scalar; the
// renderer-facing test result (Statistic + PValue + Parameters{"df"})
// rides on the layer-level Summary. expectedMin is the minimum
// expected-cell value (for forensic debugging via warning Details);
// observedN is the count of host values that contributed observed
// counts (used to populate Summary.Count). Pointer arguments for
// Statistic / PValue let the caller surface NaN values explicitly
// (degenerate paths) without conflating "not reported" with "NaN".
func buildChiSqVsPopLayer(spec *types.OverlaySpec, scalar float64, statPtr *float64, pvPtr *float64, df float64, observedN int, expectedMin float64) types.OverlayLayer {
	scalarCopy := scalar
	summary := &types.OverlaySummary{
		Statistic: statPtr,
		PValue:    pvPtr,
		Parameters: map[string]float64{
			"df": df,
		},
	}
	// Layer-level Summary count populates from the observed-value count.
	// Mirrors CHISQ_MATRIX which populates Count from the observed cell
	// count. expectedMin populates Min for forensic completeness when
	// the warning is suppressed (renderer-facing — the Details map
	// already carries the offending minimum when a warning fires).
	if observedN > 0 {
		count := observedN
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}
	// Layer.Summary.Baseline stays unset — inferential overlays do not
	// surface a ratio centerpoint (mirrors CHISQ_MATRIX which also leaves
	// Baseline unset; distinct from INDEX_VS_POP/ZSCORE_VS_POP which
	// surface Baseline = 100 / 0 respectively because they are
	// descriptive ratio / z-score overlays).
	_ = expectedMin

	return types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeScalar,
			Scalar: &scalarCopy,
		},
		Summary: summary,
	}
}

// popTotalIsZero reports whether the resolver's population view carries
// a zero record total (the population is empty). Used as a defense-in-
// depth guard inside the χ² recurrence — when every expected count is
// zero the statistic is identically zero, but we want to distinguish
// "trivial-zero from no-overlap" from "trivial-zero from constant
// distribution". TotalRecords() == 0 is the unambiguous "empty
// population" signal.
func popTotalIsZero(pop *FacetPopulationView) bool {
	if pop == nil {
		return true
	}
	return pop.TotalRecords() <= 0
}
