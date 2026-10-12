---
name: op-overlay-index-vs-rolling-mean
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_ROLLING_MEAN
description: Per-point windowed index against the arithmetic mean of the W preceding points of an ordered SERIES host.
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, window-operator]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `group`. `Ref.RollingMean` empty marker tags the ref family; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.window` required, positive width `W`. `Level` / `Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`.

## Host shape

SERIES ordered grouped Process host with ONE grouper; two or more `groups` (composite rows) → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.groupers`), predict and runtime. Window via Params, as `WIN_*`.

## Output

SERIES — one `SeriesEntry` per host group key in host order carrying `index = point / mean(prior W) × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Carrier: Welford `(count, mean, M2)` trio in a W-wide per-group ring buffer; M2 is reserved so the rolling z-score reads SD off it.
- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window <= 0` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.
- NaN entries are `statistic: null` in JSON.
- First W present ordinals → NaN, no warning (window unfilled). Absent host point → no `statistic` key, ring does NOT advance. Zero rolling mean → NaN + ONE `PULSE_OVERLAY_REF_ZERO`.
- Buffered — the ring buffer widens streaming-fold state past v1's single-state lag.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `op-overlay-zscore-vs-rolling`, `op-overlay-index-vs-prior`.
