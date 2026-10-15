---
name: op-overlay-index-vs-rolling-mean
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_ROLLING_MEAN
description: Per-point windowed index against the arithmetic mean of the W preceding points of an ordered SERIES host.
type: reference
applies_to: process
examples_tags: [overlay, time-series, window-operator]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `group`. `Ref.RollingMean` empty marker tags the ref family; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.window` required, positive width `W`. `Level` / `Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES ordered grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Window via Params, as `WIN_*`.

## Output

SERIES — one `SeriesEntry` per host group key in host order carrying `index = point / mean(prior W) × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Carrier: Welford `(count, mean, M2)` trio in a W-wide per-group ring buffer; M2 is reserved so the rolling z-score reads SD off it.
- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window <= 0` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`. Window 1 indexes each point against the previous present one (the mean of one point is the point).
- NaN entries are `statistic: null` in JSON.
- First W present ordinals → NaN, no warning (window unfilled). Absent host point → no `statistic` key, ring does NOT advance. Zero rolling mean → NaN + ONE `PULSE_OVERLAY_REF_ZERO`.
- Buffered — the ring buffer widens streaming-fold state past v1's single-state lag.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: `overlay-system`, `op-overlay-zscore-vs-rolling`, `op-overlay-index-vs-prior`.
