```yaml
name: op-overlay-zscore-vs-rolling
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_ROLLING
description: Per-point windowed z-score against the rolling-window mean + SAMPLE SD of the W preceding points.
type: reference
applies_to: process, compose
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

`Scope` required, must be `group`. `Ref.RollingMean` empty marker tags the ref family. `params.window` required, positive width `W`. `Level` / `Within` must be `0`.

## Host shape

SERIES ordered grouped Process host. Shares the per-group ring buffer + Welford trio with the rolling-mean index, reading `mean` + `M2`.

## Output

SERIES — one `SeriesEntry` per host group key carrying `z = (point - rolling_mean) / rolling_sd` on `Summary.Statistic`, `rolling_sd = sqrt(M2 / (count - 1))` (**SAMPLE SD**, n-1). Layer `Baseline = 0`.

## Reading the output

- `summary.statistic`: In each series entry: how far the point sits from the mean of up to W points before it, in sample standard deviations of those points. 0 means the point equals its recent average.
  - Sign: positive means the value sits above its centre; negative means the value sits below its centre.
  - Caveat: There are no sourced bands for reading this z-score: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this value is built from a spread of values instead, so it has no p-value behind it.

## Gotchas

- **SAMPLE SD**: the window IS a sample of the wider series → unbiased variance. Contrast the total z-score (population SD, ÷N).
- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window <= 0` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.
- `count < 2` → NaN, no warning (Welford needs ≥2). Zero rolling SD → NaN + ONE `PULSE_OVERLAY_REF_ZERO`.
- Absent host point → NaN, and the ring does NOT advance.
- Buffered.

## See

- Skills: [`overlay-system`](overlay-system.md), [`op-overlay-index-vs-rolling-mean`](op-overlay-index-vs-rolling-mean.md), [`op-overlay-zscore-vs-total`](op-overlay-zscore-vs-total.md).
