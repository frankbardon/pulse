package processing

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_KS_VS_POP — single scalar Kolmogorov-Smirnov distance + asymptotic
// p-value comparing the host Facet subset numeric distribution against the
// resolved population numeric distribution (E5-S5).
//
// Fourth and final FACET-host kind in the catalog. Second inferential
// FACET-host kind — sibling to OVERLAY_CHISQ_VS_POP. KS pairs with
// CHISQ_VS_POP as the two inferential FACET-host kinds, partitioning the
// inferential FACET surface by host arm:
//
//   - CHISQ_VS_POP: discrete-arm only (categorical buckets).
//   - KS_VS_POP:    numeric-arm only (continuous CDF).
//
// Sibling descriptive FACET-host kinds shipped earlier in E5:
// OVERLAY_INDEX_VS_POP / E5-S2 (per-value index, streamable) and
// OVERLAY_ZSCORE_VS_POP / E5-S3 (per-value z-score, streamable). KS_VS_POP
// closes the FACET-host overlay catalog by emitting a single SCALAR
// statistic over the numeric arm.
//
// Math:
//
//	D       = sup_x |F_subset(x) - F_pop(x)|       // KS distance
//	n_subset = host.Numeric.Count
//	n_pop    = pop.NumericSummary().Count
//	en      = sqrt(n_subset * n_pop / (n_subset + n_pop))
//	p_value = kolmogorovSurvival((en + 0.12 + 0.11/en) * D)
//
// Reuses the same Kolmogorov-survival helper backing TEST_KS
// (kolmogorovSurvival in processing/test_stat.go) so the overlay and the
// row-test surface produce identical p-values for the same D + N. The KS
// distance itself is hand-rolled from the available CDF reconstruction
// (histogram or percentiles) — the existing ksTwoSampleD helper in
// processing/test_ks.go requires raw sorted values which the population
// view does not retain.
//
// Population-sample availability (KEY DESIGN NOTE): the S1 resolver
// exposes only summary state for numeric fields — Welford
// mean/stdev/count, an optional percentile map, and an optional
// fixed-width histogram. Raw values were folded into Welford state at
// FacetSchema fold time and discarded. KS requires two empirical CDFs, so
// the handler picks the highest-precision reconstruction available:
//
//   1. HISTOGRAM PATH (preferred) when host AND population both surface a
//      histogram with matching `Min`/`Max`/bucket count. Aligns bucket
//      edges and computes sup|F_subset(edge_i) - F_pop(edge_i)| over the
//      bucket-fraction cumulatives at common edges. KS precision is
//      bounded by bucket width.
//   2. PERCENTILE PATH (fallback) when histograms are absent or mismatched
//      but BOTH arms surface a percentile map. Turns each percentile entry
//      into a CDF sample point (`p25` → CDF=0.25 at the percentile's
//      value) and computes sup-CDF-difference over the common percentile
//      labels. Coarser than histograms but well-defined.
//   3. WELFORD-ONLY DEGENERATE when neither path applies. NaN statistic +
//      NaN p-value + one PULSE_OVERLAY_REF_ZERO warning whose Details
//      document which knobs were missing on the FacetRequest.
//
// Inherently BUFFERED per PRD §2 Non-Goals ("Streaming overlay path for
// inferential kinds"). The streamability row in
// types/overlay_streamability.go is `false` regardless of host
// streamability. A future story may extend the S1 resolver to retain raw
// sorted population values when a KS overlay is requested; the handler
// would then call ksTwoSampleD directly on raw arms (additive change).
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime overlay
//     execution rides inside processing/ alongside the rest of the
//     overlay family.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are built
//     with string concatenation only.
//   - Numeric arm only. A categorical host (no numeric payload) fails
//     closed with errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED — the per-kind
//     validator (E5-S10) rejects categorical hosts at predict time; this
//     runtime arm is defense in depth.

// applyKSVsPop is the OVERLAY_KS_VS_POP runtime handler. Reads the host's
// already-finalised numeric payload (histogram / percentiles) and the
// resolver's population numeric summary; emits a SCALAR OverlayPayload
// carrying the KS distance D, an OverlaySummary carrying {Statistic,
// PValue, Parameters{"n_subset", "n_pop"}}, and one PULSE_OVERLAY_REF_ZERO
// warning when the empirical-CDF reconstruction cannot proceed (empty
// host, empty population, or no histogram/percentile knobs available on
// either arm).
//
// Defense in depth: nil spec / host / pop fail closed with a coded
// PROCESSING_INTERNAL error carrying PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// in the Details map (mirrors INDEX_VS_POP / ZSCORE_VS_POP / CHISQ_VS_POP).
// Categorical host (no numeric payload) fails with
// PULSE_OVERLAY_SCOPE_UNSUPPORTED — the per-kind validator (E5-S10) rejects
// categorical hosts at predict time; this runtime arm is defense in depth.
func applyKSVsPop(spec *types.OverlaySpec, host *types.FacetField, pop *FacetPopulationView) (types.OverlayLayer, []OverlayWarning, error) {
	if spec == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedError(
			errors.PROCESSING_INTERNAL,
			"overlay OVERLAY_KS_VS_POP requires a non-nil OverlaySpec")
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

	// Numeric-arm only. The per-kind validator (E5-S10) rejects
	// categorical hosts at predict time; this runtime arm is defense in
	// depth — a categorical host (no Numeric payload) fails closed with
	// PULSE_OVERLAY_SCOPE_UNSUPPORTED (KS is undefined on categorical
	// distributions: no continuous CDF to compare).
	if host.Kind != "numeric" || host.Numeric == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a numeric FacetField host (KS is undefined on categorical distributions)",
			map[string]any{
				"code":      string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
				"kind":      string(spec.Kind),
				"host_kind": host.Kind,
			})
	}

	popNum, popOK := pop.NumericSummary()
	if !popOK || popNum == nil {
		// Population resolver reports the field is non-numeric or carries
		// no Welford payload. Defense in depth — the resolver itself
		// surfaces PULSE_OVERLAY_REF_UNKNOWN at the unknown-field boundary
		// BEFORE the handler runs. Reaching this branch means the
		// population's field shape diverges from the host's — a fatal
		// fixture inconsistency. Surface PULSE_OVERLAY_SCOPE_UNSUPPORTED
		// to match the categorical-host rejection above (the population's
		// numeric arm is structurally unavailable).
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a numeric FacetPopulationView (population arm has no numeric payload)",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
				"kind": string(spec.Kind),
			})
	}

	nSubset := host.Numeric.Count
	nPop := popNum.Count

	// Degenerate-cohort guard: empty host distribution OR empty population.
	// No KS distance can be computed without samples on both sides. Emit a
	// SCALAR payload with NaN statistic + NaN p-value and one layer-level
	// PULSE_OVERLAY_REF_ZERO warning.
	if nSubset <= 0 || nPop <= 0 {
		layer, warnings := ksDegenerateLayer(spec, nSubset, nPop,
			"overlay "+string(spec.Kind)+
				" requires non-empty distributions on both arms; cannot compute KS distance",
			map[string]any{
				"kind":     string(spec.Kind),
				"host":     "facet",
				"n_subset": nSubset,
				"n_pop":    nPop,
				"reason":   "empty_arm",
			})
		return layer, warnings, nil
	}

	// Pick reconstruction path in priority order:
	//   1. Histogram (both arms with matching shape) — best precision.
	//   2. Percentile (both arms with non-empty maps) — fallback.
	//   3. Welford-only — degenerate, emit NaN + PULSE_OVERLAY_REF_ZERO.

	D, pathOK := ksDistanceHistogram(host.Numeric.Histogram, popNum.Histogram)
	if !pathOK {
		D, pathOK = ksDistancePercentiles(host.Numeric.Percentiles, popNum.Percentiles)
	}
	if !pathOK {
		// Welford-only degenerate: caller did not request a histogram or
		// percentiles on EITHER arm (or the two arms diverge). Emit NaN +
		// NaN with PULSE_OVERLAY_REF_ZERO whose Details document which
		// knobs were missing.
		hostHasHist := host.Numeric.Histogram != nil && len(host.Numeric.Histogram.Bins) > 0
		popHasHist := popNum.Histogram != nil && len(popNum.Histogram.Bins) > 0
		hostHasPct := len(host.Numeric.Percentiles) > 0
		popHasPct := len(popNum.Percentiles) > 0
		layer, warnings := ksDegenerateLayer(spec, nSubset, nPop,
			"overlay "+string(spec.Kind)+
				" cannot reconstruct empirical CDFs: neither histogram nor percentile maps available on both arms",
			map[string]any{
				"kind":                 string(spec.Kind),
				"host":                 "facet",
				"n_subset":             nSubset,
				"n_pop":                nPop,
				"reason":               "no_reconstruction_path",
				"host_has_histogram":   hostHasHist,
				"pop_has_histogram":    popHasHist,
				"host_has_percentiles": hostHasPct,
				"pop_has_percentiles":  popHasPct,
			})
		return layer, warnings, nil
	}

	// Asymptotic p-value via the Kolmogorov-survival helper that backs
	// TEST_KS — overlay and row-test surfaces produce identical p-values
	// for the same D + N.
	en := math.Sqrt(float64(nSubset*nPop) / float64(nSubset+nPop))
	pValue := kolmogorovSurvival((en + 0.12 + 0.11/en) * D)

	layer := buildKSVsPopLayer(spec, D, &D, &pValue, nSubset, nPop)
	return layer, nil, nil
}

// ksDistanceHistogram computes the KS sup-CDF-difference over aligned
// histogram edges. Returns (distance, true) when both arms carry a
// histogram with matching `Min`/`Max`/bucket count; (0, false) when the
// path is not applicable (missing or mismatched arm).
//
// The CDF at edge i is the cumulative bucket-fraction sum across bins
// [0..i-1] divided by the arm's total record count. Edge 0 always reads
// CDF=0; the last edge reads CDF=1. The KS distance is the maximum of
// |F_subset(edge_i) - F_pop(edge_i)| across all common edges.
//
// Precision is bounded by bucket width: finer histograms yield finer KS
// distance.
func ksDistanceHistogram(hostHist, popHist *types.FacetHistogram) (float64, bool) {
	if hostHist == nil || popHist == nil {
		return 0, false
	}
	if len(hostHist.Bins) == 0 || len(popHist.Bins) == 0 {
		return 0, false
	}
	// Mismatched shapes fall through to the percentile path (per the spec
	// "histograms with mismatched edges fall through").
	if hostHist.Min != popHist.Min || hostHist.Max != popHist.Max {
		return 0, false
	}
	if len(hostHist.Bins) != len(popHist.Bins) {
		return 0, false
	}

	// Aggregate arm totals.
	var hostTotal, popTotal int64
	for i := range hostHist.Bins {
		hostTotal += hostHist.Bins[i]
		popTotal += popHist.Bins[i]
	}
	if hostTotal == 0 || popTotal == 0 {
		// Degenerate histogram totals — the empty-arm guard upstream
		// should catch this; defense in depth.
		return 0, false
	}

	// Walk the cumulative bucket-fraction CDF on both sides; track the
	// maximum absolute difference. Both CDFs anchor at 0 (edge before bin
	// 0) and converge at 1 (edge after the last bin), so the sup is taken
	// over the inner edges.
	var hostCum, popCum int64
	D := 0.0
	hostTotalF := float64(hostTotal)
	popTotalF := float64(popTotal)
	for i := range hostHist.Bins {
		hostCum += hostHist.Bins[i]
		popCum += popHist.Bins[i]
		diff := math.Abs(float64(hostCum)/hostTotalF - float64(popCum)/popTotalF)
		if diff > D {
			D = diff
		}
	}
	return D, true
}

// ksDistancePercentiles computes the KS sup-CDF-difference using
// percentile-anchor x-values from BOTH arms as candidate points. Returns
// (distance, true) when both arms carry a non-empty percentile map with
// at least one parseable label per arm; (0, false) when the path is not
// applicable (empty map or no parseable labels).
//
// Each percentile entry on each arm provides a (value, CDF) sample point
// (label `"p25"` → CDF=0.25 at the percentile's value). The handler
// linearly interpolates each arm's CDF and computes |F_subset(x) -
// F_pop(x)| at every percentile-anchor x-value from both arms. The
// returned KS distance is the maximum over all candidate x-values.
//
// Lower precision than histograms (a handful of CDF samples per side vs
// hundreds of bucket edges) but well-defined and consistent with the
// canonical KS formulation.
//
// Invalid percentile labels (anything that does not parse into a `[0, 1]`
// CDF value) are filtered out so callers carrying free-form keys stay
// safe — those keys do not contribute to the sup but do not poison the
// result.
func ksDistancePercentiles(hostPct, popPct map[string]float64) (float64, bool) {
	if len(hostPct) == 0 || len(popPct) == 0 {
		return 0, false
	}
	// Both arms must surface at least one parseable percentile label;
	// otherwise the CDF interpolation is undefined.
	if !mapHasParseablePercentile(hostPct) || !mapHasParseablePercentile(popPct) {
		return 0, false
	}

	// Candidate x-values: every percentile-anchor value from either arm.
	// At each candidate the KS contribution is |F_subset(x) - F_pop(x)|
	// where each F is the piecewise-linear interpolant of its arm's
	// percentile map.
	var candidates []float64
	for label, value := range hostPct {
		if _, ok := percentileLabelToCDF(label); ok {
			candidates = append(candidates, value)
		}
	}
	for label, value := range popPct {
		if _, ok := percentileLabelToCDF(label); ok {
			candidates = append(candidates, value)
		}
	}
	if len(candidates) == 0 {
		return 0, false
	}
	sort.Float64s(candidates)

	D := 0.0
	for _, x := range candidates {
		subsetCDF := interpolatePercentileCDF(x, hostPct)
		popCDF := interpolatePercentileCDF(x, popPct)
		diff := math.Abs(subsetCDF - popCDF)
		if diff > D {
			D = diff
		}
	}
	return D, true
}

// mapHasParseablePercentile reports whether the percentile map has at
// least one parseable `pNN` label. Used by ksDistancePercentiles to
// short-circuit when both arms carry only free-form keys.
func mapHasParseablePercentile(pct map[string]float64) bool {
	for label := range pct {
		if _, ok := percentileLabelToCDF(label); ok {
			return true
		}
	}
	return false
}

// percentileLabelToCDF parses a `"pNN"` or `"pNN.M"` percentile label into
// its CDF value in `[0, 1]`. Returns (cdf, true) on a valid label;
// (0, false) on a malformed string.
//
// Labels are case-insensitive in the `p` prefix; the numeric tail is
// parsed with the strconv-equivalent rules but inline to keep the
// allocations down. Out-of-range values (`< 0` or `> 100`) return false
// so the caller can filter them out.
func percentileLabelToCDF(label string) (float64, bool) {
	if len(label) < 2 {
		return 0, false
	}
	if label[0] != 'p' && label[0] != 'P' {
		return 0, false
	}
	// Parse the numeric tail by hand (no strconv to avoid an extra import
	// and to keep the parse inline for hot-loop callers).
	var v float64
	var sawDot bool
	var fracScale float64 = 1
	for i := 1; i < len(label); i++ {
		c := label[i]
		if c >= '0' && c <= '9' {
			if sawDot {
				fracScale *= 10
				v += float64(c-'0') / fracScale
			} else {
				v = v*10 + float64(c-'0')
			}
			continue
		}
		if c == '.' && !sawDot {
			sawDot = true
			continue
		}
		return 0, false
	}
	if v < 0 || v > 100 {
		return 0, false
	}
	return v / 100, true
}

// interpolatePercentileCDF returns the CDF value at `x` interpolated from
// the percentile map. Walks the map's percentile points (sorted by value)
// and emits a piecewise-linear CDF approximation: each consecutive
// percentile pair `(value_i, cdf_i), (value_{i+1}, cdf_{i+1})` defines a
// segment, and the CDF at `x` reads linearly along the segment containing
// `x`. Out-of-range values clamp to 0 (below the smallest percentile) or
// 1 (above the largest).
//
// Invalid labels are filtered out so callers carrying free-form keys
// stay safe.
func interpolatePercentileCDF(x float64, pct map[string]float64) float64 {
	type pcPair struct {
		value float64
		cdf   float64
	}
	var pairs []pcPair
	for label, val := range pct {
		c, ok := percentileLabelToCDF(label)
		if !ok {
			continue
		}
		pairs = append(pairs, pcPair{value: val, cdf: c})
	}
	if len(pairs) == 0 {
		return 0
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].value < pairs[j].value
	})

	if x <= pairs[0].value {
		return pairs[0].cdf
	}
	if x >= pairs[len(pairs)-1].value {
		return pairs[len(pairs)-1].cdf
	}
	for i := 0; i < len(pairs)-1; i++ {
		lo := pairs[i]
		hi := pairs[i+1]
		if x >= lo.value && x <= hi.value {
			if hi.value == lo.value {
				return (lo.cdf + hi.cdf) / 2
			}
			t := (x - lo.value) / (hi.value - lo.value)
			return lo.cdf + t*(hi.cdf-lo.cdf)
		}
	}
	return pairs[len(pairs)-1].cdf
}

// ksDegenerateLayer wraps the NaN + NaN + PULSE_OVERLAY_REF_ZERO
// degenerate path into the canonical SCALAR-shape OverlayLayer plus the
// matching warning slice. The warning Details map carries the offending
// arm sizes and the reconstruction-failure reason so the renderer can
// surface a fixup hint ("turn on IncludeHistogram=true or
// NumericPercentiles=[...] on both FacetRequests").
func ksDegenerateLayer(spec *types.OverlaySpec, nSubset, nPop int64, message string, details map[string]any) (types.OverlayLayer, []OverlayWarning) {
	nanStat := math.NaN()
	nanPV := math.NaN()
	layer := buildKSVsPopLayer(spec, nanStat, &nanStat, &nanPV, nSubset, nPop)
	warnings := []OverlayWarning{{
		Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
		Message: message,
		Details: details,
	}}
	return layer, warnings
}

// buildKSVsPopLayer wraps the KS recurrence output into the canonical
// SCALAR-shape OverlayLayer. Mirrors buildChiSqVsPopLayer
// (processing/overlay_chisq_vs_pop.go) — SCALAR payload carries the KS
// distance D on Payload.Scalar; the renderer-facing test result
// (Statistic + PValue + Parameters{"n_subset", "n_pop"}) rides on the
// layer-level Summary. Pointer arguments for Statistic / PValue let the
// caller surface NaN values explicitly (degenerate paths) without
// conflating "not reported" with "NaN".
func buildKSVsPopLayer(spec *types.OverlaySpec, scalar float64, statPtr, pvPtr *float64, nSubset, nPop int64) types.OverlayLayer {
	scalarCopy := scalar
	summary := &types.OverlaySummary{
		Statistic: statPtr,
		PValue:    pvPtr,
		Parameters: map[string]float64{
			"n_subset": float64(nSubset),
			"n_pop":    float64(nPop),
		},
	}
	// Layer.Summary.Baseline stays unset — inferential overlays do not
	// surface a ratio centerpoint (mirrors CHISQ_VS_POP / CHISQ_MATRIX;
	// distinct from INDEX_VS_POP/ZSCORE_VS_POP which surface Baseline =
	// 100 / 0 respectively because they are descriptive ratio / z-score
	// overlays).

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
