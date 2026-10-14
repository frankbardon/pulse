---
name: op-overlay-zscore-vs-rolling
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_ROLLING
description: Per-point windowed z-score against the rolling-window mean + SAMPLE SD of the W preceding points.
type: reference
applies_to: process
examples_tags: [overlay, time-series, outlier-detection, window-operator]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` required, must be `group`. `Ref.RollingMean` empty marker tags the ref family. `params.window` required, positive width `W`. `Level` / `Within` must be `0`. `params.field` (string, optional) — OUTPUT LABEL of the aggregation to score (`label`, else `TYPE_field`) or an expanded element column `<label>[<elem>]` (`params.expand`); absent = `aggregations[0]`; unknown → `PULSE_OVERLAY_FIELD_UNKNOWN`. `params.partition_by` (string list, optional) — a leading prefix of the group key columns (Host shape); anything else → `PULSE_OVERLAY_PARTITION_INVALID`.

## Host shape

SERIES ordered grouped Process host. Over two or more `groups` it runs once per partition — the rows sharing the leading keys `params.partition_by` names (default all but the last, so the series runs down the last grouper; `[]` = one walk over every row), matched by key equality so a `sort` may interleave them — never crossing a segment. Shares the rolling-mean index's ring buffer + Welford trio, reading `mean` + `M2`.

## Output

SERIES — one `SeriesEntry` per host group key carrying `z = (point - rolling_mean) / rolling_sd` on `Summary.Statistic`, `rolling_sd = sqrt(M2 / (count - 1))` (**SAMPLE SD**, n-1). Layer `Baseline = 0`.

<!-- generated: reading-the-output -->

## Gotchas

- **SAMPLE SD**: the window IS a sample of the wider series → unbiased variance. Contrast the total z-score (population SD, ÷N).
- Missing `params.window` → `PULSE_OVERLAY_PARAM_MISSING`; `window <= 0` → `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE`.
- `count < 2` → NaN, no warning (Welford needs ≥2). Zero rolling SD → NaN + ONE `PULSE_OVERLAY_REF_ZERO`.
- Absent host point → NaN, and the ring does NOT advance.
- Buffered.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-rolling-mean`, `op-overlay-zscore-vs-total`.
