package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_ZSCORE_VS_TOTAL — per-group standardized z-score against the
// host series' grand-total distribution.
//
// E3-S4 scope:
//
//   - Third and final streamable SERIES-host handler in the catalog —
//     sibling to OVERLAY_INDEX_VS_TOTAL (E3-S2) and the SERIES dispatch
//     of OVERLAY_SHARE_OF_TOTAL (E3-S3). Registered in
//     processing/overlay_series.go's seriesOverlayHandlers dispatch
//     table; the dispatch route is the post-host finalize entry point
//     for the streaming-Process orchestrator (E3-S6) AND the buffered
//     fallback entry point for any callers that materialise the host
//     series before calling into the overlay surface.
//   - Per-group math: `zscore_i = (group_val[i] - mean) / sd` where
//     `mean = Σ_j group_val[j] / N` and `sd = sqrt(M2 / N)` (POPULATION
//     variance — divide by N, not N-1) folded across the N present
//     per-group aggregated values via the single-pass Welford-Pébaÿ
//     recurrence. Absent groups (resolver reports (0, false)) are
//     skipped — they do NOT contribute to the Welford accumulator,
//     identical to the INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES grand-
//     total fold contract.
//
// Variance choice (population, not sample): the kind name says
// `_VS_TOTAL` which implies the host's per-group aggregation set IS the
// whole population we are standardising against — we are NOT inferring
// a wider population from a sample of groups. Population variance is
// the correct convention; matches the sibling buffered
// `WelfordStdDev` helper (processing/welford.go) which uses the same
// `sqrt(M2 / N)` finalisation. The CLAUDE.md "Execution modes" →
// "Parallel buffered Process" note calls out Welford-Pébaÿ as the same
// numerical convention used across the codebase so cross-mode
// equivalence tests stay byte-equal within ULP.
//
// Why single-pass Welford (and not the two-pass classical
// `Σ(x - mean)² / N` formula): the story acceptance criterion calls for
// "Welford accumulator (mean + M2) updated per group at finalize —
// single-pass over the per-group SeriesPayload". The single-pass
// recurrence is numerically stable on values of disparate magnitudes
// (the classical formula suffers from catastrophic cancellation when
// the mean and the values are similar but the variance is small) and
// it is the same convention the parallel buffered Process path uses
// for cross-mode parity. A TestOverlay_ZScoreVsTotal_WelfordULP test
// in this file's companion suite verifies the single-pass output stays
// within ULP of the two-pass classical formula across a benign input
// distribution.
//
// Zero-variance path: when `sd == 0` (every present group value is
// equal, including the every-group-zero degenerate case AND the
// single-present-group case where the Welford recurrence has no spread
// to capture) the handler emits ONE PULSE_OVERLAY_REF_ZERO warning and
// populates every entry's `Summary.Statistic` with NaN. Mirrors the
// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES zero-grand-total contract —
// one warning per layer (not per cell), full parallel-slice contract
// preserved regardless of denominator health.
//
// Streaming finalize hook: same shape as INDEX_VS_TOTAL /
// SHARE_OF_TOTAL — the streaming-Process orchestrator wiring lands in
// E3-S6, and the inner per-record hook keeps a single Welford triple
// (count + mean + M2) inside the streaming fold alongside the per-
// group accumulators. This file ships the SERIES-host POST-FINALIZE
// entry; given a finalized SeriesHostView, the handler folds Welford
// once over the host group values and emits per-group standardised
// scores. The streaming-vs-buffered byte-identity test asserts that
// running this handler against a streaming vs a buffered host produces
// byte-identical SeriesPayload output.
//
// Gotcha (per the E3-S4 story note): the streaming pass folds Welford
// over GROUPS, not raw records — variance is across N groups, not N
// records. This differs from `ATTR_ZSCORE`'s record-level semantics
// (which standardises raw cell values against their record-level mean
// and SD). The two surfaces are intentionally orthogonal: ATTR_ZSCORE
// standardises records inside a single column; OVERLAY_ZSCORE_VS_TOTAL
// standardises aggregated group totals against the per-group
// distribution.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go / overlay_index_vs_total.go /
//     overlay_share_of_total_series.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays grep-
//     clean against the structural defense ban.

// computeSeriesWelfordPopulation folds the single-pass Welford-Pébaÿ
// recurrence over every present host value in the SERIES host. Returns
// the population mean + SD plus the parallel presentMask + values
// slices the per-group emission pass consumes (so callers walk the
// host once at this entry point and keep the per-group standardised
// scores they emit without a second resolver dispatch).
//
// Sibling helper to `computeSeriesGrandTotal` (used by INDEX_VS_TOTAL
// and SHARE_OF_TOTAL SERIES) — the two helpers diverge on the
// accumulator state they carry (one f64 for grand-total kinds, three
// for the Welford triple) but share the same single-pass-over-host
// shape so the streaming-Process orchestrator (E3-S6) can keep ONE
// per-spec accumulator alongside the per-group accumulators regardless
// of which streamable SERIES kinds the request mixes.
//
// Recurrence (single-pass Welford-Pébaÿ):
//
//	delta  = x - mean_{n-1}
//	mean_n = mean_{n-1} + delta / n
//	delta2 = x - mean_n
//	M2_n   = M2_{n-1} + delta * delta2
//
// Finalisation: `sd = sqrt(M2 / N)` (POPULATION variance — divide by
// N, NOT N-1; see the kind's variance-choice rationale above).
//
// Absent groups (the resolver reports `(0, false)`) are skipped — they
// do NOT contribute to the Welford accumulator. Matches the
// SeriesHostView resolver contract and the grand-total helper's absent-
// group policy.
//
// Returns `(0, 0, empty mask, empty values, 0)` when host is nil. The
// per-handler caller treats `sd == 0` (which covers the every-group-
// zero degenerate case AND the single-present-group case AND the every-
// group-equal case) as the zero-variance path (one
// PULSE_OVERLAY_REF_ZERO warning + NaN statistics on every present
// entry). The `n` return value is the count of present groups — the
// per-handler caller pairs it with `sd == 0` to surface the right
// failure mode (every-group-absent emits an empty entries slice + a
// warning; single-present-group emits the parallel-slice contract with
// a NaN statistic on the lone present entry).
func computeSeriesWelfordPopulation(host *SeriesHostView) (mean, sd float64, presentMask []bool, values []float64, n int) {
	if host == nil {
		return 0, 0, nil, nil, 0
	}
	groupCount := host.GroupCount()
	presentMask = make([]bool, groupCount)
	values = make([]float64, groupCount)
	var m2 float64
	for i := 0; i < groupCount; i++ {
		v, ok := host.ValueAt(i)
		if !ok {
			continue
		}
		presentMask[i] = true
		values[i] = v
		n++
		// Single-pass Welford-Pébaÿ — matches WelfordStdDev (processing/
		// welford.go) and the parallel buffered Process path's
		// numerical convention so cross-mode equivalence stays byte-
		// equal within ULP.
		delta := v - mean
		mean += delta / float64(n)
		delta2 := v - mean
		m2 += delta * delta2
	}
	if n >= 2 {
		// Population variance: divide by N, not N-1. See the kind's
		// variance-choice rationale above.
		sd = math.Sqrt(m2 / float64(n))
	}
	return mean, sd, presentMask, values, n
}

// applyZScoreVsTotal is the OVERLAY_ZSCORE_VS_TOTAL runtime handler.
// For every host group ordinal it surfaces `(group_val - mean) / sd` on
// the SeriesEntry's Summary.Statistic. Walks the host group-key list
// twice:
//
//  1. First pass folds the Welford accumulator (count + mean + M2)
//     over present host values via `computeSeriesWelfordPopulation`.
//     Absent host values (resolver reports `(0, false)`) are skipped —
//     they do NOT contribute to the Welford accumulator and they do
//     NOT carry a Statistic on the emitted entry.
//  2. Second pass emits one SeriesEntry per host ordinal in host order
//     — the parallel-slice contract (FR-A2) the E3-S1 dispatch
//     established. Present groups carry Statistic = `(value - mean) /
//     sd`; absent groups carry a nil Statistic.
//
// The two-pass shape over the SERIES host is the post-finalize fold the
// streaming-Process orchestrator (E3-S6) drives at the end of the
// streaming pass — by the time ApplyOverlaysSeries calls into this
// handler the host series is finalised, so "two passes over the host"
// is structurally O(groups), not O(records). The streaming
// orchestrator's per-record fold runs ONCE over the cohort to build
// the host series + a sibling Welford triple; THIS handler then runs
// once over the finalized host. Combined cost per the PRD §6 streaming
// gate: O(records + groups × layers).
//
// Zero-variance path: `sd == 0` (which covers every-group-equal, every-
// group-zero, single-present-group, and arithmetic underflow) yields
// one PULSE_OVERLAY_REF_ZERO warning carrying the overlay kind, the
// host group count, and the present-group count `n`; every emitted
// entry's Summary.Statistic is NaN per the OverlayPayload convention.
// The handler still surfaces one entry per host group ordinal — the
// parallel-slice contract is preserved regardless of denominator
// health.
//
// Defense in depth: the descriptor validator rejects ref / scope
// shape mismatches at predict time. The handler still defends against
// a nil host (caller passed nil into ApplyOverlaysSeries) by returning
// a coded PROCESSING_INTERNAL error — that branch is unreachable in
// practice because ApplyOverlaysSeries short-circuits empty specs
// before dispatch, but the defense is cheap and matches the
// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES safety pattern.
func applyZScoreVsTotal(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	groupCount := host.GroupCount()

	// Pass 1: running Welford accumulator (count + mean + M2) via the
	// dedicated SERIES helper. Absent host values (resolver reports
	// `(0, false)`) skip the addition — this is the AGG_SUM semantics
	// the story acceptance calls out (POST-FILTER only, never reads
	// pre-filter row count) AND the Welford variant of the grand-total
	// fold INDEX_VS_TOTAL / SHARE_OF_TOTAL share.
	mean, sd, presentMask, values, n := computeSeriesWelfordPopulation(host)

	// Zero-variance path: emit one PULSE_OVERLAY_REF_ZERO warning and
	// surface NaN per the OverlayPayload convention. The entries slice
	// still carries one entry per host ordinal so the parallel-slice
	// contract holds. The check covers every-group-equal (legitimate
	// sd == 0 with n >= 2), every-group-zero, single-present-group
	// (n < 2 leaves sd at the helper's zero default), and the
	// every-group-absent degenerate case (n == 0).
	var warnings []OverlayWarning
	zeroVariance := sd == 0 || math.IsNaN(sd)
	if zeroVariance {
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " grand-total standard deviation is zero; z-scores emitted as NaN",
			Details: map[string]any{
				"kind":          string(spec.Kind),
				"host":          "series",
				"group_count":   groupCount,
				"present_count": n,
				"mean":          mean,
				"sd":            sd,
			},
		})
	}

	// Pass 2: per-host-ordinal entry emission. Absent groups carry a
	// nil Summary.Statistic (canonical "present slot, empty summary"
	// shape from E3-S1). Present groups carry Statistic = (value -
	// mean) / sd; in the zero-variance path every present group
	// carries NaN.
	entries := make([]types.SeriesEntry, 0, groupCount)
	var (
		minV float64
		maxV float64
		seen int
	)
	for i := 0; i < groupCount; i++ {
		key, _ := host.GroupKey(i)
		var keyCopy types.AxisKey
		if key != nil {
			keyCopy = append(types.AxisKey(nil), key...)
		}
		var summary types.OverlaySummary
		if presentMask[i] {
			var z float64
			if zeroVariance {
				z = math.NaN()
			} else {
				z = (values[i] - mean) / sd
			}
			zCopy := z
			summary.Statistic = &zCopy
			if !math.IsNaN(z) {
				if seen == 0 {
					minV, maxV = z, z
				} else {
					if z < minV {
						minV = z
					}
					if z > maxV {
						maxV = z
					}
				}
				seen++
			}
		}
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}

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

	// Layer-level Summary mirrors the share / index family's MATRIX-host
	// summary convention but with baseline=0 (renderers centre diverging
	// colour ramps on 0 because z-scores are unitless deviations from
	// the mean — positive = above-mean, negative = below-mean). Distinct
	// from INDEX_VS_TOTAL's baseline=100 and SHARE_OF_TOTAL SERIES'
	// baseline=1.0 because the ZSCORE family produces a centred
	// distribution, not a ratio.
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

	return layer, warnings, nil
}
