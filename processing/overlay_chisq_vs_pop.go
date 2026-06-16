package processing

import (
	"math"

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
//	subset_N      = sum(host counts)              // discrete arm only
//	pop_freq[v]   = FacetPopulationView.DiscreteFrequency(v)
//	expected[v]   = pop_freq[v] * subset_N        // expected scaled to subset N
//	observed[v]   = host.Discrete.Values[v].Count
//	chisq         = Σ_v (observed[v] - expected[v])² / expected[v]
//	df            = len(observed) - 1
//	p_value       = chiSquareSurvival(chisq, df)  // = 1 - chi2_cdf
//
// Discrete arm only: χ² goodness-of-fit requires categorical buckets to
// form the observed × expected contingency. A numeric host (no discrete
// payload) emits a coded PROCESSING_INTERNAL error carrying
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE — the validator rejects
// numeric hosts at predict time so this runtime arm is defense in
// depth.
//
// Reuses the χ² survival helper backing TEST_CHISQ and the MATRIX-host
// CHISQ family (`chiSquareSurvival` in processing/test_stat.go) so the
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
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the rest
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
// PROCESSING_INTERNAL error carrying PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// in the Details map — mirrors the INDEX_VS_POP / ZSCORE_VS_POP guard.
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

	// Discrete-arm only. The per-kind validator rejects numeric hosts
	// at predict time; this runtime arm is defense in depth — a numeric
	// host (no Discrete payload) fails with the same code the
	// validator would emit.
	if host.Kind != "discrete" || host.Discrete == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a discrete FacetField host (χ² goodness-of-fit needs categorical buckets)",
			map[string]any{
				"code":      string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
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

	// Pre-resolve population frequencies once per value so we do not
	// repeat the resolver lookup inside the recurrence loop. Each
	// `(value, popFreq)` pair feeds one χ² term. Absent population
	// values yield popFreq = 0 → expected = 0 → term drops (mirrors the
	// MATRIX-host CHISQ family's "expected > 0" guard at
	// processing/overlay.go:623 and the TEST_CHISQ recurrence at
	// processing/test_chisq.go Finalize() — overlay and row-test
	// surfaces stay byte-equal on shared inputs).
	subsetNFloat := float64(subsetN)
	var stat float64
	expectedMin := math.Inf(1)
	var lowExpectedCells int
	var observedN int
	for i := range values {
		observed := float64(values[i].Count)
		popFreq, _ := pop.DiscreteFrequency(values[i].Value)
		expected := popFreq * subsetNFloat
		if expected < expectedMin {
			expectedMin = expected
		}
		if expected > 0 && expected < 5 {
			lowExpectedCells++
		}
		if expected > 0 {
			diff := observed - expected
			stat += diff * diff / expected
		}
		// Absent population (expected == 0): drops the χ² term (mirrors
		// TEST_CHISQ and the MATRIX-host CHISQ family). The low-expected
		// gate above counts expected ∈ (0, 5) only — a strictly-zero
		// expected does NOT increment lowExpectedCells because it is a
		// "absent from population" diagnostic, not a "low approximation
		// reliability" diagnostic. Both arms still surface the
		// degenerate-population PULSE_OVERLAY_REF_ZERO when every
		// expected is zero (the popTotal == 0 guard below).
		observedN++
	}

	// Degenerate-population guard: every expected count is zero (the
	// population is empty or carries no overlapping values). df reduces
	// to len(observed) - 1 by definition but the statistic is identically
	// zero; the chi-square test is meaningless. Surface NaN statistic +
	// NaN p-value with PULSE_OVERLAY_REF_ZERO so the renderer surfaces
	// "no comparison available" rather than a misleading p = 1.0 badge.
	if expectedMin == math.Inf(1) || (stat == 0 && popTotalIsZero(pop)) {
		nanStat := math.NaN()
		nanPV := math.NaN()
		zeroDf := float64(observedN - 1)
		if zeroDf < 0 {
			zeroDf = 0
		}
		warnings := []types.OverlayWarning{{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) +
				" population distribution has zero total records; cannot compute χ² goodness-of-fit",
			Details: map[string]any{
				"kind":     string(spec.Kind),
				"host":     "facet",
				"subset_n": subsetN,
			},
		}}
		layer := buildChiSqVsPopLayer(spec, nanStat, &nanStat, &nanPV, zeroDf, observedN, 0)
		return layer, warnings, nil
	}

	df := float64(observedN - 1)
	if df < 0 {
		df = 0
	}
	pValue := chiSquareSurvival(stat, df)

	var warnings []types.OverlayWarning
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
				"subset_n":           subsetN,
				"value_n":            observedN,
			},
		})
	}

	layer := buildChiSqVsPopLayer(spec, stat, &stat, &pValue, df, observedN, expectedMin)
	return layer, warnings, nil
}

// buildChiSqVsPopLayer wraps the χ² recurrence output into the
// canonical SCALAR-shape OverlayLayer. Mirrors applyChiSqMatrix's
// SCALAR layer construction (processing/overlay.go:676) — SCALAR
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
