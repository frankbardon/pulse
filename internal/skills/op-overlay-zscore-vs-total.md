---
name: op-overlay-zscore-vs-total
kind: operator
category: OVERLAY
operator: OVERLAY_ZSCORE_VS_TOTAL
description: Per-group streamable z-score against the SERIES host's grand-total distribution (population SD).
type: reference
applies_to: process, compose
examples_tags: [overlay, outlier-detection, streaming-friendly]
---

Overlays decorate the host; no `Response.Components`.

## Params

`Scope` required, must be `group`. `Ref` empty (implicit grand-total; populated → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). `Level` / `Within` must be `0`.

## Host shape

SERIES grouped Process host. Streamable alongside `OVERLAY_INDEX_VS_TOTAL` + `OVERLAY_SHARE_OF_TOTAL`.

## Output

SERIES — one `SeriesEntry` per host group key in host order carrying `z = (group_val - mean) / sd` on `Summary.Statistic`, `sd = sqrt(M2 / N)` (**POPULATION SD**). Layer `Baseline = 0`.

## Gotchas

- **POPULATION SD**: the per-group set IS the standardisation target. Contrast `OVERLAY_ZSCORE_VS_ROLLING` (sample SD, n-1); matches `OVERLAY_ZSCORE_VS_MARGIN` and `ATTR_ZSCORE`'s denominator — but the variance is across GROUPS, not raw records.
- `sd == 0` (all groups equal, all zero, single group) → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer. Absent host group → unset entry, no Welford contribution.
- Streamable — the Welford `(count, mean, M2)` triple rides the streaming fold, byte-equal within ULP across every path.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-total`, `op-overlay-zscore-vs-rolling`.
