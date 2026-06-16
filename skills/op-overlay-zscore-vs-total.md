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

Overlays decorate the host result; they do not emit `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Scope` | enum | (required) | Must be `group`. |
| `Ref` | object | (empty) | Implicit grand-total — leave empty. |
| `Level` / `Within` | int | `0` | Must be zero. |

## Host shape

SERIES — grouped Process host. Family: implicit grand-total (no `Ref`). Streamable alongside `OVERLAY_INDEX_VS_TOTAL` + `OVERLAY_SHARE_OF_TOTAL`.

## Output

SERIES — one `SeriesEntry` per host group key in host order, carrying `z = (group_val - mean) / sd` on `Summary.Statistic` where `sd = sqrt(M2 / N)` (**POPULATION SD**, n denominator). Layer `Baseline = 0`.

## Gotchas

- **POPULATION SD**: per-group set IS the standardisation target. Contrast `OVERLAY_ZSCORE_VS_ROLLING` (sample SD, n-1). Matches `ATTR_ZSCORE` + `OVERLAY_ZSCORE_VS_MARGIN`.
- Variance across GROUPS, not raw records — distinct from `ATTR_ZSCORE`.
- `sd == 0` (every group equal, every-group-zero, single-group) → NaN + ONE `PULSE_OVERLAY_REF_ZERO` per layer.
- Absent host group → unset entry; does NOT contribute to Welford fold.
- Populated `Ref` → `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
- Streamable — Welford `(count, mean, M2)` triple carried inside the streaming Process fold; byte-equal within ULP across serial / parallel buffered / streaming.

## See

- Skills: `overlay-system`, `op-overlay-index-vs-total`, `op-overlay-zscore-vs-rolling`.
