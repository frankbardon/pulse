---
name: op-overlay-zscore-vs-rolling
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_ROLLING
description: Per-point windowed z-score against the rolling-window mean + SAMPLE SD of the W preceding points.
type: reference
applies_to: process, compose
examples_tags: [overlay, time-series, outlier-detection, window-operator]
---

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref.RollingMean` | object | (empty marker) | Tags ref family. |
| `params.window` | int | (required) | Positive integer; window width `W`. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

SERIES — ordered grouped Process host. Family: rolling window (`Ref.RollingMean`). Shares per-group ring buffer + Welford trio with `OVERLAY_INDEX_VS_ROLLING_MEAN`.

## Output

SERIES — one `SeriesEntry` per host group key, carrying `z = (point - rolling_mean) / rolling_sd` on `Summary.Statistic` where `rolling_sd = sqrt(M2 / (count - 1))` (**SAMPLE SD**, n-1 denominator). Layer `Baseline = 0`.

## Gotchas

- **SAMPLE SD**: rolling window IS a sample of the wider series → unbiased variance. Contrast `OVERLAY_ZSCORE_VS_TOTAL` (population SD, divide by N).
- Shares ring buffer + Welford trio with `OVERLAY_INDEX_VS_ROLLING_MEAN`; ZSCORE reads `mean` + `M2`.
- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window <= 0` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.
- `count < 2` → NaN, no warning (Welford needs ≥2).
- Zero rolling SD → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per occurrence.
- Absent host point → NaN + ring does NOT advance.
- Buffered.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-rolling-mean`, `op-overlay-zscore-vs-total`.
