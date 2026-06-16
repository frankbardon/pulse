package processing

import (
	"encoding/json"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_INDEX_VS_ROLLING_MEAN — per-point windowed index against the
// arithmetic mean of the W immediately preceding points of an ordered
// SERIES (grouped Process) host.
//
// Behaviour:
//
//   - Windowed-Process overlay sibling of INDEX_VS_BASELINE /
//     DELTA_VS_BASELINE / INDEX_VS_PRIOR. Registered in
//     processing/overlay_series.go's seriesOverlayHandlers dispatch
//     table; the dispatch route is the buffered post-host-finalize
//     entry point for the SERIES overlay engine.
//   - Consumes the `Ref.RollingMean` arm of the OverlayRef
//     discriminated union. The arm is an empty marker — the window
//     width lives on `OverlaySpec.Params["window"]` per the `WIN_*`
//     operator convention (`skills/window-design.md`).
//   - Per-point math: `index_i = point_value_i / mean(W prior present
//     points) * 100`. The window is bounded so naive arithmetic mean
//     is numerically fine (no Welford recurrence required); the
//     carrier still STORES the Welford triple (count, mean, M2) so
//     ZSCORE_VS_ROLLING can lift `sqrt(M2 / (count - 1))` from the
//     same ring buffer without re-folding.
//
// Forward-design for ZSCORE_VS_ROLLING: this handler only reads the
// mean from the rolling carrier. The carrier struct
// (`rollingCarrier` below) stores the Welford count + mean + M2 trio so
// the sibling story can compute the rolling SD via
// `sqrt(M2 / (count - 1))` (sample SD) or `sqrt(M2 / count)` (population
// SD) without re-opening this file. The per-group cost is +1 f64 per
// layer per group — trivial relative to the overlay grand-budget. Keep
// the Welford update path in `rollingCarrier.push` exact — the sibling
// kind reuses this same surface.
//
// Carrier shape (ring buffer over the last W present values plus a
// Welford triple for the sibling kind):
//
//   - resolver returns (0, false)  ⇒ absent host point: emit nil-Stat
//                                     entry, do NOT advance the ring.
//   - ring count < W              ⇒ window not yet filled: emit NaN
//                                     (no warning — "no comparison
//                                     available" is structurally
//                                     distinct from "denominator was
//                                     zero"). Push the value into the
//                                     ring.
//   - ring count == W             ⇒ compute mean(ring). If mean == 0
//                                     emit NaN + PULSE_OVERLAY_REF_ZERO;
//                                     else emit value/mean*100. Then
//                                     evict the oldest entry and push
//                                     the current value.
//
// Absent-point policy (ring does not advance): an absent host ordinal
// (resolver reports `(0, false)`) emits a present SeriesEntry whose
// Summary leaves Statistic unset and DOES NOT advance the ring buffer.
// Mirrors the OVERLAY_INDEX_VS_PRIOR absent-point lag carrier rule.
//
// Window param validation (runtime mirror of predict gate):
//
//   - Missing Params["window"]    ⇒ PULSE_OVERLAY_PARAM_MISSING with
//                                     {kind, param} Details.
//   - Non-integer / negative / 0  ⇒ PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
//                                     with {kind, window} Details (the
//                                     LEVEL_OUT_OF_RANGE code is reused
//                                     for "value out of valid range"
//                                     semantics on Params slots — keeps
//                                     the error code surface narrow).
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go / overlay_index_vs_prior.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban.

// rollingCarrier is the per-group rolling-window accumulator shared by
// the OVERLAY_INDEX_VS_ROLLING_MEAN handler and shared with the
// OVERLAY_ZSCORE_VS_ROLLING handler. Stores a ring
// buffer of the last `window` PRESENT values plus a Welford-Pébaÿ
// (count, mean, M2) triple updated alongside each push/evict so the
// sibling story can read SD = `sqrt(M2 / (count - 1))` without
// re-folding. INDEX_VS_ROLLING_MEAN only reads `mean`; the M2 slot is
// reserved overhead (+1 f64 per group per layer).
//
// The carrier is intentionally NOT a streaming-pass primitive in v1 —
// it lives entirely on the buffered post-host-finalize handler. When a
// future story lifts the rolling carrier into the streaming fold the
// SAME struct can ride alongside the per-group accumulators.
type rollingCarrier struct {
	window int
	buf    []float64 // ring buffer of length window; only first `count` slots populated until full
	head   int       // next write index (wraps at window)
	count  int       // number of values currently in the ring (0 .. window)
	mean   float64   // Welford running mean over the W ring entries
	m2     float64   // Welford M2 (sum of squared deviations from current mean)
}

// newRollingCarrier returns a fresh rolling carrier sized for the
// supplied window. window MUST be positive (caller-side guard); a
// non-positive window will panic on the first push via an out-of-range
// slice write — callers are expected to validate the window first
// (handler does the validation up front, before any carrier exists).
func newRollingCarrier(window int) *rollingCarrier {
	return &rollingCarrier{
		window: window,
		buf:    make([]float64, window),
	}
}

// push folds a new PRESENT value into the carrier. The ring evicts the
// oldest entry once the buffer is full; the Welford (count, mean, M2)
// triple is updated for both the inserted value AND the evicted value
// when the ring is at capacity, so the trio is exact across the rolling
// window (not the cumulative history).
//
// Welford recurrence over a sliding window: insertion uses the standard
// Welford-Pébaÿ update against the current (count, mean, M2). Eviction
// is the inverse update — we run Welford backwards using the evicted
// value's deviation from the POST-insert mean. The order matters:
// insert first, then evict, so the M2 stays non-negative across the
// rolling sequence.
func (c *rollingCarrier) push(v float64) {
	if c.count < c.window {
		// Ring still filling: standard Welford insert. No eviction yet.
		c.buf[c.head] = v
		c.head = (c.head + 1) % c.window
		c.count++
		// Welford update for sample (count, mean, M2).
		delta := v - c.mean
		c.mean += delta / float64(c.count)
		delta2 := v - c.mean
		c.m2 += delta * delta2
		return
	}
	// Ring at capacity: evict the value at head (oldest) before the
	// write replaces it. We compute the Welford-backwards update for
	// the evicted value first against the current (count, mean, M2),
	// then the forward update for the new value. The intermediate
	// (count - 1, mean', M2') describes the W-1 retained values.
	evicted := c.buf[c.head]
	// Welford-backwards: remove `evicted` from a sample of size count.
	// Inverse of the forward update — see Welford-Knuth incremental
	// variance for the sliding-window form.
	deltaOut := evicted - c.mean
	cMinus := float64(c.count - 1)
	c.mean -= deltaOut / cMinus
	deltaOut2 := evicted - c.mean
	c.m2 -= deltaOut * deltaOut2
	c.count--
	// Welford-forwards: insert v into the resulting sample.
	deltaIn := v - c.mean
	c.count++
	c.mean += deltaIn / float64(c.count)
	deltaIn2 := v - c.mean
	c.m2 += deltaIn * deltaIn2
	// Store the new value at head and advance.
	c.buf[c.head] = v
	c.head = (c.head + 1) % c.window
}

// rollingMean returns the carrier's current rolling mean. Valid only
// when the ring is at capacity (count == window) — callers gate the
// read with `c.count == c.window`.
func (c *rollingCarrier) rollingMean() float64 {
	return c.mean
}

// extractWindowParam reads OverlaySpec.Params["window"] as a positive
// integer. Returns (window, ok=true) on a valid value; returns (0,
// ok=false) and a coded error on a missing or invalid value.
//
// JSON numbers decode into Go's encoding/json as float64; the helper
// accepts integral float64 values as well as native int / int64 values
// so callers that author specs in Go OR over the wire get the same
// behaviour.
func extractWindowParam(spec *types.OverlaySpec) (int, error) {
	if len(spec.Params) == 0 {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Params[\"window\"] (positive integer); Params is missing or empty",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"kind":  string(spec.Kind),
				"param": "window",
			})
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Params must be a JSON object carrying \"window\"",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"kind":  string(spec.Kind),
				"param": "window",
			})
	}
	raw, present := m["window"]
	if !present {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Params[\"window\"] (positive integer)",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"kind":  string(spec.Kind),
				"param": "window",
			})
	}
	window, ok := coerceIntegralNumber(raw)
	if !ok {
		// Non-numeric or non-integral value — same range-out-of-bounds
		// surface as a non-positive window. LEVEL_OUT_OF_RANGE is the
		// canonical "value out of valid range" code; reusing it keeps
		// the error code surface narrow.
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Params[\"window\"] must be a positive integer",
			map[string]any{
				"code":   string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"kind":   string(spec.Kind),
				"param":  "window",
				"window": raw,
			})
	}
	if window <= 0 {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Params[\"window\"] must be > 0",
			map[string]any{
				"code":   string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"kind":   string(spec.Kind),
				"param":  "window",
				"window": window,
			})
	}
	return window, nil
}

// coerceIntegralNumber turns a JSON-decoded number into an int. Returns
// (n, true) when the input is an integer (int / int64) or an integral
// float64; (0, false) otherwise. The JSON unmarshaller decodes numbers
// as float64, so the float-but-integral arm is the common case for
// over-the-wire specs.
func coerceIntegralNumber(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		i := int(n)
		if float64(i) != n {
			return 0, false
		}
		return i, true
	case float32:
		f := float64(n)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		i := int(f)
		if float64(i) != f {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

// applyIndexVsRollingMean is the OVERLAY_INDEX_VS_ROLLING_MEAN runtime
// handler. For every host group ordinal it surfaces
// `point / mean(W prior present points) * 100` on the SeriesEntry's
// Summary.Statistic. Walks the host group-key list once with a
// rolling-window carrier (the `rollingCarrier` struct above).
//
// Window-fill semantics: the first W present ordinals all emit NaN
// without warning — "window not yet filled" is structurally distinct
// from "denominator was zero" (mirrors the INDEX_VS_PRIOR first-point
// rule). When the host series is shorter than W (no ordinal ever sees
// a full window) every entry's Statistic is NaN with no warning.
//
// Zero-rolling-mean path: when the window mean is exactly `0` at the
// divide step (e.g. every prior point in the window was zero) the
// handler emits ONE PULSE_OVERLAY_REF_ZERO warning and surfaces NaN on
// the affected entry. Subsequent points may again hit the zero-mean
// path as the ring rolls; the layer emits ONE warning per layer
// regardless of how many points hit the zero-mean arm (mirrors the
// share / index / zscore family's one-warning-per-layer contract).
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against a nil
// host (caller passed nil into ApplyOverlaysSeries) and against an
// invalid Params["window"] (the predict gate runs in parallel; the
// runtime defense is cheap and matches the INDEX_VS_PRIOR safety
// pattern).
func applyIndexVsRollingMean(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	window, err := extractWindowParam(spec)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	groupCount := host.GroupCount()
	carrier := newRollingCarrier(window)

	entries := make([]types.SeriesEntry, 0, groupCount)
	var (
		minV        float64
		maxV        float64
		seen        int
		zeroMeanHit bool
	)
	for i := 0; i < groupCount; i++ {
		key, _ := host.GroupKey(i)
		var keyCopy types.AxisKey
		if key != nil {
			keyCopy = append(types.AxisKey(nil), key...)
		}

		value, present := host.ValueAt(i)
		var summary types.OverlaySummary
		if present {
			var index float64
			switch {
			case carrier.count < window:
				// Window not yet filled. Emit NaN without warning —
				// "no comparison available" rather than "denominator
				// was zero". This entry consumes the ring slot.
				index = math.NaN()
			default:
				mean := carrier.rollingMean()
				if mean == 0 {
					index = math.NaN()
					zeroMeanHit = true
				} else {
					index = value / mean * 100.0
				}
			}
			indexCopy := index
			summary.Statistic = &indexCopy
			if !math.IsNaN(index) {
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
			// Advance the ring ONLY for present ordinals (absent host
			// points do not advance the carrier — the next present
			// point will see the same ring contents). Mirrors the
			// INDEX_VS_PRIOR absent-point lag carrier rule.
			carrier.push(value)
		}
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}

	var warnings []types.OverlayWarning
	if zeroMeanHit {
		warnings = append(warnings, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " encountered a zero rolling-mean denominator; affected indices emitted as NaN",
			Details: map[string]any{
				"kind":        string(spec.Kind),
				"host":        "series",
				"group_count": groupCount,
				"window":      window,
			},
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

	// Layer-level Summary: baseline = 100 (mirrors INDEX_VS_MARGIN /
	// INDEX_VS_TOTAL / INDEX_VS_SIBLING / INDEX_VS_PRIOR / INDEX_VS_BASELINE
	// — the index family centres diverging colour ramps on 100).
	// Min / Max / Count populate from present + non-NaN entries.
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

	return layer, warnings, nil
}
