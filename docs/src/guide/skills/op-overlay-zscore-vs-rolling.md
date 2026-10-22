```yaml
name: op-overlay-zscore-vs-rolling
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_ROLLING
description: Per-point windowed z-score against the rolling-window mean + SAMPLE SD of the W preceding points.
type: reference
applies_to: process
examples_tags: [overlay, time-series, outlier-detection, window-operator]
```

Overlays decorate the host; no `Response.Components`.

## Use when

How many standard deviations each point sits from the rolling mean of the previous W points: a simple flag for unusual periods.

Questions it answers:

- Which days had unusually high or low orders compared with the previous two weeks?
- Which readings jumped far from their recent run?

Use something else:

- `TEST_TREND` when you want to test for a steady rise or fall across the whole series.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `field` | string | — | OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. |
| `partition_by` | list | — | A leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`. |
| `window` | int | (required) | Rolling window width in rows, a positive integer. |
| `min_periods` | int | — | Present priors a point needs for a figure. |

`Scope` required, must be `group`. `Ref.RollingMean` empty marker tags the ref family. `params.window` required, width `W` ≥ 2. `Level` / `Within` must be `0`.

## Host shape

SERIES ordered grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment.

## Output

SERIES — one `SeriesEntry` per host group key carrying `z = (point - rolling_mean) / rolling_sd` on `Summary.Statistic`, `rolling_sd = sqrt(M2 / (count - 1))` (**SAMPLE SD**, n-1). Layer `Baseline = 0`.

## Reading the output

- `summary.statistic`: In each series entry: how far the point sits from the mean of up to W points before it, in sample standard deviations of those points. 0 means the point equals its recent average.
  - Sign: positive means the value sits above its centre; negative means the value sits below its centre.
  - Caveat: There are no sourced bands for reading this z-score: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this value is built from a spread of values instead, so it has no p-value behind it.

## Contract

- Weight: not read; a weight in force is skipped.
- Streaming: buffered only.

## Gotchas

- **SAMPLE SD**: the window IS a sample of the wider series → unbiased variance. Contrast the total z-score (population SD, ÷N).
- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window < 2` or `min_periods` outside 2..`W` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (`details.min`), predict and runtime.
- Under `min_periods` present priors → `null`, no warning (default: a full window); an absent point gets no figure and never advances the ring. Zero rolling SD → `null` + ONE `PULSE_OVERLAY_REF_ZERO`.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-rolling-mean`](op-overlay-index-vs-rolling-mean.md), [`op-overlay-zscore-vs-total`](op-overlay-zscore-vs-total.md).
