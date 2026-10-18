```yaml
name: op-overlay-index-vs-rolling-mean
kind: operator
category: OVERLAY
operator: OVERLAY_INDEX_VS_ROLLING_MEAN
description: Per-point windowed index against the arithmetic mean of the W preceding points of an ordered SERIES host.
type: reference
applies_to: process
examples_tags: [overlay, time-series, window-operator]
```

Overlays decorate the host; no `Response.Components`.

## Use when

Each point of an ordered series as an index value against the rolling mean of the previous W points: is this period above its recent run?

Questions it answers:

- Is this week's volume above or below the average of the last four weeks?
- Which days ran well above their recent average?

Use something else:

- `OVERLAY_ZSCORE_VS_ROLLING` when you want the gap in standard deviations of the window.

## Params

`Scope` must be `group`. `Ref.RollingMean` empty marker tags the ref family; other `Ref` arms → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`. `params.window` required, positive width `W`. `params.min_periods` (int, 1..`W`, default `W`) — present priors a point needs for a figure. `Level` / `Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES ordered grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment.

## Output

SERIES — one `SeriesEntry` per host group key in host order carrying `index = point / mean(prior ≤W) × 100` on `Summary.Statistic`. Layer `Baseline = 100`.

## Gotchas

- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window <= 0` or `min_periods` outside 1..`W` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`, predict and runtime. Window 1 indexes each point against the previous present one.
- Under `min_periods` present priors → `null`, no warning (default: until the window fills); below `W` the mean is over the priors held. Absent host point → no `statistic` key, ring does NOT advance. Zero rolling mean → `null` + ONE `PULSE_OVERLAY_REF_ZERO`.
- Buffered.
- Weighted host → weighted figure (reads the host payload).

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-zscore-vs-rolling`](op-overlay-zscore-vs-rolling.md), [`op-overlay-index-vs-prior`](op-overlay-index-vs-prior.md).
